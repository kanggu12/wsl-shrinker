package tui

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/kanggu12/wsl-shrinker/internal/core/units"
	"github.com/kanggu12/wsl-shrinker/internal/engine/disks"
	"github.com/kanggu12/wsl-shrinker/internal/engine/journal"
	"github.com/kanggu12/wsl-shrinker/internal/engine/shrink"
	"github.com/kanggu12/wsl-shrinker/internal/engine/survey"
	"github.com/kanggu12/wsl-shrinker/internal/front/i18n"
	"github.com/kanggu12/wsl-shrinker/internal/front/ui"
	"github.com/kanggu12/wsl-shrinker/internal/machine/wsl"
	buildversion "github.com/kanggu12/wsl-shrinker/internal/version"
)

// sideBySide is the width from which the details sit beside the table.
const sideBySide = 110

// spinner is the frames of the mark on a step that runs.
var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// View renders the whole screen.
func (m model) View() tea.View {
	var body string
	switch {
	// Below this there is no room for the table: say so, in one line.
	case m.width < 40 || m.height < 12:
		body = m.frame([]string{ui.Fg(m.th.Muted).Render(i18n.T("small"))})
	case m.mode == modeHelp:
		body = m.helpView()
	case m.mode == modeHistory:
		body = m.historyView()
	case m.mode == modeClean:
		body = m.cleanView()
	case m.mode == modeRun:
		body = m.runView()
	default:
		body = m.mainView()
	}
	// The marks a console's font may not have, swapped once all is laid out.
	v := tea.NewView(ui.Marks(body))
	v.AltScreen = true
	v.WindowTitle = "wsl-shrinker"
	return v
}

// frame pads or cuts every line to exactly the screen width and the block
// to exactly the screen height, so nothing wraps and nothing is left over.
func (m model) frame(lines []string) string {
	out := make([]string, m.height)
	for i := range out {
		if i < len(lines) {
			out[i] = ui.Fit(strings.ReplaceAll(lines[i], "\n", " "), m.width)
		} else {
			out[i] = strings.Repeat(" ", m.width)
		}
	}
	return strings.Join(out, "\n")
}

// page lays out a screen: the header, a blank row, the body, then the
// status line and the keys at the bottom.
func (m model) page(body []string, status, keys string) string {
	lines := append([]string{m.header(), ""}, body...)
	for len(lines) < m.height-2 {
		lines = append(lines, "")
	}
	lines = append(lines[:m.height-2], status, keys)
	return m.frame(lines)
}

// bodyHeight is the rows between the header and the status line.
func (m model) bodyHeight() int { return m.height - 4 }

// header is the top bar: the name and WSL's version on the left, what all
// disks could give back on the right.
func (m model) header() string {
	t := m.th
	// The name with this build's version beside it, then WSL's, apart.
	chip, version, spin := t.Chip("wsl-shrinker")+" "+ui.Fg(t.Ink).Render(buildversion.Short()), "", ""
	if m.info.Version != "" {
		version = ui.Fg(t.Muted).Render("  ·  WSL " + m.info.Version)
	}
	if m.loading {
		spin = ui.Fg(t.Muted).Render(" ↻")
	}
	var right []string
	if m.loaded && m.err == nil && len(m.list) > 0 {
		// What can come back now, and apart from it what a program holds
		// (Docker Desktop's disk, until it is closed). A disk that cannot be
		// shrunk now for another reason counts in neither.
		var now, held int64
		for _, d := range m.list {
			switch {
			case d.Estimate == nil || m.b.Protected(d.Name):
			case d.Distro == "" && d.File.Held:
				held += d.Estimate.Reclaim
			case m.block(d) == "" && d.Estimate.Reclaim >= m.b.MinGain():
				// Worth shrinking, as the status line and CLI mode count.
				now += d.Estimate.Reclaim
			}
		}
		right = append(right, ui.Fg(t.Cat[ui.CatGain]).Render(ui.Block+" "+i18n.T("header.total.now", units.Bytes(now))))
		if held > 0 {
			right = append(right, ui.Fg(t.Cat[ui.CatLive]).Render(i18n.T("header.held", units.Bytes(held))))
		}
		if free := m.driveFree(); free != "" {
			right = append(right, ui.Fg(t.Muted).Render(free))
		}
	}
	// fit lays out a left side and right pieces, if they fit with a gap.
	fit := func(left string, right []string) (string, bool) {
		r := strings.Join(right, ui.Fg(t.Faint).Render(" · "))
		gap := m.width - lipgloss.Width(left) - lipgloss.Width(r) - 2
		if gap < 2 {
			return "", false
		}
		return " " + left + strings.Repeat(" ", gap) + r + " ", true
	}
	// When it does not all fit, pieces go in this order: the drive's free
	// space, what is in use, then WSL's version; the total stays longest.
	for n := len(right); n > 0; n-- {
		if line, ok := fit(chip+version+spin, right[:n]); ok {
			return line
		}
	}
	if len(right) > 0 {
		if line, ok := fit(chip+spin, right[:1]); ok {
			return line
		}
	}
	return " " + chip + version + spin
}

// driveFree names the free space of the drive the default disk is on.
func (m model) driveFree() string {
	for _, d := range m.list {
		if d.File.DriveFree > 0 && len(d.Path) >= 2 && d.Path[1] == ':' {
			return i18n.T("header.free", strings.ToUpper(d.Path[:2]), units.Bytes(d.File.DriveFree))
		}
	}
	return ""
}

