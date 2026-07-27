package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/jobs-build/assimilate/internal/spec"
)

const (
	maxLogLines = 5000 // per-build ring capacity; oldest lines drop
	minLeftW    = 20
	maxLeftW    = 44
	// rowOverhead is the non-name part of a left-pane row:
	// cursor(2) + space + elapsed(4) + space + icon(1).
	rowOverhead = 9
)

type (
	eventMsg  struct{ ev spec.Event }
	closedMsg struct{}  // event stream closed: quit is now allowed
	cancelReq struct{}  // cancel request from a signal (filter-injected)
	tickMsg   time.Time // 1s cadence while any build runs (elapsed redraw)
)

var (
	styleDim   = lipgloss.NewStyle().Faint(true)
	styleGreen = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styleRed   = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	styleSel   = lipgloss.NewStyle().Bold(true)
)

// buildRow is one build's UI state.
type buildRow struct {
	name  string
	state spec.BuildState
	info  string // transient note (push progress, error summary)
	log   *ring
	start time.Time // set when the build leaves StatePending
	end   time.Time // set on the terminal transition
}

// model is the TUI. Quit is gated on closedMsg — the stream must drain
// before the program ends — with one escape hatch: a second quit request
// while already cancelling sets forceQuit and quits immediately, so a
// stream that never closes cannot wedge the terminal.
type model struct {
	rows     []buildRow
	selected int

	vp     viewport.Model
	follow bool // pinned to the log tail until the user scrolls up
	spin   spinner.Model

	width, height int
	baseLeftW     int // clamp(longest name+rowOverhead, minLeftW, maxLeftW)

	cancel     context.CancelFunc
	cancelling bool // cancel fired; footer shows cancelling…
	forceQuit  bool // second quit request while cancelling: abandon the drain
	closed     bool
	ticking    bool   // 1s ticker armed
	spinning   bool   // spinner tick armed
	logDirty   bool   // selected ring changed; rebuild coalesced to the next tick
	refreshes  int    // test seam: counts real O(ring) viewport rebuilds
	globalNote string // latest global (Build == -1) line/info, footer right
	clock      func() time.Time
}

func newModel(names []string, cancel context.CancelFunc, clock func() time.Time) model {
	rows := make([]buildRow, len(names))
	longest := 0
	for i, n := range names {
		rows[i] = buildRow{name: n, state: spec.StatePending, log: newRing(maxLogLines)}
		if w := lipgloss.Width(n); w > longest {
			longest = w
		}
	}
	return model{
		rows:      rows,
		vp:        viewport.New(0, 0),
		follow:    true,
		spin:      spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		baseLeftW: clampInt(longest+rowOverhead, minLeftW, maxLeftW),
		cancel:    cancel,
		clock:     clock,
	}
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) { return m.update(msg) }

// update is Update with a concrete receiver type, for tests.
func (m model) update(msg tea.Msg) (model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		return m, nil

	case tea.KeyMsg:
		return m.updateKey(msg)

	case tea.MouseMsg:
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		m.follow = m.vp.AtBottom()
		return m, cmd

	case tickMsg:
		m.flushLog()
		if m.anyActive() {
			return m, tickCmd()
		}
		m.ticking = false
		return m, nil

	case spinner.TickMsg:
		m.flushLog()
		if !m.anyBuilding() {
			m.spinning = false
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case eventMsg:
		return m.apply(msg.ev)

	case cancelReq:
		return m.quitRequest()

	case closedMsg:
		m.closed = true
		m.flushLog()
		return m, tea.Quit
	}
	return m, nil
}

func (m model) updateKey(msg tea.KeyMsg) (model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		m.moveSelection(-1)
	case "down", "j":
		m.moveSelection(+1)
	case "q", "ctrl+c":
		return m.quitRequest()
	case "pgup", "pgdown":
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		m.follow = m.vp.AtBottom()
		return m, cmd
	}
	return m, nil
}

// quitRequest handles q/ctrl-c/SIGINT: the first request fires cancel
// exactly once and waits for the closed sentinel so the stream drains;
// a second request while already cancelling force-quits past a stream
// that never closes.
func (m model) quitRequest() (model, tea.Cmd) {
	if m.cancelling {
		m.forceQuit = true
		return m, tea.Quit
	}
	m.requestCancel()
	return m, nil
}

// requestCancel fires cancel exactly once.
func (m *model) requestCancel() {
	if m.cancelling {
		return
	}
	m.cancelling = true
	if m.cancel != nil {
		m.cancel()
	}
}

// moveSelection selects a neighbouring build and re-pins its log tail.
func (m *model) moveSelection(delta int) {
	if len(m.rows) == 0 {
		return
	}
	sel := clampInt(m.selected+delta, 0, len(m.rows)-1)
	if sel == m.selected {
		return
	}
	m.selected = sel
	m.follow = true
	m.refreshLog()
}

