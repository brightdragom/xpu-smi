// Package intel implements the Collector contract for Intel GPUs using a
// practical MVP data path: a short intel_gpu_top -J snapshot for engine
// utilization plus sysfs (/sys/class/drm/card*/) for clock, power, temperature
// and — on discrete Arc cards only — VRAM.
//
// The defining subtlety of this adapter: integrated Intel GPUs share system
// RAM and have no dedicated VRAM, so MemoryUsedBytes/MemoryTotalBytes are left
// unsupported rather than filled with a bogus system-RAM figure. Only discrete
// cards that expose mem_info_vram_* get memory marked supported.
//
// The oneAPI Level Zero (zes/Sysman) path is intentionally NOT implemented here
// — it requires a cgo binding to libze_loader.so. See _workspace/02_intel_notes.md.
package intel

import (
	"context"
	"time"

	"xpu-smi/internal/collector"
)

// intelVendorID is the PCI vendor id for Intel, used to distinguish Intel DRM
// cards from other vendors' cards (AMD 0x1002, NVIDIA 0x10de, QEMU 0x1b36, ...).
const intelVendorID = "0x8086"

// gpuTopTimeout bounds every intel_gpu_top subprocess. It must comfortably
// exceed the sampling period so at least one complete JSON sample is emitted.
const (
	gpuTopSamplePeriodMS = 200
	gpuTopTimeout        = 3 * time.Second
)

// Collector is the Intel adapter. It holds no persistent handles or
// subprocesses; every Collect runs a fresh, context-bounded snapshot.
type Collector struct{}

// New returns an Intel Collector. This is the only exported symbol of the
// package; main.go wires it via collector.Register(intel.New).
func New() collector.Collector {
	return &Collector{}
}

// Vendor returns the lowercase vendor name.
func (c *Collector) Vendor() string {
	return "intel"
}

// Available reports whether this machine can actually produce Intel GPU
// metrics. It never panics or exits.
//
// Detection order:
//  1. No Intel DRM card (vendor id 0x8086) present -> false (hardware absent).
//  2. Intel card present and at least one sysfs metric readable -> true.
//  3. Intel card present, sysfs yields nothing, but intel_gpu_top runs -> true.
//  4. Intel card present but nothing is readable (typically a permission
//     problem: intel_gpu_top needs root/CAP_PERFMON and sysfs may be blocked)
//     -> false.
//
// Cases 1 and 4 both return false but are different situations; the distinction
// is documented in _workspace/02_intel_notes.md.
func (c *Collector) Available() bool {
	cards := discoverIntelCards()
	if len(cards) == 0 {
		// Hardware absent (or non-Intel cards only). Normal on most machines.
		return false
	}

	// Prefer the cheap, permission-friendly sysfs probe: if any card exposes at
	// least one readable metric, the adapter is usable.
	for i := range cards {
		if cards[i].hasReadableMetric() {
			return true
		}
	}

	// sysfs gave nothing (unusual). Fall back to confirming intel_gpu_top works.
	ctx, cancel := context.WithTimeout(context.Background(), gpuTopTimeout)
	defer cancel()
	if _, err := runIntelGPUTop(ctx, ""); err == nil {
		return true
	}

	// Intel hardware is present but no data source is reachable — most likely a
	// permission problem. The adapter cannot be used, so report false.
	return false
}

// Collect returns one GPUMetrics per detected Intel GPU. A parse failure on one
// card is skipped so the remaining cards still report. Utilization comes from a
// single context-bounded intel_gpu_top snapshot; sysfs supplies the rest.
func (c *Collector) Collect(ctx context.Context) ([]collector.GPUMetrics, error) {
	cards := discoverIntelCards()
	if len(cards) == 0 {
		return nil, nil
	}

	out := make([]collector.GPUMetrics, 0, len(cards))
	for i := range cards {
		if ctx.Err() != nil {
			// Context cancelled/expired: return whatever we have so far.
			return out, ctx.Err()
		}
		out = append(out, cards[i].collect(ctx))
	}
	return out, nil
}

// Close releases resources. The adapter holds none (no long-lived handles or
// subprocesses), so this is a no-op.
func (c *Collector) Close() error {
	return nil
}
