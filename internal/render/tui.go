package render

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"xpu-smi/internal/collector"
)

// Threshold constants for warning colors. Color is always paired with the
// literal text value (never color alone) so colorblind users lose no
// information — see the SKILL rule on dual color+text encoding.
const (
	utilWarnPercent = 80.0 // UTIL% at or above this is highlighted
	tempWarnCelsius = 85.0 // TEMP at or above this is highlighted
)

// Palette. Styles are vendor-neutral; they key off values, never off Vendor.
var (
	styleHeader  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))
	styleTitle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	styleHint    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	styleWarn    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214")) // amber
	styleCrit    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("196")) // red
	styleStale   = lipgloss.NewStyle().Faint(true).Foreground(lipgloss.Color("240"))
	stylePlain   = lipgloss.NewStyle()
	styleWarnMsg = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
)

// vendorState tracks one collector across ticks so a single vendor's failure
// only ever affects that vendor's rows. On a failed Collect we retain the last
// successful metrics and flag them stale, per the SKILL resilience rule.
type vendorState struct {
	col     collector.Collector
	label   string                 // display label (vendor name)
	metrics []collector.GPUMetrics // last successful collection
	stale   bool                   // last tick's Collect failed
	lastErr error                  // error from the most recent failed tick
	seen    bool                   // whether any collection ever succeeded
}

// tickMsg fires every interval to trigger a fresh collection.
type tickMsg time.Time

// collectResultMsg carries per-vendor collection outcomes back to the UI.
type collectResultMsg struct {
	results []vendorResult
}

type vendorResult struct {
	idx     int // index into model.states
	metrics []collector.GPUMetrics
	err     error
}

type model struct {
	ctx      context.Context
	interval time.Duration
	states   []vendorState
	width    int
	height   int
}

// newModel builds the initial model from the collector list. Collectors that
// report Available()==false are still tracked (they simply never yield rows),
// keeping the renderer free of vendor-specific presence logic.
func newModel(ctx context.Context, collectors []collector.Collector, interval time.Duration) model {
	states := make([]vendorState, 0, len(collectors))
	for _, c := range collectors {
		states = append(states, vendorState{col: c, label: c.Vendor()})
	}
	return model{ctx: ctx, interval: interval, states: states}
}

func (m model) Init() tea.Cmd {
	// Collect once immediately so the first frame has data, then start ticking.
	return tea.Batch(m.collectCmd(), m.tickCmd())
}

// tickCmd schedules the next refresh.
func (m model) tickCmd() tea.Cmd {
	return tea.Tick(m.interval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// collectCmd re-collects every vendor in one background command. Each
// collector is isolated: a panic or error from one is captured and reported
// as that vendor's result without disturbing the others.
func (m model) collectCmd() tea.Cmd {
	states := m.states
	ctx := m.ctx
	return func() tea.Msg {
		results := make([]vendorResult, len(states))
		for i := range states {
			results[i] = collectOne(ctx, i, states[i].col)
		}
		return collectResultMsg{results: results}
	}
}

// collectOne runs a single collector defensively, converting a panic into an
// error so one misbehaving adapter can never crash the dashboard.
func collectOne(ctx context.Context, idx int, c collector.Collector) (res vendorResult) {
	res.idx = idx
	defer func() {
		if r := recover(); r != nil {
			res.metrics = nil
			res.err = fmt.Errorf("collector panicked: %v", r)
		}
	}()
	if !c.Available() {
		res.err = fmt.Errorf("not available")
		return res
	}
	metrics, err := c.Collect(ctx)
	if err != nil {
		res.err = err
		return res
	}
	res.metrics = metrics
	return res
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tickMsg:
		return m, tea.Batch(m.collectCmd(), m.tickCmd())
	case collectResultMsg:
		for _, r := range msg.results {
			if r.idx < 0 || r.idx >= len(m.states) {
				continue
			}
			st := &m.states[r.idx]
			if r.err != nil {
				// Keep last-good metrics but flag stale; do not clear rows.
				st.stale = true
				st.lastErr = r.err
			} else {
				st.metrics = r.metrics
				st.stale = false
				st.lastErr = nil
				st.seen = true
			}
		}
	}
	return m, nil
}

// currentMetrics flattens all vendors' last-known metrics into one sorted
// slice, tagging each row with its vendor's stale flag for styling.
func (m model) currentMetrics() []displayRow {
	var rows []displayRow
	for i := range m.states {
		st := &m.states[i]
		for _, mm := range st.metrics {
			rows = append(rows, displayRow{metric: mm, stale: st.stale})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i].metric, rows[j].metric
		if a.Vendor != b.Vendor {
			return a.Vendor < b.Vendor
		}
		return a.Index < b.Index
	})
	return rows
}

type displayRow struct {
	metric collector.GPUMetrics
	stale  bool
}

