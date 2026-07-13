package amd

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"xpu-smi/internal/collector"
)

// drmRoot is the sysfs location of DRM devices. Overridable in tests, but kept
// unexported so the package API stays New()-only.
const drmRoot = "/sys/class/drm"

// amdPCIVendor is AMD's PCI vendor id, used to keep only amdgpu cards and skip
// Intel/Nvidia DRM nodes that happen to share the card* namespace.
const amdPCIVendor = "0x1002"

// cardNameRe matches a real DRM card node ("card0", "card1"), excluding
// connector nodes like "card0-DP-1".
var cardNameRe = regexp.MustCompile(`^card[0-9]+$`)

// sclkLineRe extracts the MHz value from a pp_dpm_*clk line whose active level
// is marked with a trailing '*', e.g. "1: 1500Mhz *".
var sclkLineRe = regexp.MustCompile(`([0-9]+)\s*[Mm][Hh]z\s*\*`)

// collectSysfs reads amdgpu metrics directly from /sys/class/drm/card*/device.
// It requires no external tools — only the amdgpu kernel driver. This path
// cannot produce UtilizationMemoryPercent (VRAM capacity is not bandwidth
// utilization), so that field is deliberately left unsupported.
func (a *adapter) collectSysfs(ctx context.Context) ([]collector.GPUMetrics, error) {
	entries, err := os.ReadDir(drmRoot)
	if err != nil {
		return nil, err
	}

	// Collect and sort card directory names for stable ordering.
	var cards []string
	for _, e := range entries {
		if cardNameRe.MatchString(e.Name()) {
			cards = append(cards, e.Name())
		}
	}
	sort.Strings(cards)

	var out []collector.GPUMetrics
	for _, card := range cards {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		devDir := filepath.Join(drmRoot, card, "device")

		// Keep only AMD cards. Missing/mismatched vendor id → skip silently.
		if vendor := strings.TrimSpace(readFile(filepath.Join(devDir, "vendor"))); vendor != amdPCIVendor {
			continue
		}

		idx := cardIndex(card)
		m := collector.NewMetrics(vendorName, idx)
		if uuid := strings.TrimSpace(readFile(filepath.Join(devDir, "unique_id"))); uuid != "" {
			m.UUID = uuid
		}

		fillSysfsCard(&m, devDir)
		out = append(out, m)
	}

	return out, nil
}

// fillSysfsCard populates a single card's metrics from its device directory,
// marking each field supported only after a successful read+parse.
func fillSysfsCard(m *collector.GPUMetrics, devDir string) {
	// GPU utilization: gpu_busy_percent is 0..100 already.
	if v, ok := readUint(filepath.Join(devDir, "gpu_busy_percent")); ok {
		m.UtilizationGPUPercent = float64(v)
		m.MarkSupported(collector.FieldUtilizationGPUPercent)
	}

	// VRAM used / total: bytes, no conversion.
	if v, ok := readUint(filepath.Join(devDir, "mem_info_vram_used")); ok {
		m.MemoryUsedBytes = v
		m.MarkSupported(collector.FieldMemoryUsedBytes)
	}
	if v, ok := readUint(filepath.Join(devDir, "mem_info_vram_total")); ok {
		m.MemoryTotalBytes = v
		m.MarkSupported(collector.FieldMemoryTotalBytes)
	}

	// Temperature and power live under hwmon/hwmon*/.
	if hw := firstHwmonDir(devDir); hw != "" {
		// temp1_input is milli-celsius → /1000 for celsius.
		if v, ok := readInt(filepath.Join(hw, "temp1_input")); ok {
			m.TemperatureCelsius = float64(v) / 1000.0
			m.MarkSupported(collector.FieldTemperatureCelsius)
		}
		// power1_average is microwatt → /1e6 for watts.
		if v, ok := readInt(filepath.Join(hw, "power1_average")); ok {
			m.PowerWatts = float64(v) / 1_000_000.0
			m.MarkSupported(collector.FieldPowerWatts)
		}
	}

	// Clocks: parse the active ('*') level from pp_dpm_sclk / pp_dpm_mclk.
	if mhz, ok := activeClockMHz(filepath.Join(devDir, "pp_dpm_sclk")); ok {
		m.ClockGraphicsMHz = mhz
		m.MarkSupported(collector.FieldClockGraphicsMHz)
	}
	if mhz, ok := activeClockMHz(filepath.Join(devDir, "pp_dpm_mclk")); ok {
		m.ClockMemoryMHz = mhz
		m.MarkSupported(collector.FieldClockMemoryMHz)
	}

	// UtilizationMemoryPercent is intentionally left unsupported: sysfs exposes
	// VRAM capacity, not memory-bandwidth utilization.
}

// firstHwmonDir returns the first hwmon* subdirectory under devDir/hwmon, or ""
// if none. The numeric suffix varies per card, so it is globbed.
func firstHwmonDir(devDir string) string {
	matches, err := filepath.Glob(filepath.Join(devDir, "hwmon", "hwmon*"))
	if err != nil || len(matches) == 0 {
		return ""
	}
	sort.Strings(matches)
	return matches[0]
}

// activeClockMHz parses a pp_dpm_*clk file and returns the MHz of the level
// marked active with '*'.
func activeClockMHz(path string) (uint32, bool) {
	data := readFile(path)
	if data == "" {
		return 0, false
	}
	for _, line := range strings.Split(data, "\n") {
		if m := sclkLineRe.FindStringSubmatch(line); m != nil {
			if v, err := strconv.ParseUint(m[1], 10, 32); err == nil {
				return uint32(v), true
			}
		}
	}
	return 0, false
}

// cardIndex extracts the trailing integer from a "cardN" directory name.
func cardIndex(card string) int {
	n, err := strconv.Atoi(strings.TrimPrefix(card, "card"))
	if err != nil {
		return 0
	}
	return n
}

// readFile returns the trimmed file contents, or "" on any error.
func readFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// readUint parses an unsigned integer sysfs value.
func readUint(path string) (uint64, bool) {
	s := readFile(path)
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// readInt parses a signed integer sysfs value (temp/power can be signed).
func readInt(path string) (int64, bool) {
	s := readFile(path)
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}