// mainView is the disks: the table and the selected disk's details.
func (m model) mainView() string {
	// statusRows is how many rows a message takes at most: enough for a
	// failure's two sentences in the narrowest window.
	const statusRows = 3
	t := m.th
	h := m.bodyHeight()
	// A message too long for the status row takes the rows above it as
	// well, statusRows in all at most: its end says what to do. The body
	// gives those rows up.
	status := []string{m.statusLine()}
	text, st, ok := m.message()
	switch {
	case m.mode == modeForget:
		// The box says the run itself, and the keys this row names do
		// nothing while it is open: the row is empty rather than half
		// covered by the box.
		status = []string{""}
	case ok && lipgloss.Width(text)+1 > m.width:
		// Wrapped plain and colored after: a message cut short is marked in
		// its own text, inside the width.
		rows := m.wrapTo(text, m.width-1, "", lipgloss.NewStyle())
		if len(rows) > statusRows {
			rows = rows[:statusRows]
			rows[statusRows-1] = strings.TrimRight(ansi.Truncate(rows[statusRows-1], m.width-3, ""), " ") + "…"
		}
		status = status[:0]
		for _, r := range rows {
			status = append(status, " "+st.Render(r))
		}
		h -= len(status) - 1
	}
	var body []string
	switch {
	case !m.loaded:
		// The logo while the disks are read, and what is going on under it.
		body = m.centered(h, append(m.logoBlock(), "", ui.Fg(t.Muted).Render(i18n.T("loading")))...)
	case m.err != nil:
		// The error says what is wrong in WSL's own words: show it whole,
		// under what to do when that is known.
		bw := min(m.width-8, 90)
		box := lipgloss.NewStyle().Width(bw).Align(lipgloss.Center)
		// Wrapped in the box, a command or a name in the text stays whole.
		lines := func(st lipgloss.Style, s string) []string { return strings.Split(unhold(st.Render(hold(s, bw))), "\n") }
		var parts []string
		switch {
		case notEnabled(m.err):
			parts = append(parts, lines(box.Foreground(t.Ink).Bold(true), i18n.T("error.notenabled"))...)
			parts = append(parts, "")
			parts = append(parts, lines(box.Foreground(t.Muted).Bold(false), oneLine(m.err.Error()))...)
		case noAnswer(m.err):
			// Not an error in the disks: WSL starting, updating or stuck.
			// What to do first, then the call that went unanswered.
			parts = append(parts, lines(box.Foreground(t.Ink).Bold(true), i18n.T("error.noanswer"))...)
			parts = append(parts, "")
			parts = append(parts, lines(box.Foreground(t.Muted).Bold(false), oneLine(m.err.Error()))...)
		default:
			parts = append(parts, lines(box.Foreground(t.Cat[ui.CatBad]), i18n.T("error.list", oneLine(m.err.Error())))...)
			parts = append(parts, "", ui.Fg(t.Muted).Render(i18n.T("error.retry")))
		}
		body = m.centered(h, parts...)
	case len(m.list) == 0:
		body = m.centered(h,
			lipgloss.NewStyle().Bold(true).Foreground(t.Ink).Render(i18n.T("empty.title")),
			ui.Fg(t.Muted).Render(i18n.T("empty.hint")))
	case m.width >= sideBySide:
		// Table and recent runs on the left, details on the right.
		lw := m.width * 56 / 100
		left := append(m.table(lw, m.tableRows()), "")
		left = append(left, m.recent(lw, h-len(left))...)
		right := m.details(m.width-lw-3, h)
		for i := 0; i < h; i++ {
			l, r := "", ""
			if i < len(left) {
				l = left[i]
			}
			if i < len(right) {
				r = right[i]
			}
			body = append(body, ui.Fit(l, lw)+" "+ui.Fg(t.Rule).Render("│")+" "+r)
		}
	default:
		// Narrow: the table, then as much of the details as fits, one cell
		// in, where the table's rows start.
		body = append(m.table(m.width, m.tableRows()), "")
		for _, l := range m.details(m.width-2, h-len(body)) {
			body = append(body, " "+l)
		}
	}
	// All of a long message but its last row goes where the body ends.
	if n := len(status) - 1; n > 0 {
		for len(body) < h {
			body = append(body, "")
		}
		body = append(body[:h], status[:n]...)
		status = status[n:]
	}
	screen := m.page(body, status[0], m.footer())
	if m.mode == modeConfirm {
		return m.overlay(screen, m.confirmBox())
	}
	if m.mode == modeForget {
		return m.overlay(screen, m.forgetBox())
	}
	return screen
}

// centered puts a few lines in the middle of a block of height h.
func (m model) centered(h int, lines ...string) []string {
	out := make([]string, 0, h)
	for len(out) < (h-len(lines))/2 {
		out = append(out, "")
	}
	for _, l := range lines {
		out = append(out, lipgloss.PlaceHorizontal(m.width, lipgloss.Center, l))
	}
	return out
}

// logoBlock is the logo as the screen draws it: the mark, and beside it the
// name with this build's version over the line that says what it does. Its
// lines are padded to one width, so placed in the middle the mark stays square.
func (m model) logoBlock() []string {
	t := m.th
	mark := t.Logo()
	name := lipgloss.NewStyle().Foreground(t.Ink).Bold(true).Render("wsl-shrinker") + "  " + ui.Fg(t.Ink).Render(buildversion.Short())
	room := max(10, m.width-lipgloss.Width(mark[0])-6)
	lines := []string{
		mark[0] + "   " + name,
		mark[1] + "   " + ui.Fg(t.Muted).Render(ui.Fit(i18n.T("logo.tagline"), room)),
		mark[2],
	}
	w := 0
	for _, l := range lines {
		w = max(w, lipgloss.Width(l))
	}
	for i, l := range lines {
		lines[i] = l + strings.Repeat(" ", w-lipgloss.Width(l))
	}
	return lines
}

// category says what a disk's color means.
func (m model) category(d disks.Disk) ui.Category {
	switch m.block(d) {
	case shrink.BlockProtected:
		return ui.CatSafe
	case shrink.BlockHeld:
		// In use by its program: it can be shrunk once that program stops.
		return ui.CatLive
	case "":
		// Nothing in the way: what it gives back decides.
		if d.Estimate.Reclaim >= m.b.MinGain() {
			return ui.CatGain
		}
		return ui.CatSmall
	}
	// Any other reason is one it cannot be shrunk for now.
	return ui.CatBad
}

// sizeW and barW are the widths of a size column and of a row's bar.
const sizeW, barW = 8, 6

// columns is which of the table's columns fit a width, and how wide its
// name and state columns are.
type columns struct {
	nameW, stateW           int
	state, file, after, bar bool
}

// tableColumns drops columns until a row fits w cells, the least needed
// first: the bar, the size after, the file's size, the state. What a shrink
// frees always stays. stateW is the widest state among the disks listed.
func tableColumns(w, stateW int) columns {
	c := columns{state: true, file: true, after: true, bar: true, stateW: stateW}
	// The state column fits its usual words in the language in use, so it
	// stays where it is as disks start and stop; a word longer than those
	// (a disk that cannot be read) widens it rather than lose its end.
	for _, k := range []string{"col.state", "state.running", "state.stopped", "state.inuse", "state.file", "state.protected"} {
		c.stateW = max(c.stateW, lipgloss.Width(i18n.T(k)))
	}
	for {
		// 5 cells lead a row (space, mark, space, dot, space); every column
		// after the name takes its width and a gap of 2.
		used := 5 + 2 + sizeW
		if c.state {
			used += 2 + c.stateW
		}
		if c.file {
			used += 2 + sizeW
		}
		if c.after {
			used += 2 + sizeW
		}
		if c.bar {
			used += 2 + barW
		}
		c.nameW = min(28, w-used)
		switch {
		case c.nameW >= 10:
			return c
		case c.bar:
			c.bar = false
		case c.after:
			c.after = false
		case c.file:
			c.file = false
		case c.state:
			c.state = false
		default:
			// Nothing left to drop: the name takes what remains.
			c.nameW = max(1, c.nameW)
			return c
		}
	}
}