func (m model) View() string {
	rows := m.currentMetrics()

	var b strings.Builder
	b.WriteString(m.renderSummary(rows))
	b.WriteString("\n\n")
	if len(rows) == 0 {
		b.WriteString("No GPUs detected.\n")
	} else {
		b.WriteString(m.renderTable(rows))
		b.WriteString("\n")
	}
	if warn := m.renderStaleWarnings(); warn != "" {
		b.WriteString("\n")
		b.WriteString(warn)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(styleHint.Render("q/esc/ctrl+c: quit   |   refresh: " + m.interval.String()))
	return b.String()
}

// renderSummary shows the detected GPU count and a per-vendor breakdown, e.g.
// "3 GPU(s) detected (amd x1, intel x1, nvidia x1)".
func (m model) renderSummary(rows []displayRow) string {
	counts := map[string]int{}
	var order []string
	for _, r := range rows {
		if _, ok := counts[r.metric.Vendor]; !ok {
			order = append(order, r.metric.Vendor)
		}
		counts[r.metric.Vendor]++
	}
	sort.Strings(order)
	parts := make([]string, 0, len(order))
	for _, v := range order {
		parts = append(parts, fmt.Sprintf("%s x%d", v, counts[v]))
	}
	title := styleTitle.Render("xpu-smi")
	if len(rows) == 0 {
		return title + "  —  0 GPU(s) detected"
	}
	return fmt.Sprintf("%s  —  %d GPU(s) detected (%s)", title, len(rows), strings.Join(parts, ", "))
}

// renderStaleWarnings lists vendors whose latest tick failed, with the reason.
// This is the text half of the stale indication (the rows are also dimmed).
func (m model) renderStaleWarnings() string {
	var lines []string
	for i := range m.states {
		st := &m.states[i]
		if !st.stale {
			continue
		}
		reason := "collect failed"
		if st.lastErr != nil {
			reason = st.lastErr.Error()
		}
		state := "stale"
		if !st.seen {
			state = "unavailable"
		}
		lines = append(lines, styleWarnMsg.Render(
			fmt.Sprintf("! %s %s: %s", st.label, state, reason)))
	}
	return strings.Join(lines, "\n")
}

// styledCell pairs display text with the style to render it. Width is computed
// from the plain text so ANSI color codes never distort alignment.
type styledCell struct {
	text  string
	style lipgloss.Style
}

// renderTable lays out the same 8 columns as the snapshot, applying threshold
// colors to UTIL% and TEMP and dimming stale rows. Widths are derived from the
// widest plain text in each column so columns stay aligned regardless of color.
func (m model) renderTable(rows []displayRow) string {
	header := []styledCell{
		{"VENDOR", styleHeader}, {"IDX", styleHeader}, {"NAME", styleHeader},
		{"UTIL%", styleHeader}, {"MEM", styleHeader}, {"TEMP", styleHeader},
		{"POWER", styleHeader}, {"CLOCK(G/M)", styleHeader},
	}

	grid := [][]styledCell{header}
	for _, r := range rows {
		grid = append(grid, m.rowCells(r))
	}

	// Column widths from plain text.
	ncol := len(header)
	widths := make([]int, ncol)
	for _, row := range grid {
		for c, cell := range row {
			if w := lipgloss.Width(cell.text); w > widths[c] {
				widths[c] = w
			}
		}
	}

	var b strings.Builder
	for _, row := range grid {
		for c, cell := range row {
			padded := cell.text + strings.Repeat(" ", widths[c]-lipgloss.Width(cell.text))
			b.WriteString(cell.style.Render(padded))
			if c < ncol-1 {
				b.WriteString("   ") // 3-space gutter, matches snapshot spacing
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

// rowCells builds one styled table row from a metric, reusing the snapshot
// format* helpers for the text so the two views can never diverge.
func (m model) rowCells(r displayRow) []styledCell {
	mm := r.metric

	// Base style: dim the whole row when its vendor is stale.
	base := stylePlain
	if r.stale {
		base = styleStale
	}

	// Threshold styles apply only when the field is supported (has a real
	// value) and the row is not already stale-dimmed.
	utilStyle := base
	if !r.stale && mm.IsSupported(collector.FieldUtilizationGPUPercent) &&
		mm.UtilizationGPUPercent >= utilWarnPercent {
		utilStyle = styleWarn
	}
	tempStyle := base
	if !r.stale && mm.IsSupported(collector.FieldTemperatureCelsius) &&
		mm.TemperatureCelsius >= tempWarnCelsius {
		tempStyle = styleCrit
	}

	return []styledCell{
		{mm.Vendor, base},
		{fmt.Sprintf("%d", mm.Index), base},
		{formatName(mm), base},
		{formatUtil(mm), utilStyle},
		{formatMem(mm), base},
		{formatTemp(mm), tempStyle},
		{formatPower(mm), base},
		{formatClock(mm), base},
	}
}

// RunTUI launches the live dashboard. It re-collects from every collector each
// interval and refreshes the table. A single vendor's Collect failure only
// marks that vendor's rows stale — the rest keep updating. The program exits
// cleanly on q/esc/ctrl+c or when ctx is cancelled.
func RunTUI(ctx context.Context, collectors []collector.Collector, interval time.Duration) error {
	// Enforce a lower bound so a tight --interval can't hammer subprocess-based
	// adapters (e.g. rocm-smi) into a fork storm. See SKILL refresh guidance.
	const minInterval = 500 * time.Millisecond
	if interval < minInterval {
		interval = minInterval
	}

	m := newModel(ctx, collectors, interval)
	p := tea.NewProgram(m, tea.WithContext(ctx), tea.WithAltScreen())
	_, err := p.Run()
	// A cancelled context is a normal, user-driven shutdown, not an error.
	if err != nil && ctx.Err() != nil {
		return nil
	}
	return err
}
