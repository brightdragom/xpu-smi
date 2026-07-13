// Package render turns vendor-neutral GPUMetrics into the two user-facing
// views of xpu-smi: a one-shot nvidia-smi style table (Snapshot) and a live
// htop-style dashboard (RunTUI).
//
// Nothing in this package branches on m.Vendor for behavior. The Vendor field
// is a display label only; every value is rendered from GPUMetrics and its
// Supported map, so adding a new vendor adapter never requires touching the
// renderer.
package render

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"xpu-smi/internal/collector"
)

// naValue is the single string shown for any field whose Supported flag is
// false. Keeping it in one place makes the "0 vs. unmeasured" distinction
// consistent across the snapshot table and the TUI.
const naValue = "N/A"

// sortMetrics orders rows by vendor name (alphabetical) then by index, so a
// given GPU always lands on the same row across runs. Dynamic values such as
// utilization are never used as sort keys — stable positions keep the output
// grep/awk friendly for scripting. The input slice is copied, not mutated.
func sortMetrics(metrics []collector.GPUMetrics) []collector.GPUMetrics {
	out := make([]collector.GPUMetrics, len(metrics))
	copy(out, metrics)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Vendor != out[j].Vendor {
			return out[i].Vendor < out[j].Vendor
		}
		return out[i].Index < out[j].Index
	})
	return out
}

// The following format* helpers each take a GPUMetrics and return the display
// string for one column, honoring the Supported map. They are shared by both
// the snapshot table and the TUI so the two views can never drift.

func formatUtil(m collector.GPUMetrics) string {
	if !m.IsSupported(collector.FieldUtilizationGPUPercent) {
		return naValue
	}
	return fmt.Sprintf("%.0f%%", m.UtilizationGPUPercent)
}

// formatMem renders "used/total GB". Both byte fields must be supported;
// integrated GPUs that expose no VRAM figure render as N/A rather than 0/0.
func formatMem(m collector.GPUMetrics) string {
	if !m.IsSupported(collector.FieldMemoryUsedBytes) || !m.IsSupported(collector.FieldMemoryTotalBytes) {
		return naValue
	}
	const gib = 1024 * 1024 * 1024
	used := float64(m.MemoryUsedBytes) / gib
	total := float64(m.MemoryTotalBytes) / gib
	return fmt.Sprintf("%.1f/%.1f GB", used, total)
}

func formatTemp(m collector.GPUMetrics) string {
	if !m.IsSupported(collector.FieldTemperatureCelsius) {
		return naValue
	}
	return fmt.Sprintf("%.0fC", m.TemperatureCelsius)
}

func formatPower(m collector.GPUMetrics) string {
	if !m.IsSupported(collector.FieldPowerWatts) {
		return naValue
	}
	return fmt.Sprintf("%.0fW", m.PowerWatts)
}

// formatClock renders "graphics/memory MHz". If only the graphics clock is
// supported (e.g. an Intel integrated GPU with no separate memory clock) it
// shows just "graphics MHz". If graphics is unsupported the whole column is
// N/A regardless of the memory clock.
func formatClock(m collector.GPUMetrics) string {
	hasG := m.IsSupported(collector.FieldClockGraphicsMHz)
	hasM := m.IsSupported(collector.FieldClockMemoryMHz)
	switch {
	case hasG && hasM:
		return fmt.Sprintf("%d/%d MHz", m.ClockGraphicsMHz, m.ClockMemoryMHz)
	case hasG:
		return fmt.Sprintf("%d MHz", m.ClockGraphicsMHz)
	default:
		return naValue
	}
}

// formatName guards against an empty Name so the column never collapses.
func formatName(m collector.GPUMetrics) string {
	if strings.TrimSpace(m.Name) == "" {
		return naValue
	}
	return m.Name
}

// snapshotHeaders are the column titles shared by the snapshot table. The TUI
// reuses the same labels so both views read identically.
var snapshotHeaders = []string{"VENDOR", "IDX", "NAME", "UTIL%", "MEM", "TEMP", "POWER", "CLOCK(G/M)"}

// snapshotRow builds the ordered cell values for one metric.
func snapshotRow(m collector.GPUMetrics) []string {
	return []string{
		m.Vendor,
		fmt.Sprintf("%d", m.Index),
		formatName(m),
		formatUtil(m),
		formatMem(m),
		formatTemp(m),
		formatPower(m),
		formatClock(m),
	}
}

// Snapshot writes the already-collected metrics as a single aligned table and
// returns. It performs no collection of its own — the caller gathers metrics
// (across all vendors) and hands them in. An empty slice yields a short
// "no GPUs detected" line and a nil error, matching the "don't error when the
// machine has no GPU" rule.
func Snapshot(w io.Writer, metrics []collector.GPUMetrics) error {
	if len(metrics) == 0 {
		_, err := fmt.Fprintln(w, "No GPUs detected.")
		return err
	}

	tw := tabwriter.NewWriter(w, 0, 4, 3, ' ', 0)
	if _, err := fmt.Fprintln(tw, strings.Join(snapshotHeaders, "\t")); err != nil {
		return err
	}
	for _, m := range sortMetrics(metrics) {
		if _, err := fmt.Fprintln(tw, strings.Join(snapshotRow(m), "\t")); err != nil {
			return err
		}
	}
	return tw.Flush()
}
