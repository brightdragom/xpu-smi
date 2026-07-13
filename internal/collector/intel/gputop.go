package intel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strconv"
	"strings"
)

// gpuTopSample is the subset of one intel_gpu_top -J sample we consume: the
// per-engine busy percentages. Everything else in the JSON is ignored.
type gpuTopSample struct {
	Engines map[string]gpuTopEngine `json:"engines"`
}

type gpuTopEngine struct {
	Busy float64 `json:"busy"` // percent (0~100)
}

// errNoSample means intel_gpu_top produced no usable JSON sample (tool missing,
// permission denied, or output could not be parsed).
var errNoSample = errors.New("intel_gpu_top produced no usable sample")

// runIntelGPUTop runs a single short intel_gpu_top -J snapshot and returns the
// last complete sample. device, when non-empty, is passed to -d to target a
// specific card (e.g. "drm:/dev/dri/card0"); empty targets the default GPU.
//
// intel_gpu_top is a streaming tool: it keeps emitting samples until killed, so
// the call is bounded by a context deadline (exec.CommandContext sends the
// kill). We deliberately ignore the resulting "killed" error and instead judge
// success by whether at least one full sample was parsed from stdout.
func runIntelGPUTop(ctx context.Context, device string) (gpuTopSample, error) {
	cctx, cancel := context.WithTimeout(ctx, gpuTopTimeout)
	defer cancel()

	args := []string{"-J", "-s", strconv.Itoa(gpuTopSamplePeriodMS)}
	if device != "" {
		args = append(args, "-d", device)
	}

	cmd := exec.CommandContext(cctx, "intel_gpu_top", args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	// Run returns an error when the context kills the streaming tool; that is
	// expected. A missing binary or a permission failure also surfaces here,
	// but in those cases stdout is empty and parsing fails below.
	_ = cmd.Run()

	if sample, ok := parseGPUTopStream(stdout.Bytes()); ok {
		return sample, nil
	}
	return gpuTopSample{}, errNoSample
}

// parseGPUTopStream extracts the last complete sample from intel_gpu_top -J
// output. The stream is a JSON array that is only closed with ']' on a clean
// exit; because we kill the tool, the final element is usually truncated. We
// therefore decode array elements one by one and keep the last one that parses,
// tolerating a truncated tail. A bare concatenated-object stream (some versions)
// is also handled.
func parseGPUTopStream(data []byte) (gpuTopSample, bool) {
	dec := json.NewDecoder(bytes.NewReader(data))

	// Peek the first token. If it's the '[' array delimiter, decode elements.
	if tok, err := dec.Token(); err == nil {
		if d, ok := tok.(json.Delim); ok && d == '[' {
			var last gpuTopSample
			found := false
			for dec.More() {
				var s gpuTopSample
				if err := dec.Decode(&s); err != nil {
					break // truncated final element: stop, keep last good one
				}
				last, found = s, true
			}
			return last, found
		}
	}

	// Not an array (or empty/garbled): retry as a stream of bare objects.
	dec = json.NewDecoder(bytes.NewReader(data))
	var last gpuTopSample
	found := false
	for {
		var s gpuTopSample
		if err := dec.Decode(&s); err != nil {
			break
		}
		last, found = s, true
	}
	return last, found
}

// readEngineBusy returns the representative GPU utilization for this card from
// intel_gpu_top, using the Render/3D engine's busy value (the most common
// workload indicator). It first targets the card's own DRM node, then falls
// back to a device-agnostic run for single-GPU systems.
func (c *intelCard) readEngineBusy(ctx context.Context) (float64, bool) {
	device := "drm:/dev/dri/card" + strconv.Itoa(c.index)
	if sample, err := runIntelGPUTop(ctx, device); err == nil {
		if busy, ok := renderBusy(sample); ok {
			return busy, true
		}
	}
	// Fallback: no device selector. Correct when exactly one Intel GPU exists;
	// on multi-GPU systems this attributes the default GPU's load, so callers
	// should treat multi-GPU utilization as approximate (see notes).
	if sample, err := runIntelGPUTop(ctx, ""); err == nil {
		if busy, ok := renderBusy(sample); ok {
			return busy, true
		}
	}
	return 0, false
}

// renderBusy returns the busy percentage of the Render/3D engine from a sample.
// Engine keys look like "Render/3D/0"; we match on the prefix.
func renderBusy(s gpuTopSample) (float64, bool) {
	for name, e := range s.Engines {
		if strings.HasPrefix(name, "Render/3D") {
			return e.Busy, true
		}
	}
	return 0, false
}