// apply folds one stream event into the model and arms the ticker/spinner
// when a state transition first needs them.
func (m model) apply(ev spec.Event) (model, tea.Cmd) {
	if ev.Build < 0 || ev.Build >= len(m.rows) {
		switch ev.Kind {
		case spec.KindLog:
			m.globalNote = sanitizeLine(ev.Line)
		case spec.KindInfo, spec.KindState:
			if ev.Info != "" {
				m.globalNote = ev.Info
			}
		}
		return m, nil
	}
	row := &m.rows[ev.Build]
	var cmds []tea.Cmd
	switch ev.Kind {
	case spec.KindState:
		prev := row.state
		row.state = ev.State
		switch {
		case ev.Info != "":
			row.info = ev.Info
		case ev.State.Terminal() && ev.State != spec.StateFailed:
			// Drop a stale transient note ("push 3/7") from a settled row;
			// a failed row keeps its detail even when the event has none.
			row.info = ""
		}
		if prev == spec.StatePending && ev.State != spec.StatePending {
			row.start = m.clock()
		}
		if ev.State.Terminal() {
			row.end = m.clock()
			if row.start.IsZero() { // cancelled before it ever started
				row.start = row.end
			}
			m.flushLog() // the last tick may never come; show the final tail now
		}
		if !m.ticking && m.anyActive() {
			m.ticking = true
			cmds = append(cmds, tickCmd())
		}
		if !m.spinning && m.anyBuilding() {
			m.spinning = true
			cmds = append(cmds, m.spin.Tick)
		}
	case spec.KindLog:
		row.log.push(sanitizeLine(ev.Line))
		if ev.Build == m.selected {
			// Coalesce: mark dirty and let the pending tick rebuild, so a
			// verbose build costs O(ring) per tick, not per line.
			m.logDirty = true
			if !m.ticking && !m.spinning {
				m.refreshLog() // no tick is coming; rebuild now so the tail follows
			}
		}
	case spec.KindInfo:
		row.info = ev.Info
	}
	return m, tea.Batch(cmds...)
}

// anyActive: a build is running (left pending, not settled) — the elapsed
// column is live and the 1s ticker must run.
func (m model) anyActive() bool {
	for _, r := range m.rows {
		if r.state != spec.StatePending && !r.state.Terminal() {
			return true
		}
	}
	return false
}