// cells joins a row's columns, as many as fit.
func (c columns) cells(name, state, file, after, frees, bar string) string {
	s := ui.Fit(name, c.nameW)
	if c.state {
		s += "  " + ui.Fit(state, c.stateW)
	}
	if c.file {
		s += "  " + ui.FitRight(file, sizeW)
	}
	if c.after {
		s += "  " + ui.FitRight(after, sizeW)
	}
	s += "  " + ui.FitRight(frees, sizeW)
	if c.bar {
		s += "  " + bar
	}
	return s
}

// table is the disks, one row each, w cells wide, at most rows of them: a
// longer list shows the window around the selected disk.
func (m model) table(w, rows int) []string {
	t := m.th
	// Every disk's state, not only the rows in view: the columns stay
	// where they are as the list scrolls.
	stateW := 0
	for _, d := range m.list {
		stateW = max(stateW, lipgloss.Width(m.stateWord(d)))
	}
	c := tableColumns(w, stateW)
	// The heading starts where the names do, after the 5 cells that lead a row.
	head := "     " + c.cells(i18n.T("col.disk"), i18n.T("col.state"), i18n.T("col.file"), i18n.T("col.after"),
		i18n.T("col.frees"), "")
	lines := []string{lipgloss.NewStyle().Foreground(t.Muted).Bold(true).Render(strings.TrimRight(head, " "))}
	// The bars share one scale: the disk that gives back most fills its bar.
	var most int64
	for _, d := range m.list {
		if d.Estimate != nil {
			most = max(most, d.Estimate.Reclaim)
		}
	}
	top := m.tableWindow(rows)
	for i := top; i < len(m.list) && i < top+rows; i++ {
		d := m.list[i]
		cat := m.category(d)
		mark := "  "
		if m.marked[d.Name] {
			mark = ui.Fg(t.Mark).Render("✓") + " "
		}
		file, after, frees := "—", "—", "—"
		bar := strings.Repeat(" ", barW)
		if e := d.Estimate; e != nil {
			file, after, frees = units.Bytes(e.FileSize), units.Bytes(e.After), units.Bytes(e.Reclaim)
			bar = t.Bar(barW, most, ui.Part{Value: e.Reclaim, Color: t.Cat[cat]})
		}
		row := " " + mark + t.Dot(cat) + " " + c.cells(d.Name, m.stateWord(d), file, after, ui.Fg(t.Cat[cat]).Render(frees), bar)
		if i == m.sel {
			row = ui.Highlight(row, t.Selected, w)
		}
		lines = append(lines, row)
	}
	return lines
}

// stateWord is a disk's state, colored.
func (m model) stateWord(d disks.Disk) string {
	t := m.th
	switch {
	case m.b.Protected(d.Name):
		// Said in words, not by its pink alone: it is never shrunk, whatever
		// else it is.
		return ui.Fg(t.Cat[ui.CatSafe]).Render(i18n.T("state.protected"))
	case d.Err != "" || d.Estimate == nil:
		// A disk that cannot be shrunk says why, not "stopped", which reads
		// as ready.
		return ui.Fg(t.Cat[ui.CatBad]).Render(i18n.T("state.unreadable"))
	case d.File.Encrypted:
		return ui.Fg(t.Cat[ui.CatBad]).Render(i18n.T("state.encrypted"))
	case d.Estimate.FSErrors:
		return ui.Fg(t.Cat[ui.CatBad]).Render(i18n.T("state.fserrors"))
	case d.Estimate.Fixed:
		return ui.Fg(t.Cat[ui.CatBad]).Render(i18n.T("state.fixed"))
	case d.Distro == "" && d.File.Held:
		// Docker Desktop's disk, or a file from the settings another program
		// has open.
		return ui.Fg(t.Cat[ui.CatLive]).Render(i18n.T("state.inuse"))
	case d.Kind != disks.KindDistro:
		return ui.Fg(t.Muted).Render(i18n.T("state.file"))
	case d.Running:
		return ui.Fg(t.Cat[ui.CatLive]).Render(i18n.T("state.running"))
	}
	return ui.Fg(t.Muted).Render(i18n.T("state.stopped"))
}

// recent is the last runs, under the table.
func (m model) recent(w, h int) []string {
	if h < 3 || len(m.hist) == 0 {
		return nil
	}
	t := m.th
	lines := []string{lipgloss.NewStyle().Foreground(t.Muted).Bold(true).Render(" " + i18n.T("recent.title"))}
	for i := len(m.hist) - 1; i >= 0 && len(lines) < h; i-- {
		e := m.hist[i]
		when := ui.Fg(t.Muted).Render(e.At.Local().Format("01-02 15:04"))
		var what string
		if e.Err != "" {
			what = ui.Fg(t.Cat[ui.CatBad]).Render(i18n.T("recent.failed", e.Disk))
		} else {
			// The disk first, then what it gave back: "Debian freed 20.0 GB".
			what = e.Disk + " " + ui.Fg(t.Cat[ui.CatDone]).Render(i18n.T("recent.freed", units.Bytes(e.Freed))) +
				ui.Fg(t.Muted).Render(" · "+units.Bytes(e.Before)+" → "+units.Bytes(e.After))
		}
		lines = append(lines, ui.Fit(" "+when+"  "+what, w))
	}
	return lines
}

