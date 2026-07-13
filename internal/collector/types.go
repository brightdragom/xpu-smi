// Package collector defines the vendor-neutral contract for GPU metric
// collection. Every vendor adapter (nvidia, amd, intel, ...) implements
// Collector and fills GPUMetrics using the fixed units documented below.
package collector

import (
	"context"
	"time"
)

// Keys for GPUMetrics.Supported. Adapters mark a field supported only after
// successfully filling it; renderers must show N/A for unsupported fields.
const (
	FieldUtilizationGPUPercent    = "UtilizationGPUPercent"
	FieldUtilizationMemoryPercent = "UtilizationMemoryPercent"
	FieldMemoryUsedBytes          = "MemoryUsedBytes"
	FieldMemoryTotalBytes         = "MemoryTotalBytes"
	FieldTemperatureCelsius       = "TemperatureCelsius"
	FieldPowerWatts               = "PowerWatts"
	FieldClockGraphicsMHz         = "ClockGraphicsMHz"
	FieldClockMemoryMHz           = "ClockMemoryMHz"
)

// GPUMetrics is a vendor-neutral snapshot of a single GPU.
//
// Fixed units: memory in bytes, temperature in celsius, power in watts,
// clocks in MHz, utilization in percent (0~100). Adapters convert from
// whatever their SDK returns (e.g. NVML milliwatts, sysfs milli-celsius).
//
// A metric field is meaningful only when Supported reports true for its key.
// Zero and "not supported" are different things: an idle GPU really is at
// 0 % utilization, while an integrated GPU has no VRAM figure at all.
type GPUMetrics struct {
	Vendor string // "nvidia" | "amd" | "intel"
	Index  int
	Name   string
	UUID   string

	UtilizationGPUPercent    float64
	UtilizationMemoryPercent float64

	MemoryUsedBytes  uint64
	MemoryTotalBytes uint64

	TemperatureCelsius float64
	PowerWatts         float64

	ClockGraphicsMHz uint32
	ClockMemoryMHz   uint32

	Timestamp time.Time

	// Supported maps Field* keys to availability. A missing key means
	// unsupported — the zero value fails safe so a forgotten field renders
	// as N/A instead of a bogus 0.
	Supported map[string]bool
}

// NewMetrics returns a GPUMetrics with identity fields set and an empty
// Supported map. Adapters call MarkSupported for each field they fill.
func NewMetrics(vendor string, index int) GPUMetrics {
	return GPUMetrics{
		Vendor:    vendor,
		Index:     index,
		Timestamp: time.Now(),
		Supported: make(map[string]bool),
	}
}

// MarkSupported records that the given fields hold real values.
func (m *GPUMetrics) MarkSupported(fields ...string) {
	if m.Supported == nil {
		m.Supported = make(map[string]bool)
	}
	for _, f := range fields {
		m.Supported[f] = true
	}
}

// IsSupported reports whether a field holds a real value.
func (m *GPUMetrics) IsSupported(field string) bool {
	return m.Supported[field]
}

// Collector is the contract every vendor adapter implements.
type Collector interface {
	// Vendor returns the lowercase vendor name: "nvidia", "amd" or "intel".
	Vendor() string

	// Available reports whether this machine can actually use the vendor's
	// driver/SDK. Missing drivers, missing devices and missing permissions
	// are all normal situations, not errors: the adapter silently disables
	// itself by returning false. It must never panic or exit the process.
	Available() bool

	// Collect returns one GPUMetrics per detected GPU. A failure on one GPU
	// must not abort the others; skip it and keep collecting. Collect must
	// honor ctx cancellation (use exec.CommandContext for subprocesses).
	Collect(ctx context.Context) ([]GPUMetrics, error)

	// Close releases handles or subprocesses held by the adapter.
	Close() error
}
