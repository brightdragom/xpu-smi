package amd

import (
	"context"
	"encoding/json"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"xpu-smi/internal/collector"
)

// numberRe grabs the leading numeric token from a string such as "45.0",
// "35.0 W" or "1500Mhz", so string-typed CLI values still parse.
var numberRe = regexp.MustCompile(`[-+]?[0-9]*\.?[0-9]+`)

// runJSON executes a tool with a self-bounded timeout and unmarshals its stdout
// into v. It returns an error if the tool is absent, fails, times out, or emits
// non-JSON. The timeout derives from ctx, so it also honors caller cancellation.
func runJSON(ctx context.Context, v interface{}, name string, args ...string) error {
	if _, err := exec.LookPath(name); err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()

	out, err := exec.CommandContext(cctx, name, args...).Output()
	if err != nil {
		return err
	}
	return json.Unmarshal(out, v)
}

// ---------------------------------------------------------------------------
// amd-smi metric --json  (ROCm 6+, preferred)
// ---------------------------------------------------------------------------

// collectAMDSMI parses `amd-smi metric --json`. Modern amd-smi emits a JSON
// array of per-GPU objects with nested {value, unit} leaves; some builds wrap
// it in a top-level object keyed by gpu id. Both shapes are handled.
func (a *adapter) collectAMDSMI(ctx context.Context) ([]collector.GPUMetrics, error) {
	var raw json.RawMessage
	if err := runJSON(ctx, &raw, "amd-smi", "metric", "--json"); err != nil {
		return nil, err
	}

	gpus, err := decodeGPUList(raw)
	if err != nil {
		return nil, err
	}

	var out []collector.GPUMetrics
	for i, gpu := range gpus {
		idx := i
		if v, ok := toFloat(gpu["gpu"]); ok {
			idx = int(v)
		}
		m := collector.NewMetrics(vendorName, idx)
		if name, ok := gpu["market_name"].(string); ok {
			m.Name = name
		}

		if usage := asMap(gpu["usage"]); usage != nil {
			if v, ok := toFloat(usage["gfx_activity"]); ok {
				m.UtilizationGPUPercent = v
				m.MarkSupported(collector.FieldUtilizationGPUPercent)
			}
			if v, ok := toFloat(usage["umc_activity"]); ok {
				m.UtilizationMemoryPercent = v
				m.MarkSupported(collector.FieldUtilizationMemoryPercent)
			}
		}

		if power := asMap(gpu["power"]); power != nil {
			// socket_power (W) is the modern key; average_socket_power is older.
			if v, ok := firstFloat(power, "socket_power", "average_socket_power", "current_socket_power"); ok {
				m.PowerWatts = v
				m.MarkSupported(collector.FieldPowerWatts)
			}
		}

		if temp := asMap(gpu["temperature"]); temp != nil {
			if v, ok := firstFloat(temp, "edge", "hotspot", "junction"); ok {
				m.TemperatureCelsius = v
				m.MarkSupported(collector.FieldTemperatureCelsius)
			}
		}

		if mem := asMap(gpu["mem_usage"]); mem != nil {
			if v, ok := firstBytes(mem, "used_vram", "used_visible_vram"); ok {
				m.MemoryUsedBytes = v
				m.MarkSupported(collector.FieldMemoryUsedBytes)
			}
			if v, ok := firstBytes(mem, "total_vram", "total_visible_vram"); ok {
				m.MemoryTotalBytes = v
				m.MarkSupported(collector.FieldMemoryTotalBytes)
			}
		}

		if clock := asMap(gpu["clock"]); clock != nil {
			if v, ok := clockMHz(clock, "gfx_0", "gfx"); ok {
				m.ClockGraphicsMHz = v
				m.MarkSupported(collector.FieldClockGraphicsMHz)
			}
			if v, ok := clockMHz(clock, "mem_0", "mem"); ok {
				m.ClockMemoryMHz = v
				m.MarkSupported(collector.FieldClockMemoryMHz)
			}
		}

		out = append(out, m)
	}
	return out, nil
}