// details is the selected disk: what it gives back, its checks, what fills
// it, and where it is.
func (m model) details(w, h int) []string {
	t := m.th
	d, ok := m.selected()
	if !ok {
		return nil
	}
	bold := lipgloss.NewStyle().Bold(true).Foreground(t.Ink)
	section := func(s string) string { return lipgloss.NewStyle().Bold(true).Foreground(t.Muted).Render(s) }
	cat := m.category(d)
	lines := []string{bold.Render(d.Name) + ui.Fg(t.Muted).Render("  "+m.kindWords(d))}
	// The verdict, in the disk's color.
	var verdict string
	switch m.block(d) {
	case shrink.BlockProtected:
		verdict = i18n.T("verdict.protected")
	case shrink.BlockUnreadable:
		verdict = i18n.T("verdict.unreadable")
	case shrink.BlockHeld:
		verdict = i18n.T(heldKey(d, "verdict.held"), units.Bytes(d.Estimate.Reclaim))
	case shrink.BlockEncrypted:
		verdict = i18n.T("verdict.encrypted", units.Bytes(d.Estimate.Reclaim))
	case shrink.BlockFSErrors:
		verdict = i18n.T("verdict.fserrors", units.Bytes(d.Estimate.Reclaim))
	case shrink.BlockFixed:
		verdict = i18n.T("verdict.fixed")
	case shrink.BlockNoCompact:
		verdict = i18n.T("verdict.nocompact", units.Bytes(d.Estimate.Reclaim))
	case "":
		if cat == ui.CatGain {
			verdict = i18n.T("verdict.gain", units.Bytes(d.Estimate.Reclaim))
		} else {
			verdict = i18n.T("verdict.small", units.Bytes(d.Estimate.Reclaim))
		}
	}
	// The verdict wraps rather than lose its end; in a pane too short for
	// all of it, the last line that fits ends where it was cut.
	vst := ui.Fg(t.Cat[cat]).Bold(true)
	vl := m.wrapTo(verdict, w, "", vst)
	if room := h - len(lines); room > 0 && len(vl) > room {
		rest := make([]string, 0, len(vl)-room+1)
		for _, l := range vl[room-1:] {
			rest = append(rest, ansi.Strip(l))
		}
		vl = append(vl[:room-1], vst.Render(ui.Fit(strings.Join(rest, " "), w)))
	}
	lines = append(lines, vl...)
	// The rest goes in sections, each whole or not at all, so a short
	// window never shows a heading without what is under it. The path
	// goes last, when there is room.
	add := func(sec []string) bool {
		if len(lines)+1+len(sec) > h {
			return false
		}
		lines = append(lines, "")
		lines = append(lines, sec...)
		return true
	}
	if e := d.Estimate; e != nil {
		// One bar for the file: what stays, and what a shrink gives back.
		bw := min(w-2, 44)
		sec := []string{section(i18n.T("details.space")),
			// The part that stays in plain grey; the part that goes in the
			// disk's own color (teal, or pink when protected).
			" " + t.Bar(bw, e.FileSize, ui.Part{Value: e.After, Color: t.Stays}, ui.Part{Value: e.Reclaim, Color: t.Cat[cat]})}
		// Side by side, or one under the other when that does not fit.
		stays := " " + ui.Fg(t.Stays).Render(ui.Block) + " " + i18n.T("details.stays", units.Bytes(e.After))
		goes := ui.Fg(t.Cat[cat]).Render(ui.Block) + " " + goesWords(m.b.Protected(d.Name), e.Reclaim)
		if lipgloss.Width(stays+"   "+goes) <= w {
			sec = append(sec, stays+"   "+goes)
		} else {
			sec = append(sec, stays, " "+goes)
		}
		if e.Filesystem {
			sec = append(sec, ui.Fg(t.Muted).Render(" "+i18n.T("details.used", units.Bytes(e.Used))))
		}
		add(sec)
	}
	// The checks a run makes first, as they stand now.
	checks := []string{section(i18n.T("details.checks"))}
	for _, c := range m.checks(d) {
		checks = append(checks, m.wrapHang(c, w, " ", "   ", lipgloss.NewStyle())...)
	}
	add(checks)
	// The inside of a running distribution: the findings take the rows left
	// above the path, and need at least three.
	if d.Distro != "" {
		rows := h - len(lines) - 4
		if inside := m.inside(d, w, rows); rows >= 3 || len(inside) <= rows+1 {
			add(append([]string{section(i18n.T("details.inside"))}, inside...))
		} else if s := m.surveys[d.Distro]; d.Running && s != nil && !s.loading {
			// No room for the list: one line still says it is there, or that
			// the look failed, which nothing else on the screen says.
			switch {
			case s.err != nil:
				add([]string{ui.Fg(t.Cat[ui.CatBad]).Render(i18n.T("inside.failed.short"))})
			case len(s.r.Items) > 0:
				var found int64
				for _, it := range s.r.Items {
					found += it.Bytes
				}
				add([]string{ui.Fg(t.Muted).Render(i18n.T("inside.summary", units.Bytes(found)))})
			}
		}
	}
	add([]string{ui.Fg(t.Muted).Render(ui.Middle(d.Path, w))})
	for i := range lines {
		lines[i] = ui.Fit(lines[i], w)
	}
	return lines
}

// heldKey is the key of a message about a disk a program holds: the
// program that keeps it for itself (Docker Desktop, Rancher Desktop, a
// Podman machine), or, for a file from the settings, another program.
func heldKey(d disks.Disk, key string) string { return ownerKey(shrink.OwnerOf(d), key) }

// ownerKey completes key with the program that keeps the disk: Docker
// Desktop's words are the key itself, the other programs' have their own,
// and a file another program has open has ".file".
func ownerKey(o shrink.Owner, key string) string {
	switch o {
	case shrink.OwnerDocker:
		return key
	case shrink.OwnerRancher:
		return key + ".rancher"
	case shrink.OwnerPodman:
		return key + ".podman"
	}
	return key + ".file"
}

// kindWords says what a disk belongs to.
func (m model) kindWords(d disks.Disk) string {
	switch {
	case d.Kind == disks.KindDocker:
		return i18n.T("kind.docker")
	case d.Kind == disks.KindFile:
		return i18n.T("kind.file")
	case d.Default:
		return i18n.T("kind.default")
	}
	return i18n.T("kind.distro")
}

