// xpu-smi shows Nvidia/AMD/Intel GPU metrics in one vendor-neutral view.
//
// Default mode prints a one-shot snapshot table; --watch starts a live TUI.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"xpu-smi/internal/collector"
	"xpu-smi/internal/collector/amd"
	"xpu-smi/internal/collector/intel"
	"xpu-smi/internal/collector/nvidia"
	"xpu-smi/internal/render"
)

func main() {
	watch := flag.Bool("watch", false, "실시간 갱신 TUI 모드로 실행")
	interval := flag.Duration("interval", time.Second, "TUI 갱신 주기 (하한 500ms)")
	flag.Parse()

	if *interval < 500*time.Millisecond {
		*interval = 500 * time.Millisecond
	}

	registerAdapters()

	collectors := collector.Detect()
	if len(collectors) == 0 {
		fmt.Println("감지된 GPU 없음 (nvidia/amd/intel 드라이버를 찾지 못했습니다)")
		return
	}
	defer func() {
		for _, c := range collectors {
			_ = c.Close()
		}
	}()

	ctx := context.Background()
	if err := run(ctx, collectors, *watch, *interval); err != nil {
		fmt.Fprintln(os.Stderr, "xpu-smi:", err)
		os.Exit(1)
	}
}

// registerAdapters lists every known vendor explicitly. Detect() keeps only
// the ones whose driver actually works on this machine.
func registerAdapters() {
	collector.Register(nvidia.New)
	collector.Register(amd.New)
	collector.Register(intel.New)
}

// run dispatches to the snapshot or TUI renderer.
func run(ctx context.Context, collectors []collector.Collector, watch bool, interval time.Duration) error {
	if watch {
		return render.RunTUI(ctx, collectors, interval)
	}

	// Snapshot mode: collect once from every vendor. One failing vendor must
	// not hide the others — report it on stderr and keep going.
	var metrics []collector.GPUMetrics
	for _, c := range collectors {
		m, err := c.Collect(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "xpu-smi: %s 수집 실패: %v\n", c.Vendor(), err)
			continue
		}
		metrics = append(metrics, m...)
	}
	return render.Snapshot(os.Stdout, metrics)
}
