// Package amd implements the collector.Collector contract for AMD GPUs.
//
// AMD lacks a mature Go binding, so this adapter probes a runtime fallback
// chain and uses the first path that yields data:
//
//  1. amd-smi metric --json   (ROCm 6+, preferred)
//  2. rocm-smi ... --json      (older, widely deployed)
//  3. sysfs /sys/class/drm/card*/device/  (amdgpu kernel driver only)
//
// All external processes run under exec.CommandContext with a short timeout
// so a hung tool never blocks a snapshot. On a machine without any AMD GPU
// (no tools, no amdgpu sysfs nodes) every path fails and Available() returns
// false without panicking — that is the normal, expected outcome here.
package amd

import (
	"context"
	"errors"
	"sync"
	"time"

	"xpu-smi/internal/collector"
)

// cmdTimeout bounds each external tool invocation. amd-smi / rocm-smi can take
// several hundred milliseconds; 2s leaves headroom without blocking the TUI.
const cmdTimeout = 2 * time.Second

// vendorName is the lowercase vendor tag required by the contract.
const vendorName = "amd"

var errNoSource = errors.New("amd: no working collection path (amd-smi/rocm-smi/sysfs all failed)")

// source is one collection strategy. It returns one GPUMetrics per detected
// GPU, or an error/empty slice if the strategy is unavailable on this machine.
type source func(ctx context.Context) ([]collector.GPUMetrics, error)

// adapter is the concrete Collector. New returns it as an interface value; the
// registry keeps the same instance across Available() and Collect(), so the
// detected path is memoized in the struct.
type adapter struct {
	mu     sync.Mutex
	active source // memoized winning path, set by detect()
}

// New constructs the AMD adapter. It is the only exported symbol in this
// package, matching the vendor API contract: func New() collector.Collector.
func New() collector.Collector {
	return &adapter{}
}

// Vendor returns the lowercase vendor name.
func (a *adapter) Vendor() string { return vendorName }

// sources lists the collection strategies in priority order.
func (a *adapter) sources() []source {
	return []source{a.collectAMDSMI, a.collectROCmSMI, a.collectSysfs}
}

// detect runs each strategy in priority order and returns the first that
// yields at least one GPU. Each CLI strategy self-bounds its own timeout, so
// passing a background context here is safe.
func (a *adapter) detect(ctx context.Context) source {
	for _, s := range a.sources() {
		metrics, err := s(ctx)
		if err == nil && len(metrics) > 0 {
			return s
		}
	}
	return nil
}

// Available probes the fallback chain once and memoizes the winning path. It
// never panics or exits: a machine with no AMD driver simply gets false.
func (a *adapter) Available() bool {
	s := a.detect(context.Background())
	a.mu.Lock()
	a.active = s
	a.mu.Unlock()
	return s != nil
}

// Collect gathers metrics via the memoized path, re-detecting if that path has
// since disappeared (or was never established). A per-GPU parse failure does
// not abort the others — each source skips the bad GPU and returns the rest.
func (a *adapter) Collect(ctx context.Context) ([]collector.GPUMetrics, error) {
	a.mu.Lock()
	s := a.active
	a.mu.Unlock()

	if s != nil {
		if metrics, err := s(ctx); err == nil && len(metrics) > 0 {
			return metrics, nil
		}
	}

	// Memoized path failed or was unset; re-probe the whole chain.
	s = a.detect(ctx)
	a.mu.Lock()
	a.active = s
	a.mu.Unlock()
	if s == nil {
		return nil, errNoSource
	}
	return s(ctx)
}

// Close releases resources. This adapter holds no long-lived handles or
// subprocesses (each collection spawns and reaps its own), so there is
// nothing to clean up.
func (a *adapter) Close() error { return nil }