// checks are the conditions a run checks first, each with its mark.
func (m model) checks(d disks.Disk) []string {
	t := m.th
	ok := func(s string) string { return ui.Fg(t.Cat[ui.CatDone]).Render("✓") + " " + s }
	bad := func(s string) string { return ui.Fg(t.Cat[ui.CatBad]).Render("✗") + " " + s }
	// In use by its program: indigo, as the disk shows everywhere else.
	inUse := func(s string) string { return ui.Fg(t.Cat[ui.CatLive]).Render(ui.Block) + " " + s }
	note := func(s string) string { return ui.Fg(t.Cat[ui.CatWarn]).Render("!") + " " + s }
	var out []string
	if d.Estimate == nil {
		return []string{bad(i18n.T("check.unreadable", oneLine(d.Err)))}
	}
	if d.Distro != "" {
		// A distribution another program runs for itself is that
		// program's while it runs, whatever WSL can do.
		if shrink.OwnerOf(d) != shrink.OwnerNone && d.Running {
			out = append(out, inUse(i18n.T(heldKey(d, "check.held"))))
		}
		if m.info.CanCompact {
			out = append(out, ok(i18n.T("check.compact", m.info.Version)))
		} else if m.infoKnown {
			out = append(out, bad(i18n.T("check.nocompact")))
		}
	} else if d.File.Held {
		out = append(out, inUse(i18n.T(heldKey(d, "check.held"))))
	} else {
		out = append(out, ok(i18n.T("check.free")))
	}
	if d.File.Encrypted {
		out = append(out, bad(i18n.T("check.encrypted")))
	}
	if d.File.Sparse {
		out = append(out, note(i18n.T("check.sparse")))
	}
	if d.Estimate.Fixed {
		out = append(out, bad(i18n.T("check.fixed")))
	}
	switch {
	case d.Estimate.FSErrors:
		// Errors the kernel recorded: the run refuses the disk until e2fsck
		// has checked it, so this line takes the place of how it read.
		out = append(out, bad(i18n.T("check.fserrors")))
	case d.Estimate.InUse && d.Estimate.Mounted:
		out = append(out, note(i18n.T("check.live")))
	case d.Estimate.InUse:
		out = append(out, ok(i18n.T("check.held.read")))
	default:
		out = append(out, ok(i18n.T("check.clean")))
	}
	if !d.Estimate.Filesystem {
		out = append(out, note(i18n.T("check.nofs")))
	}
	return out
}

// inside is what fills a distribution: the largest findings, or why they
// are not shown.
func (m model) inside(d disks.Disk, w, rows int) []string {
	t := m.th
	if !d.Running {
		return []string{ui.Fg(t.Muted).Render(" " + i18n.T("inside.stopped"))}
	}
	s := m.surveys[d.Distro]
	switch {
	case s == nil || s.loading:
		return []string{ui.Fg(t.Muted).Render(" " + spinner[m.spin%len(spinner)] + " " + i18n.T("inside.loading"))}
	case s.err != nil:
		// The reason wraps rather than lose its end, up to a few lines.
		lines := m.wrapTo(i18n.T("inside.failed", oneLine(s.err.Error())), w, " ", ui.Fg(t.Cat[ui.CatBad]))
		return lines[:min(len(lines), 4)]
	case len(s.r.Items) == 0:
		return []string{ui.Fg(t.Muted).Render(" " + i18n.T("inside.nothing"))}
	}
	var out []string
	var total int64
	for _, it := range s.r.Items {
		total += it.Bytes
	}
	// As many as fit, with a line for the rest and the hint: at least three.
	show := max(3, rows-2)
	for i, it := range s.r.Items {
		if i == show && len(s.r.Items) > show {
			out = append(out, ui.Fg(t.Muted).Render(" "+i18n.T("inside.more", len(s.r.Items)-show)))
			break
		}
		check := " "
		if m.picked[pickKey(d.Distro, it.ID)] {
			check = ui.Fg(t.Mark).Render("✓")
		}
		out = append(out, " "+check+ui.FitRight(units.Bytes(it.Bytes), 8)+"  "+m.kindTag(it.Kind)+"  "+ui.Fit(itemLabel(it), w-14-kindWidth()))
	}
	// The hint wraps rather than lose the amount at its end.
	out = append(out, m.wrapTo(i18n.T("inside.hint", units.Bytes(total)), w, " ", ui.Fg(t.Muted))...)
	return out
}

// kindTag is a finding's kind as a short colored word of fixed width.
func (m model) kindTag(k survey.Kind) string {
	// One quiet color: the colors of this screen mean a disk's state.
	return ui.Fg(m.th.Muted).Render(ui.Fit(i18n.T("kind."+string(k)), kindWidth()))
}

// goesWords is the legend of the part a shrink gives back.
func goesWords(protected bool, n int64) string {
	if protected {
		return i18n.T("details.goes.protected", units.Bytes(n))
	}
	return i18n.T("details.goes", units.Bytes(n))
}

// kindWidth is the width of the longest kind word in the language in use.
func kindWidth() int {
	w := 0
	for _, k := range []survey.Kind{survey.KindCache, survey.KindDownload, survey.KindLog, survey.KindImage} {
		w = max(w, lipgloss.Width(i18n.T("kind."+string(k))))
	}
	return w
}

// itemLabel names a finding: its kind of thing and where it is.
func itemLabel(it survey.Item) string {
	name := i18n.T("what." + it.What)
	if strings.HasPrefix(name, "what.") {
		name = it.What
	}
	switch it.What {
	case "image", "docker-build-cache", "docker-dangling", "apt", "journal":
		if it.What == "image" {
			return it.Label
		}
		return name
	}
	// A cache: its tool, then whose home it is in.
	owner := it.Label
	if strings.HasPrefix(owner, "/home/") {
		owner, _, _ = strings.Cut(strings.TrimPrefix(owner, "/home/"), "/")
	} else if strings.HasPrefix(owner, "/root/") {
		owner = "root"
	}
	return i18n.T("item.owner", name, owner)
}

// message is what the status row says instead of the counts: a passing
// message, or a run that has not ended, in its color; ok is false when
// there is none.
func (m model) message() (text string, st lipgloss.Style, ok bool) {
	t := m.th
	if m.toast != "" && time.Now().Before(m.toastUntil) {
		return m.toast, ui.Fg(t.Cat[m.toastCat]), true
	}
	// A run that has not ended matters more than the counts, and is said
	// where no disk is listed too: the journal is read apart from WSL, and
	// a cut-off run of a disk that is gone still waits for R.
	switch m.unfinishedState {
	case journal.CutOff:
		unknown := m.unfinished.Step == "" || m.unfinished.Disk == ""
		switch {
		// After an R that could not start things again: the two ways on.
		case m.recoverFailed && unknown:
			return i18n.T("unfinished.unknown.failed"), ui.Fg(t.Cat[ui.CatWarn]), true
		case m.recoverFailed:
			return i18n.T("unfinished.cutoff.failed", m.unfinished.Disk), ui.Fg(t.Cat[ui.CatWarn]), true
		case unknown:
			return i18n.T("unfinished.unknown"), ui.Fg(t.Cat[ui.CatWarn]), true
		}
		return i18n.T("unfinished.cutoff", m.unfinished.Disk, i18n.T("step."+m.unfinished.Step)), ui.Fg(t.Cat[ui.CatWarn]), true
	case journal.Going:
		// A journal that does not parse yet: a run starting, its disk not
		// said.
		if m.unfinished.Disk == "" {
			return i18n.T("unfinished.going.unknown"), ui.Fg(t.Cat[ui.CatLive]), true
		}
		return i18n.T("unfinished.going", m.unfinished.Disk), ui.Fg(t.Cat[ui.CatLive]), true
	}
	return "", st, false
}

