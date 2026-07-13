package collector

// The registry keeps adapter construction separate from adapter selection.
// main.go registers every known vendor explicitly; Detect keeps only the
// adapters that actually work on this machine. A vendor whose driver is
// missing is skipped silently — that is the normal case on most machines,
// never a reason to abort the process.

var registered []func() Collector

// Register adds an adapter factory. Call once per vendor from main.
func Register(factory func() Collector) {
	registered = append(registered, factory)
}

// Detect instantiates every registered adapter and returns the ones that
// report Available() == true. Unavailable adapters are closed and dropped.
func Detect() []Collector {
	var active []Collector
	for _, factory := range registered {
		c := factory()
		if c.Available() {
			active = append(active, c)
		} else {
			_ = c.Close()
		}
	}
	return active
}