// decodeGPUList normalizes amd-smi output (array or gpu-keyed object) into a
// slice of per-GPU maps.
func decodeGPUList(raw json.RawMessage) ([]map[string]interface{}, error) {
	var arr []map[string]interface{}
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr, nil
	}
	var obj map[string]map[string]interface{}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	list := make([]map[string]interface{}, 0, len(obj))
	for _, k := range keys {
		list = append(list, obj[k])
	}
	return list, nil
}

// ---------------------------------------------------------------------------
// rocm-smi --json  (older, widely deployed)
// ---------------------------------------------------------------------------

// collectROCmSMI parses rocm-smi's card-keyed JSON. Values are flat and usually
// strings; key names drift between versions, so lookups try several candidates
// and fall back to a case-insensitive substring match.
func (a *adapter) collectROCmSMI(ctx context.Context) ([]collector.GPUMetrics, error) {
	var cards map[string]map[string]interface{}
	if err := runJSON(ctx, &cards, "rocm-smi",
		"--showuse", "--showmemuse", "--showtemp", "--showpower",
		"--showmeminfo", "vram", "--showclocks", "--json"); err != nil {
		return nil, err
	}

	keys := make([]string, 0, len(cards))
	for k := range cards {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var out []collector.GPUMetrics
	for i, key := range keys {
		card := cards[key]
		idx := i
		if n, ok := trailingInt(key); ok {
			idx = n
		}
		m := collector.NewMetrics(vendorName, idx)

		if v, ok := findFloat(card, "GPU use (%)", "GPU Activity"); ok {
			m.UtilizationGPUPercent = v
			m.MarkSupported(collector.FieldUtilizationGPUPercent)
		}
		if v, ok := findFloat(card, "GPU Memory Allocated (VRAM%)", "GPU memory use (%)", "Memory Activity"); ok {
			m.UtilizationMemoryPercent = v
			m.MarkSupported(collector.FieldUtilizationMemoryPercent)
		}
		if v, ok := findFloat(card, "Temperature (Sensor edge) (C)", "Temperature (Sensor junction) (C)"); ok {
			m.TemperatureCelsius = v
			m.MarkSupported(collector.FieldTemperatureCelsius)
		}
		if v, ok := findFloat(card,
			"Average Graphics Package Power (W)",
			"Current Socket Graphics Package Power (W)"); ok {
			m.PowerWatts = v
			m.MarkSupported(collector.FieldPowerWatts)
		}
		if v, ok := findBytes(card, "VRAM Total Used Memory (B)"); ok {
			m.MemoryUsedBytes = v
			m.MarkSupported(collector.FieldMemoryUsedBytes)
		}
		if v, ok := findBytes(card, "VRAM Total Memory (B)"); ok {
			m.MemoryTotalBytes = v
			m.MarkSupported(collector.FieldMemoryTotalBytes)
		}
		if v, ok := findFloat(card, "sclk clock speed:", "sclk clock level:"); ok {
			m.ClockGraphicsMHz = uint32(v)
			m.MarkSupported(collector.FieldClockGraphicsMHz)
		}
		if v, ok := findFloat(card, "mclk clock speed:", "mclk clock level:"); ok {
			m.ClockMemoryMHz = uint32(v)
			m.MarkSupported(collector.FieldClockMemoryMHz)
		}

		out = append(out, m)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// shared value helpers (handle both nested {value,unit} and flat string forms)
// ---------------------------------------------------------------------------

// asMap returns v as a JSON object, or nil.
func asMap(v interface{}) map[string]interface{} {
	m, _ := v.(map[string]interface{})
	return m
}

// toFloat extracts a float from a JSON number, a numeric string (with optional
// unit suffix), or a {"value": ...} wrapper object. Returns false when no
// numeric value is present, so the caller can leave the field unsupported.
func toFloat(v interface{}) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case string:
		tok := numberRe.FindString(x)
		if tok == "" {
			return 0, false
		}
		f, err := strconv.ParseFloat(tok, 64)
		return f, err == nil
	case map[string]interface{}:
		if val, ok := x["value"]; ok {
			return toFloat(val)
		}
	}
	return 0, false
}

// unitOf returns the "unit" string of a {value,unit} node, or "".
func unitOf(v interface{}) string {
	if m, ok := v.(map[string]interface{}); ok {
		if u, ok := m["unit"].(string); ok {
			return u
		}
	}
	return ""
}

// toBytes extracts a byte count, scaling by the node's unit when present.
// A missing or unrecognized unit is treated as bytes.
func toBytes(v interface{}) (uint64, bool) {
	f, ok := toFloat(v)
	if !ok || f < 0 {
		return 0, false
	}
	switch strings.ToUpper(strings.TrimSpace(unitOf(v))) {
	case "KB":
		f *= 1024
	case "MB":
		f *= 1024 * 1024
	case "GB":
		f *= 1024 * 1024 * 1024
	default:
		// "", "B", "BYTES" or unknown → assume bytes.
	}
	return uint64(f), true
}

// firstFloat returns the first candidate key that yields a float.
func firstFloat(m map[string]interface{}, keys ...string) (float64, bool) {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if f, ok := toFloat(v); ok {
				return f, true
			}
		}
	}
	return 0, false
}