// statusLine is the summary, or a message in its place.
func (m model) statusLine() string {
	t := m.th
	if text, st, ok := m.message(); ok {
		return " " + st.Render(text)
	}
	if !m.loaded || m.err != nil || len(m.list) == 0 {
		return ""
	}
	worth := 0
	for _, d := range m.list {
		if m.category(d) == ui.CatGain {
			worth++
		}
	}
	s := i18n.TN("status.disks", len(m.list), len(m.list)) + " · " + i18n.TN("status.worth", worth, worth)
	if n := len(m.marked); n > 0 {
		s += " · " + ui.Fg(t.Mark).Render(i18n.TN("status.marked", n, n))
	}
	return " " + ui.Fg(t.Muted).Render(s)
}

// footer is the keys of the main screen.
func (m model) footer() string {
	if m.mode == modeConfirm {
		keys := [][2]string{{"⏎", i18n.T("key.shrink")}}
		if m.choosable(m.queueFor()) {
			keys = append(keys, [2]string{"c", i18n.T("key.chooseinside")})
		}
		return m.keyBar(append(keys, [2]string{"esc", i18n.T("key.cancel")}))
	}
	if m.mode == modeForget {
		return m.keyBar([][2]string{{"⏎", i18n.T("key.forget")}, {"esc", i18n.T("key.cancel")}})
	}
	// With no disk on screen, the keys that act on one do nothing.
	keys := [][2]string{{"r", i18n.T("key.reload")}, {"h", i18n.T("key.history")}, {"?", i18n.T("key.help")}, {"q", i18n.T("key.quit")}}
	if m.loaded && m.err == nil && len(m.list) > 0 {
		keys = [][2]string{
			{"↑↓", i18n.T("key.select")}, {"space", i18n.T("key.mark")}, {"s", m.shrinkKey()},
			{"c", i18n.T("key.clean")}, {"r", i18n.T("key.reload")}, {"h", i18n.T("key.history")},
			{"?", i18n.T("key.help")}, {"q", i18n.T("key.quit")},
		}
		// While a run is cut off, or another wsl-shrinker runs one, s only
		// says so: its key goes.
		if m.unfinishedState == journal.CutOff || m.unfinishedState == journal.Going {
			keys = slices.DeleteFunc(keys, func(k [2]string) bool { return k[0] == "s" })
		}
	}
	// A run cut off is finished first, whatever else is on screen: its key
	// leads, and never drops. Once R has failed to start things again, the
	// key that forgets the run follows it.
	if m.unfinishedState == journal.CutOff {
		lead := [][2]string{{"R", i18n.T("key.recover")}}
		if m.recoverFailed {
			lead = append(lead, [2]string{"F", i18n.T("key.forget")})
		}
		keys = append(lead, keys...)
	}
	return m.keyBar(keys)
}

// dropOrder is which keys leave the footer first when it does not fit; the
// help screen lists them all. A screen's main keys come last: space marks
// on the disks and chooses in the clean list, where a, n and esc go before
// it, as the way to choose shows nowhere else.
var dropOrder = []string{"↑↓", "r", "h", "a", "n", "esc", "space", "?", "c"}

// keyBar joins keycaps to fit the width, dropping the least needed first.
func (m model) keyBar(keys [][2]string) string {
	render := func(keys [][2]string) string {
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = m.th.KeyCap(k[0], k[1])
		}
		return " " + strings.Join(parts, "  ")
	}
	for _, drop := range dropOrder {
		if lipgloss.Width(render(keys)) <= m.width {
			break
		}
		for i, k := range keys {
			if k[0] == drop {
				keys = append(keys[:i:i], keys[i+1:]...)
				break
			}
		}
	}
	return render(keys)
}

// overlay floats a dialog over the screen, which is dimmed behind it.
func (m model) overlay(screen, box string) string {
	rows := strings.Split(screen, "\n")
	for i, r := range rows {
		rows[i] = ui.Fg(m.th.Rule).Render(ansi.Strip(r))
	}
	bw, bh := lipgloss.Width(box), lipgloss.Height(box)
	x := max(0, (m.width-bw)/2)
	y := max(0, (m.height-bh)/2)
	// A cell of space around the box, so nothing behind it touches its
	// border; where a few cells are all that is left beside it, those go
	// too, rather than show pieces of words. The keys at the bottom stay.
	cx, cw := max(0, x-1), bw+2
	if x <= 6 {
		cx, cw = 0, m.width
	}
	cy := max(0, y-1)
	end := max(y+bh, min(y+bh+1, m.height-2))
	space := strings.TrimSuffix(strings.Repeat(strings.Repeat(" ", min(cw, m.width-cx))+"\n", end-cy), "\n")
	out := lipgloss.NewCompositor(lipgloss.NewLayer(strings.Join(rows, "\n")),
		lipgloss.NewLayer(space).X(cx).Y(cy).Z(1), lipgloss.NewLayer(box).X(x).Y(y).Z(2)).Render()
	return m.frame(strings.Split(out, "\n"))
}

// box draws a dialog: an opaque panel with a rounded border.
func (m model) box(border ui.Category, lines ...string) string {
	w := min(70, m.width-4)
	// The text wraps once, at the width inside the border (2 cells) and the
	// padding (4): lipgloss's Width counts both, so a wider body would wrap
	// again and leave a word alone on a line.
	// A name, an amount and a command stay whole where the text wraps.
	held := make([]string, len(lines))
	for i, l := range lines {
		held[i] = hold(l, w-6)
	}
	body := lipgloss.NewStyle().Width(w - 6).Foreground(m.th.Ink).Render(strings.Join(held, "\n"))
	return unhold(lipgloss.NewStyle().BorderForeground(m.th.Cat[border]).Border(lipgloss.RoundedBorder()).Padding(0, 2).Width(w).Render(body))
}

// boxLine is one line of a dialog; drop says how early it goes when the
// dialog is taller than the screen, higher first (0: never).
type boxLine struct {
	s    string
	drop int
}

// fitBox draws a dialog no taller than the screen: while it is taller, the
// line with the highest drop goes, the last of them first.
func (m model) fitBox(border ui.Category, lines []boxLine) string {
	for {
		texts := make([]string, len(lines))
		worst, at := 0, -1
		for i, l := range lines {
			texts[i] = l.s
			if l.drop > 0 && l.drop >= worst {
				worst, at = l.drop, i
			}
		}
		box := m.box(border, texts...)
		if lipgloss.Height(box) <= m.height || at < 0 {
			return box
		}
		lines = append(lines[:at:at], lines[at+1:]...)
	}
}

