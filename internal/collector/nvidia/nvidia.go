// Package nvidia implements the collector.Collector contract for NVIDIA GPUs
// using NVML through the go-nvml bindings.
//
// go-nvml loads libnvidia-ml.so lazily (dlopen), so this package builds and
// links on machines without the NVIDIA driver. On such machines Init() simply
// fails and Available() returns false — the normal, non-error situation for a
// host that has no NVIDIA GPU.
package nvidia

import (
	"context"

	"github.com/NVIDIA/go-nvml/pkg/nvml"

	"xpu-smi/internal/collector"
)

// nvidiaCollector is stateless: each Collect brackets its work with an
// Init/Shutdown pair, and NVML refcounts those internally.
type nvidiaCollector struct{}

// New returns an NVIDIA adapter. It is the only exported symbol of this
// package; the leader wires it up via collector.Register(nvidia.New).
func New() collector.Collector {
	return &nvidiaCollector{}
}

// Vendor returns the lowercase vendor name.
func (c *nvidiaCollector) Vendor() string {
	return "nvidia"
}

// Available reports whether NVML can be initialized on this machine. A missing
// driver/library, missing device or missing permission all surface as a
// non-SUCCESS return here and yield false. It never panics or exits.
func (c *nvidiaCollector) Available() bool {
	if ret := nvml.Init(); ret != nvml.SUCCESS {
		return false
	}
	_ = nvml.Shutdown()
	return true
}

// Collect returns one GPUMetrics per detected NVIDIA GPU. Per-field NVML calls
// that report NOT_SUPPORTED (or otherwise fail) are skipped and left unmarked
// in Supported; a failure on one GPU skips only that GPU. Collect honors ctx
// cancellation between GPUs.
func (c *nvidiaCollector) Collect(ctx context.Context) ([]collector.GPUMetrics, error) {
	if ret := nvml.Init(); ret != nvml.SUCCESS {
		return nil, nil
	}
	defer nvml.Shutdown()

	count, ret := nvml.DeviceGetCount()
	if ret != nvml.SUCCESS {
		return nil, nil
	}

	var out []collector.GPUMetrics
	for i := 0; i < count; i++ {
		if err := ctx.Err(); err != nil {
			return out, err
		}

		device, ret := nvml.DeviceGetHandleByIndex(i)
		if ret != nvml.SUCCESS {
			// Skip this GPU; keep collecting the rest.
			continue
		}

		m := collector.NewMetrics("nvidia", i)

		// Identity fields — best-effort, not gated by Supported.
		if name, ret := device.GetName(); ret == nvml.SUCCESS {
			m.Name = name
		}
		if uuid, ret := device.GetUUID(); ret == nvml.SUCCESS {
			m.UUID = uuid
		}

		// Utilization (percent, 0~100 — used as-is).
		if util, ret := device.GetUtilizationRates(); ret == nvml.SUCCESS {
			m.UtilizationGPUPercent = float64(util.Gpu)
			m.UtilizationMemoryPercent = float64(util.Memory)
			m.MarkSupported(
				collector.FieldUtilizationGPUPercent,
				collector.FieldUtilizationMemoryPercent,
			)
		}

		// Memory (bytes — used as-is).
		if mem, ret := device.GetMemoryInfo(); ret == nvml.SUCCESS {
			m.MemoryUsedBytes = mem.Used
			m.MemoryTotalBytes = mem.Total
			m.MarkSupported(
				collector.FieldMemoryUsedBytes,
				collector.FieldMemoryTotalBytes,
			)
		}

		// Temperature (celsius — used as-is).
		if temp, ret := device.GetTemperature(nvml.TEMPERATURE_GPU); ret == nvml.SUCCESS {
			m.TemperatureCelsius = float64(temp)
			m.MarkSupported(collector.FieldTemperatureCelsius)
		}

		// Power (NVML returns milliwatts — convert to watts).
		if powerMW, ret := device.GetPowerUsage(); ret == nvml.SUCCESS {
			m.PowerWatts = float64(powerMW) / 1000.0
			m.MarkSupported(collector.FieldPowerWatts)
		}

		// Clocks (MHz — used as-is).
		if clk, ret := device.GetClockInfo(nvml.CLOCK_GRAPHICS); ret == nvml.SUCCESS {
			m.ClockGraphicsMHz = clk
			m.MarkSupported(collector.FieldClockGraphicsMHz)
		}
		if clk, ret := device.GetClockInfo(nvml.CLOCK_MEM); ret == nvml.SUCCESS {
			m.ClockMemoryMHz = clk
			m.MarkSupported(collector.FieldClockMemoryMHz)
		}

		out = append(out, m)
	}

	return out, nil
}

// Close releases adapter-held resources. This adapter keeps no long-lived NVML
// session (each Collect brackets its own Init/Shutdown), so there is nothing to
// release.
func (c *nvidiaCollector) Close() error {
	return nil
}