// firstBytes returns the first candidate key that yields a byte count.
func firstBytes(m map[string]interface{}, keys ...string) (uint64, bool) {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if b, ok := toBytes(v); ok {
				return b, true
			}
		}
	}
	return 0, false
}

// clockMHz reads an amd-smi clock domain (e.g. {"clk": {value, unit}}).
func clockMHz(clock map[string]interface{}, keys ...string) (uint32, bool) {
	for _, k := range keys {
		domain := asMap(clock[k])
		if domain == nil {
			continue
		}
		if v, ok := toFloat(domain["clk"]); ok {
			return uint32(v), true
		}
		if v, ok := toFloat(domain); ok { // some builds inline the value
			return uint32(v), true
		}
	}
	return 0, false
}

// findFloat looks up a rocm-smi field: exact candidate keys first, then a
// case-insensitive substring match to tolerate version-to-version key drift.
func findFloat(card map[string]interface{}, candidates ...string) (float64, bool) {
	for _, c := range candidates {
		if v, ok := card[c]; ok {
			if f, ok := toFloat(v); ok {
				return f, true
			}
		}
	}
	for _, c := range candidates {
		lc := strings.ToLower(c)
		for k, v := range card {
			if strings.Contains(strings.ToLower(k), lc) {
				if f, ok := toFloat(v); ok {
					return f, true
				}
			}
		}
	}
	return 0, false
}

// findBytes is findFloat for byte-valued rocm-smi fields.
func findBytes(card map[string]interface{}, candidates ...string) (uint64, bool) {
	for _, c := range candidates {
		if v, ok := card[c]; ok {
			if b, ok := toBytes(v); ok {
				return b, true
			}
		}
	}
	for _, c := range candidates {
		lc := strings.ToLower(c)
		for k, v := range card {
			if strings.Contains(strings.ToLower(k), lc) {
				if b, ok := toBytes(v); ok {
					return b, true
				}
			}
		}
	}
	return 0, false
}

// trailingInt parses the trailing integer of a key like "card0" → 0.
func trailingInt(s string) (int, bool) {
	i := len(s)
	for i > 0 && s[i-1] >= '0' && s[i-1] <= '9' {
		i--
	}
	if i == len(s) {
		return 0, false
	}
	n, err := strconv.Atoi(s[i:])
	if err != nil {
		return 0, false
	}
	return n, true
}