// confirmBox asks before a run, saying what it will do.
func (m model) confirmBox() string {
	t := m.th
	q := m.queueFor()
	// Blank lines go first on a small screen, then the notes, least needed
	// first; the title, the sizes, what stops and the keys always stay.
	const blank, others, notes, list, clean = 5, 4, 3, 2, 1
	var lines []boxLine
	add := func(s string, drop int) { lines = append(lines, boxLine{s, drop}) }
	title := lipgloss.NewStyle().Bold(true).Foreground(t.Ink)
	if len(q) == 1 {
		add(title.Render(i18n.T("confirm.title", q[0].Name)), 0)
	} else {
		add(title.Render(i18n.T("confirm.title.n", len(q))), 0)
	}
	add("", blank)
	var gain, cleaned int64
	running, containers, known := 0, 0, true
	runningName := ""
	for _, d := range q {
		if d.Estimate != nil {
			gain += d.Estimate.Reclaim
		}
		for _, it := range m.chosenItems(d.Distro) {
			if d.Running {
				cleaned += it.Bytes
			}
		}
		if d.Running && d.Distro != "" {
			running++
			runningName = d.Name
			// Without a look inside, how many containers run is unknown.
			if s := m.surveys[d.Distro]; s != nil && !s.loading && s.err == nil {
				containers += s.r.Containers
			} else {
				known = false
			}
		}
		if len(q) > 1 && d.Estimate != nil {
			add(" • "+d.Name+ui.Fg(t.Muted).Render("  "+i18n.T("confirm.each", units.Bytes(d.Estimate.Reclaim))), list)
		}
	}
	if len(q) > 1 {
		add("", blank)
	}
	// What comes back is the estimate plus what is removed inside first:
	// both leave the file.
	if len(q) == 1 && q[0].Estimate != nil {
		e := q[0].Estimate
		add(ui.Fg(t.Cat[ui.CatGain]).Bold(true).Render(i18n.T("confirm.sizes", units.Bytes(e.FileSize), units.Bytes(max(0, e.After-cleaned)), units.Bytes(e.Reclaim+cleaned))), 0)
	} else {
		add(ui.Fg(t.Cat[ui.CatGain]).Bold(true).Render(i18n.T("confirm.total", units.Bytes(gain+cleaned))), 0)
	}
	if cleaned > 0 {
		add(i18n.T("confirm.clean", units.Bytes(cleaned)), clean)
	}
	// What stops: with several disks and one of them running, by its name,
	// since "it" would not say which.
	switch {
	case running == 0:
	case len(q) > 1 && running == 1 && !known:
		add(i18n.T("confirm.stops.of.unknown", runningName), 0)
	case len(q) > 1 && running == 1 && containers == 0:
		add(i18n.T("confirm.stops.of.none", runningName), 0)
	case len(q) > 1 && running == 1:
		add(i18n.TN("confirm.stops.of", containers, runningName, containers), 0)
	case !known:
		add(i18n.TN("confirm.stops.unknown", running), 0)
	case running > 1:
		add(i18n.T("confirm.stops.many", containers), 0)
	case containers == 0:
		add(i18n.T("confirm.stops.none"), 0)
	default:
		add(i18n.TN("confirm.stops", containers, containers), 0)
	}
	// A failure stops the disks after it: say so before several go.
	if len(q) > 1 {
		add(ui.Fg(t.Muted).Render(i18n.T("confirm.order")), notes)
	}
	// A running distribution with findings and none chosen: say c chooses,
	// once, before it is too late to.
	if m.choosable(q) && cleaned == 0 {
		var found int64
		for _, it := range m.surveys[q[0].Distro].r.Items {
			found += it.Bytes
		}
		add(ui.Fg(t.Muted).Render(i18n.T("confirm.nothing", units.Bytes(found))), notes)
	}
	add(ui.Fg(t.Muted).Render(i18n.T("confirm.others")), others)
	if m.hostsAny(q) {
		add("", blank)
		add(ui.Fg(t.Cat[ui.CatLive]).Render(i18n.T("confirm.window")), 0)
	}
	// The keys on one line: the screen behind is dimmed, so they are said
	// here. c, offered where there is something inside to choose from,
	// goes first when the box is narrow, then the space between keys.
	inner := min(70, m.width-4) - 6
	keys := []string{t.KeyCap("⏎", i18n.T("key.shrink"))}
	if m.choosable(q) {
		keys = append(keys, t.KeyCap("c", i18n.T("key.chooseinside")))
	}
	keys = append(keys, t.KeyCap("esc", i18n.T("key.cancel")))
	line := strings.Join(keys, "   ")
	if lipgloss.Width(line) > inner {
		line = strings.Join(keys, "  ")
	}
	if lipgloss.Width(line) > inner && len(keys) == 3 {
		line = keys[0] + "  " + keys[2]
	}
	add("", blank)
	add(line, 0)
	return m.fitBox(ui.CatGain, lines)
}

// forgetBox asks before a cut-off run is forgotten, saying what that run
// had stopped, which stays stopped, and how to start it by hand.
func (m model) forgetBox() string {
	t := m.th
	e := m.unfinished
	// On a small screen the blank lines go first, then the commands to type;
	// the title, what stays stopped and the keys always stay.
	const blank, byhand = 2, 1
	var lines []boxLine
	add := func(s string, drop int) { lines = append(lines, boxLine{s, drop}) }
	title := lipgloss.NewStyle().Bold(true).Foreground(t.Ink)
	if e.Disk == "" {
		add(title.Render(i18n.T("forget.title.unknown")), 0)
	} else {
		add(title.Render(i18n.T("forget.title", e.Disk)), 0)
	}
	add("", blank)
	// What the run had stopped: the distribution, and the containers a
	// recovery would have started (the ids docker start is given).
	stopped := e.Stopped && e.Distro != ""
	ids := shrink.Startable(e.Containers)
	switch {
	case e.Disk == "":
		// A journal that did not parse says nothing of what its run had
		// stopped: that is said, not guessed.
		add(i18n.T("forget.unknown"), 0)
	case stopped && len(ids) > 0:
		add(i18n.TN("forget.stopped", len(ids), e.Distro, len(ids)), 0)
	case stopped:
		add(i18n.T("forget.stopped.none", e.Distro), 0)
	default:
		add(i18n.T("forget.nothing"), 0)
	}
	if stopped {
		// The commands a recovery would have run, to type afterwards: one
		// line of the box, so on a small screen all of it drops, never half.
		// The box keeps each command whole where it wraps.
		start := "wsl -d " + ui.ShellArg(e.Distro)
		hand := i18n.T("forget.byhand", start)
		if len(ids) > 0 {
			hand = i18n.T("forget.byhand.docker", start, "docker start "+strings.Join(ids, " "))
		}
		add("", blank)
		add(ui.Fg(t.Muted).Render(hand), byhand)
	}
	// The width inside the box's border and padding.
	inner := min(70, m.width-4) - 6
	// The keys on one line, as in the question before a run: the screen
	// behind is dimmed, so they are said here.
	keys := []string{t.KeyCap("⏎", i18n.T("key.forget")), t.KeyCap("esc", i18n.T("key.cancel"))}
	line := strings.Join(keys, "   ")
	if lipgloss.Width(line) > inner {
		line = strings.Join(keys, "  ")
	}
	add("", blank)
	add(line, 0)
	return m.fitBox(ui.CatWarn, lines)
}