func (m model) anyBuilding() bool {
	for _, r := range m.rows {
		if r.state == spec.StateBuilding {
			return true
		}
	}
	return false
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// --- geometry ---

// leftWidth caps the fixed left pane to half of a narrow terminal.
func (m model) leftWidth() int {
	w := m.baseLeftW
	if m.width > 0 && w > m.width/2 {
		w = max(1, m.width/2)
	}
	return w
}

// layout recomputes the panes after a resize.
func (m *model) layout() {
	m.vp.Width = max(1, m.width-m.leftWidth()-1)
	m.vp.Height = max(1, m.height-2) // right-pane title + footer
	m.refreshLog()
}

// refreshLog reloads the viewport with the selected build's ring buffer,
// hard-truncating lines to the pane width and re-pinning the tail when
// following. O(ring) — appended lines only mark logDirty and rely on
// flushLog at tick cadence.
func (m *model) refreshLog() {
	m.logDirty = false
	if len(m.rows) == 0 || m.vp.Width <= 0 {
		return
	}
	m.refreshes++
	lines := m.rows[m.selected].log.lines()
	for i, l := range lines {
		lines[i] = truncLine(l, m.vp.Width)
	}
	m.vp.SetContent(strings.Join(lines, "\n"))
	if m.follow {
		m.vp.GotoBottom()
	}
}

// flushLog performs the rebuild a coalesced log line deferred.
func (m *model) flushLog() {
	if m.logDirty {
		m.refreshLog()
	}
}

// sanitizeLine makes one raw build-log line inert for the viewport: the
// terminal must render it as text, never execute it. Carriage returns keep
// progress-bar semantics (the text after the last interior \r wins), every
// ANSI escape sequence (CSI/OSC/DCS/SGR/…) is stripped, tabs expand to
// 8-column stops, and remaining C0/DEL control bytes drop. RunPlain
// intentionally bypasses this: non-TTY consumers get the raw bytes.
func sanitizeLine(s string) string {
	s = strings.TrimRight(s, "\r\n")
	if i := strings.LastIndexByte(s, '\r'); i >= 0 {
		s = s[i+1:]
	}
	s = ansi.Strip(s) // drops escape sequences but keeps C0 controls
	if !strings.ContainsFunc(s, isCtrl) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	col := 0
	for _, r := range s {
		switch {
		case r == '\t':
			n := 8 - col%8
			b.WriteString(strings.Repeat(" ", n))
			col += n
		case isCtrl(r):
			// dropped (\a, \b, …)
		default:
			b.WriteRune(r)
			col += ansi.StringWidth(string(r))
		}
	}
	return b.String()
}

func isCtrl(r rune) bool { return r < 0x20 || r == 0x7f }

func truncLine(s string, w int) string {
	if ansi.StringWidth(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, "…")
}

// --- view ---

// View renders left build list │ right log pane, footer last. Must not
// crash before the first WindowSizeMsg.
func (m model) View() string {
	if m.width <= 0 || m.height <= 0 {
		return "starting…"
	}
	leftW := m.leftWidth()
	contentH := max(1, m.height-1)
	left := m.leftLines(leftW, contentH)
	right := m.rightLines(max(1, m.width-leftW-1), contentH)
	var b strings.Builder
	for i := 0; i < contentH; i++ {
		b.WriteString(left[i])
		b.WriteString(styleDim.Render("│"))
		b.WriteString(right[i])
		b.WriteByte('\n')
	}
	b.WriteString(m.footer())
	return b.String()
}

// leftLines renders the build list, windowed around the selection when it
// outgrows the pane.
func (m model) leftLines(w, h int) []string {
	top := 0
	if len(m.rows) > h {
		top = clampInt(m.selected-h/2, 0, len(m.rows)-h)
	}
	lines := make([]string, h)
	for i := range lines {
		if j := top + i; j < len(m.rows) {
			lines[i] = m.rowLine(j, w)
		} else {
			lines[i] = strings.Repeat(" ", w)
		}
	}
	return lines
}

// rowLine is one left-pane row: '<cursor> <name> <elapsed> <icon>', the
// Info note dim in the spare name space; always exactly w cells wide.
func (m model) rowLine(i, w int) string {
	r := m.rows[i]
	cursor := "  "
	if i == m.selected {
		cursor = "> "
	}
	nameW := max(1, w-rowOverhead)
	field := truncLine(r.name, nameW)
	if i == m.selected {
		field = styleSel.Render(field)
	}
	if spare := nameW - lipgloss.Width(field); r.info != "" && spare >= 5 {
		field += " " + styleDim.Render(truncLine(r.info, spare-1))
	}
	fill := max(0, nameW-lipgloss.Width(field))
	line := cursor + field + strings.Repeat(" ", fill) +
		fmt.Sprintf(" %4s ", fmtElapsed(m.elapsed(r))) + m.icon(r.state)
	return truncLine(line, w)
}

// rightLines is the selected build's title over its log viewport.
func (m model) rightLines(w, h int) []string {
	lines := make([]string, h)
	if len(m.rows) > 0 {
		r := m.rows[m.selected]
		title := " " + r.name + " — " + string(r.state)
		if r.info != "" {
			title += " · " + r.info
		}
		lines[0] = truncLine(title, w)
	}
	vpLines := strings.Split(m.vp.View(), "\n")
	for i := 1; i < h; i++ {
		if i-1 < len(vpLines) {
			lines[i] = vpLines[i-1]
		}
	}
	return lines
}

func (m model) footer() string {
	done := 0
	for _, r := range m.rows {
		if r.state.Terminal() {
			done++
		}
	}
	base := fmt.Sprintf(" %d/%d done", done, len(m.rows))
	var line string
	if m.cancelling {
		line = styleDim.Render(base+" · ") + styleRed.Render("cancelling…") +
			styleDim.Render(" (q again to force quit)")
	} else {
		line = styleDim.Render(base + " · ↑/↓ select · PgUp/PgDn scroll · q cancel")
	}
	if m.globalNote != "" {
		note := styleDim.Render(truncLine(m.globalNote, m.width/2))
		if gap := m.width - lipgloss.Width(line) - lipgloss.Width(note); gap >= 1 {
			line += strings.Repeat(" ", gap) + note
		}
	}
	return truncLine(line, m.width)
}

// elapsed: negative = not started (pending), frozen at end once terminal.
func (m model) elapsed(r buildRow) time.Duration {
	if r.start.IsZero() {
		return -1
	}
	if r.state.Terminal() {
		return r.end.Sub(r.start)
	}
	return m.clock().Sub(r.start)
}

// fmtElapsed renders a duration in at most 4 cells ("" for not started).
func fmtElapsed(d time.Duration) string {
	if d < 0 {
		return ""
	}
	s := int(d / time.Second)
	switch {
	case s < 100:
		return strconv.Itoa(s) + "s"
	case s < 100*60:
		return strconv.Itoa(s/60) + "m"
	default:
		return strconv.Itoa(min(s/3600, 99)) + "h"
	}
}

func (m model) icon(st spec.BuildState) string {
	switch st {
	case spec.StatePushing:
		return "⇡"
	case spec.StateBuilding:
		return m.spin.View()
	case spec.StateDone:
		return styleGreen.Render("✓")
	case spec.StateFailed:
		return styleRed.Render("✗")
	case spec.StateCancelled:
		return styleDim.Render("∅")
	default: // pending
		return styleDim.Render("·")
	}
}

func clampInt(v, lo, hi int) int {
	return min(max(v, lo), hi)
}
