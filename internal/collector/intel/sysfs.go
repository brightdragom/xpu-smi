package intel

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

// drmRoot is the sysfs DRM directory. Declared as a var so tests could point it
// elsewhere; production code always uses the real path.
var drmRoot = "/sys/class/drm"

// cardNameRe matches primary DRM card directories (card0, card1, ...) while
// excluding connector entries like "card0-Virtual-1" or "card0-DP-1".
var cardNameRe = regexp.MustCompile(`^card([0-9]+)$`)

// intelCard is a single detected Intel DRM card and the sysfs paths under it.
type intelCard struct {
	index      int    // the N in cardN, used as GPUMetrics.Index
	cardPath   string // /sys/class/drm/cardN
	devicePath string // /sys/class/drm/cardN/device
	driver     string // "i915", "xe" or "" if undetermined
}

// discoverIntelCards scans /sys/class/drm for primary cards whose PCI vendor id
// is Intel's (0x8086). Non-Intel and unreadable cards are skipped. The result
// is sorted by card index for stable ordering.
func discoverIntelCards() []intelCard {
	entries, err := os.ReadDir(drmRoot)
	if err != nil {
		return nil
	}

	var cards []intelCard
	for _, e := range entries {
		m := cardNameRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		cardPath := filepath.Join(drmRoot, e.Name())
		devicePath := filepath.Join(cardPath, "device")

		vendor, ok := readTrimmedFile(filepath.Join(devicePath, "vendor"))
		if !ok || !strings.EqualFold(vendor, intelVendorID) {
			continue // absent vendor file or a different vendor's card
		}

		idx, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		cards = append(cards, intelCard{
			index:      idx,
			cardPath:   cardPath,
			devicePath: devicePath,
			driver:     detectDriver(devicePath),
		})
	}

	sort.Slice(cards, func(i, j int) bool { return cards[i].index < cards[j].index })
	return cards
}

// detectDriver resolves the bound kernel driver ("i915" or "xe") from the
// device/driver symlink. Returns "" when it cannot be determined.
func detectDriver(devicePath string) string {
	if target, err := os.Readlink(filepath.Join(devicePath, "driver")); err == nil {
		return filepath.Base(target)
	}
	return ""
}

// hasReadableMetric reports whether at least one sysfs metric can be read for
// this card. Used by Available() as a cheap, permission-friendly probe.
func (c *intelCard) hasReadableMetric() bool {
	if _, ok := c.readClockMHz(); ok {
		return true
	}
	if _, ok := c.readPowerWatts(); ok {
		return true
	}
	if _, ok := c.readTemperatureCelsius(); ok {
		return true
	}
	if _, _, ok := c.readVRAMBytes(); ok {
		return true
	}
	return false
}

// collect builds the GPUMetrics for this card, marking only the fields it
// successfully fills. Utilization is sourced from intel_gpu_top; everything
// else from sysfs.
func (c *intelCard) collect(ctx context.Context) collector.GPUMetrics {
	m := collector.NewMetrics("intel", c.index)
	m.Name = c.displayName()

	// --- Utilization via intel_gpu_top (needs root/CAP_PERFMON) ---
	if busy, ok := c.readEngineBusy(ctx); ok {
		m.UtilizationGPUPercent = busy
		m.MarkSupported(collector.FieldUtilizationGPUPercent)
	}

	// --- Clock via sysfs ---
	if mhz, ok := c.readClockMHz(); ok {
		m.ClockGraphicsMHz = mhz
		m.MarkSupported(collector.FieldClockGraphicsMHz)
	}

	// --- Power via hwmon (microwatt -> watt) ---
	if w, ok := c.readPowerWatts(); ok {
		m.PowerWatts = w
		m.MarkSupported(collector.FieldPowerWatts)
	}

	// --- Temperature via hwmon (milli-celsius -> celsius) ---
	if t, ok := c.readTemperatureCelsius(); ok {
		m.TemperatureCelsius = t
		m.MarkSupported(collector.FieldTemperatureCelsius)
	}

	// --- VRAM: DISCRETE (Arc) cards only ---
	// Integrated GPUs share system RAM and expose no mem_info_vram_*; we
	// deliberately leave memory unsupported rather than reporting system RAM.
	if used, total, ok := c.readVRAMBytes(); ok {
		m.MemoryUsedBytes = used
		m.MemoryTotalBytes = total
		m.MarkSupported(collector.FieldMemoryUsedBytes, collector.FieldMemoryTotalBytes)
		if total > 0 {
			m.UtilizationMemoryPercent = float64(used) / float64(total) * 100
			m.MarkSupported(collector.FieldUtilizationMemoryPercent)
		}
	}

	// ClockMemoryMHz: no reliable sysfs source across i915/xe; left unsupported.

	return m
}