// hostsAny reports whether a run would stop the distribution this
// program's terminal runs in.
func (m model) hostsAny(q []disks.Disk) bool {
	for _, d := range q {
		if d.Running && d.Distro != "" && m.b.Hosts(d.Distro) {
			return true
		}
	}
	return false
}

// wrap breaks a sentence into lines that fit the screen, each starting
// with indent, in style st.
func (m model) wrap(s, indent string, st lipgloss.Style) []string {
	return m.wrapTo(s, m.width, indent, st)
}

// amountRe matches an amount and its unit ("67.4 GB"), which a line must not
// part.
var amountRe = regexp.MustCompile(`(\d) (B|KB|MB|GB|TB)\b`)

// commandRe matches a command with what it acts on ("wsl -d NAME", "wsl
// --shutdown", "docker start ID ID"), which a line must not part either:
// it is read, and typed, as one. The ids are hex digits, all docker start
// is given. A mark written against it, as a quote, is measured with it.
var commandRe = regexp.MustCompile(`\S*(wsl -d \S+|wsl --\S+|docker start( [0-9a-f]+)+)`)

// wordRe matches a word: what stands between spaces.
var wordRe = regexp.MustCompile(`\S+`)

// The marks hold leaves where a line must not break, which unhold takes
// out again: a no-break space, and a no-break hyphen, one cell each as the
// characters they stand for.
const (
	holdSpace  = "\u00a0"
	holdHyphen = "\u2011"
)

// hold marks where s must not break when it wraps in a column w cells
// wide. A no-break space joins an amount to its unit and a command's
// words. A no-break hyphen stands for every hyphen of a word, since the
// wrapping breaks after a hyphen as after a space: "Ubuntu-" on one line
// and "24.04" on the next, or "wsl --" and "shutdown". What is wider than
// the column is left as it is: it has to break somewhere, and breaks best
// at its spaces and hyphens.
func hold(s string, w int) string {
	s = amountRe.ReplaceAllString(s, "$1"+holdSpace+"$2")
	s = commandRe.ReplaceAllStringFunc(s, func(c string) string {
		if lipgloss.Width(c) <= w {
			return strings.ReplaceAll(c, " ", holdSpace)
		}
		// Wider than the column, as docker start with many ids is: its
		// first three words stay together where they fit, so a line never
		// ends in "docker" or "docker start", and the rest breaks between
		// ids.
		words := strings.SplitN(c, " ", 4)
		if len(words) < 4 {
			return c
		}
		head := strings.Join(words[:3], holdSpace)
		if lipgloss.Width(head) > w {
			// Narrower still: "docker start" at least stays one.
			return words[0] + holdSpace + words[1] + " " + strings.Join(words[2:], " ")
		}
		return head + " " + words[3]
	})
	return wordRe.ReplaceAllStringFunc(s, func(word string) string {
		if lipgloss.Width(word) > w {
			return word
		}
		return strings.ReplaceAll(word, "-", holdHyphen)
	})
}

// unhold puts back the spaces and hyphens hold marked.
func unhold(s string) string {
	return strings.NewReplacer(holdSpace, " ", holdHyphen, "-").Replace(s)
}

// wrapTo is wrap for a column w cells wide, every line after indent. A line
// already styled keeps its colors.
func (m model) wrapTo(s string, width int, indent string, st lipgloss.Style) []string {
	return m.wrapHang(s, width, indent, indent, st)
}

// wrapHang is wrapTo with the first line after first and the lines after it
// after rest: a check's continuation starts under its words, not its mark.
func (m model) wrapHang(s string, width int, first, rest string, st lipgloss.Style) []string {
	w := max(10, width-max(lipgloss.Width(first), lipgloss.Width(rest))-1)
	// An amount, a command and a name stay whole while the text wraps.
	var out []string
	for l := range strings.SplitSeq(lipgloss.NewStyle().Width(w).Render(hold(s, w)), "\n") {
		l = unhold(strings.TrimRight(l, " "))
		lead := rest
		if len(out) == 0 {
			lead = first
		}
		out = append(out, lead+st.Render(l))
	}
	return out
}

// sinceRe matches Docker's "5 weeks ago" and its few fixed phrases.
var sinceRe = regexp.MustCompile(`^(\d+|About an?|Less than a) (second|minute|hour|day|week|month|year)s? ago$`)

// since says Docker's "how long ago" in the language in use. Docker prints
// it in English whatever the language; an unknown phrase stays as it is.
func since(s string) string {
	m := sinceRe.FindStringSubmatch(s)
	if m == nil || i18n.Current() == i18n.English {
		return s
	}
	n := m[1]
	switch n {
	case "About a", "About an":
		n = "1"
	case "Less than a":
		n = "0"
	}
	return i18n.T("since."+m[2], n)
}

// oneLine joins an error's lines.
func oneLine(s string) string {
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' }), " · ")
}

// elapsed formats a duration as 1m 05s or 12s.
func elapsed(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return i18n.T("time.s", int(d.Seconds()))
	}
	return i18n.T("time.ms", int(d.Minutes()), int(d.Seconds())%60)
}

// notEnabled reports whether an error says WSL itself is not turned on.
func notEnabled(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "has not been enabled") || strings.Contains(s, "optional_component") ||
		strings.Contains(s, "wsl.exe\": executable file not found")
}

// noAnswer reports whether err is WSL not answering in time. From inside
// WSL the error is wsl-shrinker.exe's words, so they are matched too.
func noAnswer(err error) bool {
	return errors.Is(err, wsl.ErrNoAnswer) || strings.Contains(err.Error(), wsl.ErrNoAnswer.Error())
}

// shrinkKey says what s shrinks: the marked disks, by count, when there are.
func (m model) shrinkKey() string {
	if n := len(m.marked); n > 0 {
		return i18n.T("key.shrink.marked", n)
	}
	return i18n.T("key.shrink")
}