// displayName returns a human-friendly identity string. It is best-effort and
// not gated by a Supported flag (Name is identity metadata, not a metric).
func (c *intelCard) displayName() string {
	name := "Intel GPU"
	if c.driver != "" {
		name += " [" + c.driver + "]"
	}
	// Presence of VRAM sysfs distinguishes discrete Arc from integrated.
	if _, _, ok := c.readVRAMBytes(); ok {
		name += " (discrete)"
	} else {
		name += " (integrated)"
	}
	return name
}

// readClockMHz reads the current GPU clock in MHz. It tries the i915 layout
// first, then the xe layout (globbed across tile/gt directories). Value is used
// as-is (already MHz).
func (c *intelCard) readClockMHz() (uint32, bool) {
	// i915: gt_cur_freq_mhz lives under the card (and sometimes under device).
	i915Paths := []string{
		filepath.Join(c.cardPath, "gt_cur_freq_mhz"),
		filepath.Join(c.devicePath, "gt_cur_freq_mhz"),
	}
	for _, p := range i915Paths {
		if v, ok := readUintFile(p); ok {
			return uint32(v), true
		}
	}

	// xe: per-gt frequency, e.g. device/tile0/gt0/freq0/act_freq (or cur_freq).
	xeGlobs := []string{
		filepath.Join(c.devicePath, "tile*", "gt*", "freq*", "act_freq"),
		filepath.Join(c.devicePath, "tile*", "gt*", "freq*", "cur_freq"),
		filepath.Join(c.devicePath, "gt*", "freq*", "act_freq"),
	}
	for _, g := range xeGlobs {
		if v, ok := readUintGlob(g); ok {
			return uint32(v), true
		}
	}
	return 0, false
}

// readPowerWatts reads instantaneous package power from hwmon. sysfs reports
// microwatts, converted here to watts.
func (c *intelCard) readPowerWatts() (float64, bool) {
	if v, ok := readUintGlob(filepath.Join(c.devicePath, "hwmon", "hwmon*", "power1_average")); ok {
		return float64(v) / 1_000_000.0, true
	}
	if v, ok := readUintGlob(filepath.Join(c.devicePath, "hwmon", "hwmon*", "power1_input")); ok {
		return float64(v) / 1_000_000.0, true
	}
	return 0, false
}

// readTemperatureCelsius reads GPU temperature from hwmon. sysfs reports
// milli-celsius, converted here to celsius.
func (c *intelCard) readTemperatureCelsius() (float64, bool) {
	if v, ok := readUintGlob(filepath.Join(c.devicePath, "hwmon", "hwmon*", "temp1_input")); ok {
		return float64(v) / 1000.0, true
	}
	return 0, false
}

// readVRAMBytes reads discrete-GPU VRAM usage. Returns ok=false for integrated
// GPUs, which have no mem_info_vram_* files. Values are already in bytes.
func (c *intelCard) readVRAMBytes() (used, total uint64, ok bool) {
	usedPaths := []string{
		filepath.Join(c.devicePath, "mem_info_vram_used"),
		filepath.Join(c.cardPath, "mem_info_vram_used"),
	}
	totalPaths := []string{
		filepath.Join(c.devicePath, "mem_info_vram_total"),
		filepath.Join(c.cardPath, "mem_info_vram_total"),
	}

	u, uok := readFirstUint(usedPaths)
	t, tok := readFirstUint(totalPaths)
	if uok && tok {
		return u, t, true
	}
	return 0, 0, false
}

// --- small file helpers -------------------------------------------------------

// readTrimmedFile reads a file and returns its whitespace-trimmed contents.
func readTrimmedFile(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(b)), true
}

// readUintFile parses a sysfs file holding a single unsigned integer.
func readUintFile(path string) (uint64, bool) {
	s, ok := readTrimmedFile(path)
	if !ok {
		return 0, false
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// readFirstUint returns the first readable/parseable uint among the paths.
func readFirstUint(paths []string) (uint64, bool) {
	for _, p := range paths {
		if v, ok := readUintFile(p); ok {
			return v, true
		}
	}
	return 0, false
}

// readUintGlob expands a glob and returns the first readable/parseable uint.
func readUintGlob(pattern string) (uint64, bool) {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return 0, false
	}
	sort.Strings(matches) // deterministic pick when several match
	for _, p := range matches {
		if v, ok := readUintFile(p); ok {
			return v, true
		}
	}
	return 0, false
}
