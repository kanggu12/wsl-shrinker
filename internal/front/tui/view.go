package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/kanggu12/wsl-shrinker/internal/core/units"
	"github.com/kanggu12/wsl-shrinker/internal/engine/disks"
	"github.com/kanggu12/wsl-shrinker/internal/engine/survey"
	"github.com/kanggu12/wsl-shrinker/internal/front/i18n"
	"github.com/kanggu12/wsl-shrinker/internal/front/ui"
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
	v := tea.NewView(body)
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
	left := t.Chip("wsl-shrinker")
	if m.info.Version != "" {
		left += ui.Fg(t.Muted).Render("  WSL " + m.info.Version)
	}
	if m.loading {
		left += ui.Fg(t.Muted).Render(" ↻")
	}
	var right []string
	if m.loaded && m.err == nil {
		total := int64(0)
		for _, d := range m.list {
			if d.Estimate != nil && !m.b.Protected(d.Name) {
				total += d.Estimate.Reclaim
			}
		}
		right = append(right, ui.Fg(t.Cat[ui.CatGain]).Render("● "+i18n.T("header.total", units.Bytes(total))))
		if free := m.driveFree(); free != "" {
			right = append(right, ui.Fg(t.Muted).Render(free))
		}
	}
	r := strings.Join(right, ui.Fg(t.Rule).Render(" · "))
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(r) - 2
	if gap < 2 {
		return " " + left
	}
	return " " + left + strings.Repeat(" ", gap) + r + " "
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
	t := m.th
	h := m.bodyHeight()
	var body []string
	switch {
	case !m.loaded:
		body = m.centered(h, ui.Fg(t.Muted).Render(i18n.T("loading")))
	case m.err != nil:
		body = m.centered(h,
			ui.Fg(t.Cat[ui.CatBad]).Render(ansi.Truncate(i18n.T("error.list", oneLine(m.err.Error())), m.width-4, "…")),
			ui.Fg(t.Muted).Render(i18n.T("error.retry")))
	case len(m.list) == 0:
		body = m.centered(h,
			lipgloss.NewStyle().Bold(true).Foreground(t.Ink).Render(i18n.T("empty.title")),
			ui.Fg(t.Muted).Render(i18n.T("empty.hint")))
	case m.width >= sideBySide:
		// Table and recent runs on the left, details on the right.
		lw := m.width * 56 / 100
		left := append(m.table(lw), "")
		left = append(left, m.recent(lw, h-len(left))...)
		right := m.details(m.width - lw - 3)
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
		// Narrow: the table, then as much of the details as fits.
		body = append(m.table(m.width), "")
		body = append(body, m.details(m.width-2)...)
	}
	screen := m.page(body, m.statusLine(), m.footer())
	if m.mode == modeConfirm {
		return m.overlay(screen, m.confirmBox())
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

// category says what a disk's color means.
func (m model) category(d disks.Disk) ui.Category {
	switch {
	case m.b.Protected(d.Name):
		return ui.CatSafe
	case d.Estimate == nil || d.File.Encrypted:
		return ui.CatBad
	case d.Estimate.Reclaim >= m.b.MinGain():
		return ui.CatGain
	}
	return ui.CatSmall
}

// table is the disks, one row each, w cells wide.
func (m model) table(w int) []string {
	t := m.th
	// Fixed columns; the name takes what is left.
	// The state column fits its longest word in the language in use.
	stateW := 0
	for _, k := range []string{"col.state", "state.running", "state.stopped", "state.inuse", "state.file"} {
		stateW = max(stateW, lipgloss.Width(i18n.T(k)))
	}
	const sizeW, barW = 8, 6
	nameW := min(28, max(10, w-4-stateW-3*sizeW-barW-10))
	head := "    " + ui.Fit(i18n.T("col.disk"), nameW) + "  " + ui.Fit(i18n.T("col.state"), stateW) + "  " +
		ui.FitRight(i18n.T("col.file"), sizeW) + "  " + ui.FitRight(i18n.T("col.after"), sizeW) + "  " +
		ui.FitRight(i18n.T("col.frees"), sizeW)
	lines := []string{lipgloss.NewStyle().Foreground(t.Muted).Bold(true).Render(head)}
	// The bars share one scale: the disk that gives back most fills its bar.
	var most int64
	for _, d := range m.list {
		if d.Estimate != nil {
			most = max(most, d.Estimate.Reclaim)
		}
	}
	for i, d := range m.list {
		cat := m.category(d)
		mark := " "
		if m.marked[d.Name] {
			mark = ui.Fg(t.Mark).Render("✓")
		}
		file, after, frees := "—", "—", "—"
		bar := strings.Repeat(" ", barW)
		if e := d.Estimate; e != nil {
			file, after, frees = units.Bytes(e.FileSize), units.Bytes(e.After), units.Bytes(e.Reclaim)
			bar = t.Bar(barW, most, ui.Part{Value: e.Reclaim, Color: t.Cat[cat]})
		}
		name := d.Name
		row := " " + mark + t.Dot(cat) + " " + ui.Fit(name, nameW) + "  " + ui.Fit(m.stateWord(d), stateW) + "  " +
			ui.FitRight(file, sizeW) + "  " + ui.FitRight(after, sizeW) + "  " +
			ui.FitRight(ui.Fg(t.Cat[cat]).Render(frees), sizeW) + "  " + bar
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
	case d.Kind == disks.KindDocker && d.File.Held:
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
			what = ui.Fg(t.Cat[ui.CatDone]).Render(i18n.T("recent.freed", units.Bytes(e.Freed))) + " " + e.Disk +
				ui.Fg(t.Muted).Render(" · "+units.Bytes(e.Before)+" → "+units.Bytes(e.After))
		}
		lines = append(lines, ui.Fit(" "+when+"  "+what, w))
	}
	return lines
}

// details is the selected disk: what it gives back, its checks, what fills
// it, and where it is.
func (m model) details(w int) []string {
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
	switch {
	case m.b.Protected(d.Name):
		verdict = i18n.T("verdict.protected")
	case d.Estimate == nil:
		verdict = i18n.T("verdict.unreadable")
	case cat == ui.CatGain:
		verdict = i18n.T("verdict.gain", units.Bytes(d.Estimate.Reclaim))
	default:
		verdict = i18n.T("verdict.small", units.Bytes(d.Estimate.Reclaim))
	}
	lines = append(lines, ui.Fg(t.Cat[cat]).Bold(true).Render(verdict), "")
	if e := d.Estimate; e != nil {
		// One bar for the file: what stays, and what a shrink gives back.
		lines = append(lines, section(i18n.T("details.space")))
		bw := min(w-2, 44)
		lines = append(lines, " "+t.Bar(bw, e.FileSize, ui.Part{Value: e.After, Color: t.Cat[ui.CatLive]}, ui.Part{Value: e.Reclaim, Color: t.Cat[ui.CatGain]}))
		lines = append(lines, " "+ui.Fg(t.Cat[ui.CatLive]).Render("●")+" "+i18n.T("details.stays", units.Bytes(e.After))+"   "+
			ui.Fg(t.Cat[ui.CatGain]).Render("●")+" "+i18n.T("details.goes", units.Bytes(e.Reclaim)))
		if e.Filesystem {
			lines = append(lines, ui.Fg(t.Muted).Render(" "+i18n.T("details.used", units.Bytes(e.Used))))
		}
		lines = append(lines, "")
	}
	// The checks a run makes first, as they stand now.
	lines = append(lines, section(i18n.T("details.checks")))
	for _, c := range m.checks(d) {
		lines = append(lines, " "+c)
	}
	lines = append(lines, "")
	// The inside of a running distribution.
	if d.Distro != "" {
		lines = append(lines, section(i18n.T("details.inside")))
		lines = append(lines, m.inside(d, w)...)
		lines = append(lines, "")
	}
	lines = append(lines, ui.Fg(t.Muted).Render(ui.Middle(d.Path, w)))
	for i := range lines {
		lines[i] = ui.Fit(lines[i], w)
	}
	return lines
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
	note := func(s string) string { return ui.Fg(t.Cat[ui.CatGain]).Render("!") + " " + s }
	var out []string
	if d.Estimate == nil {
		return []string{bad(i18n.T("check.unreadable", oneLine(d.Err)))}
	}
	if d.Distro != "" {
		if m.info.CanCompact {
			out = append(out, ok(i18n.T("check.compact", m.info.Version)))
		} else if m.info.Version != "" {
			out = append(out, bad(i18n.T("check.nocompact")))
		}
	} else if d.File.Held {
		out = append(out, bad(i18n.T("check.held")))
	} else {
		out = append(out, ok(i18n.T("check.free")))
	}
	if d.File.Encrypted {
		out = append(out, bad(i18n.T("check.encrypted")))
	}
	if d.File.Sparse {
		out = append(out, note(i18n.T("check.sparse")))
	}
	switch {
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
func (m model) inside(d disks.Disk, w int) []string {
	t := m.th
	if !d.Running {
		return []string{ui.Fg(t.Muted).Render(" " + i18n.T("inside.stopped"))}
	}
	s := m.surveys[d.Distro]
	switch {
	case s == nil || s.loading:
		return []string{ui.Fg(t.Muted).Render(" " + spinner[m.spin%len(spinner)] + " " + i18n.T("inside.loading"))}
	case s.err != nil:
		return []string{ui.Fg(t.Cat[ui.CatBad]).Render(" " + i18n.T("inside.failed", oneLine(s.err.Error())))}
	case len(s.r.Items) == 0:
		return []string{ui.Fg(t.Muted).Render(" " + i18n.T("inside.nothing"))}
	}
	var out []string
	var total int64
	for _, it := range s.r.Items {
		total += it.Bytes
	}
	for i, it := range s.r.Items {
		if i == 5 {
			out = append(out, ui.Fg(t.Muted).Render(" "+i18n.T("inside.more", len(s.r.Items)-5)))
			break
		}
		check := " "
		if m.picked[pickKey(d.Distro, it.ID)] {
			check = ui.Fg(t.Mark).Render("✓")
		}
		out = append(out, " "+check+ui.FitRight(units.Bytes(it.Bytes), 8)+"  "+m.kindTag(it.Kind)+"  "+ui.Fit(itemLabel(it), w-14-kindWidth()))
	}
	out = append(out, ui.Fg(t.Muted).Render(" "+i18n.T("inside.hint", units.Bytes(total))))
	return out
}

// kindTag is a finding's kind as a short colored word of fixed width.
func (m model) kindTag(k survey.Kind) string {
	t := m.th
	c := map[survey.Kind]ui.Category{survey.KindCache: ui.CatLive, survey.KindDownload: ui.CatSafe, survey.KindLog: ui.CatSmall, survey.KindImage: ui.CatGain}[k]
	return ui.Fg(t.Cat[c]).Render(ui.Fit(i18n.T("kind."+string(k)), kindWidth()))
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
	return name + " · " + owner
}

// statusLine is the summary, or a passing message.
func (m model) statusLine() string {
	t := m.th
	if m.toast != "" && time.Now().Before(m.toastUntil) {
		return " " + ui.Fg(t.Cat[m.toastCat]).Render(m.toast)
	}
	if !m.loaded || m.err != nil {
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
		return m.keyBar([][2]string{{"⏎", i18n.T("key.shrink")}, {"esc", i18n.T("key.cancel")}})
	}
	return m.keyBar([][2]string{
		{"↑↓", i18n.T("key.select")}, {"space", i18n.T("key.mark")}, {"s", i18n.T("key.shrink")},
		{"c", i18n.T("key.clean")}, {"r", i18n.T("key.reload")}, {"h", i18n.T("key.history")},
		{"?", i18n.T("key.help")}, {"q", i18n.T("key.quit")},
	})
}

// dropOrder is which keys leave the footer first when it does not fit; the
// help screen lists them all.
var dropOrder = []string{"↑↓", "r", "h", "space", "?", "c"}

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
	x := max(0, (m.width-lipgloss.Width(box))/2)
	y := max(0, (m.height-lipgloss.Height(box))/2)
	out := lipgloss.NewCompositor(lipgloss.NewLayer(strings.Join(rows, "\n")), lipgloss.NewLayer(box).X(x).Y(y).Z(1)).Render()
	return m.frame(strings.Split(out, "\n"))
}

// box draws a dialog: an opaque panel with a rounded border.
func (m model) box(border ui.Category, lines ...string) string {
	w := min(70, m.width-4)
	body := lipgloss.NewStyle().Width(w - 4).Foreground(m.th.Ink).Render(strings.Join(lines, "\n"))
	return lipgloss.NewStyle().BorderForeground(m.th.Cat[border]).Border(lipgloss.RoundedBorder()).Padding(0, 2).Width(w).Render(body)
}

// confirmBox asks before a run, saying what it will do.
func (m model) confirmBox() string {
	t := m.th
	q := m.queueFor()
	var lines []string
	title := lipgloss.NewStyle().Bold(true).Foreground(t.Ink)
	if len(q) == 1 {
		lines = append(lines, title.Render(i18n.T("confirm.title", q[0].Name)), "")
	} else {
		lines = append(lines, title.Render(i18n.T("confirm.title.n", len(q))), "")
	}
	var gain, cleaned int64
	running, containers := false, 0
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
			running = true
			if s := m.surveys[d.Distro]; s != nil {
				containers += s.r.Containers
			}
		}
		if len(q) > 1 && d.Estimate != nil {
			lines = append(lines, " • "+d.Name+ui.Fg(t.Muted).Render("  "+i18n.T("confirm.each", units.Bytes(d.Estimate.Reclaim))))
		}
	}
	if len(q) > 1 {
		lines = append(lines, "")
	}
	if len(q) == 1 && q[0].Estimate != nil {
		e := q[0].Estimate
		lines = append(lines, ui.Fg(t.Cat[ui.CatGain]).Bold(true).Render(i18n.T("confirm.sizes", units.Bytes(e.FileSize), units.Bytes(e.After), units.Bytes(e.Reclaim))))
	} else {
		lines = append(lines, ui.Fg(t.Cat[ui.CatGain]).Bold(true).Render(i18n.T("confirm.total", units.Bytes(gain))))
	}
	if cleaned > 0 {
		lines = append(lines, i18n.T("confirm.clean", units.Bytes(cleaned)))
	}
	switch {
	case running && len(q) > 1:
		lines = append(lines, i18n.T("confirm.stops.many", containers))
	case running && containers == 0:
		lines = append(lines, i18n.T("confirm.stops.none"))
	case running:
		lines = append(lines, i18n.TN("confirm.stops", containers, containers))
	}
	// A failure stops the disks after it: say so before several go.
	if len(q) > 1 {
		lines = append(lines, ui.Fg(t.Muted).Render(i18n.T("confirm.order")))
	}
	lines = append(lines, ui.Fg(t.Muted).Render(i18n.T("confirm.others")))
	if m.hostsAny(q) {
		lines = append(lines, "", ui.Fg(t.Cat[ui.CatLive]).Render(i18n.T("confirm.window")))
	}
	lines = append(lines, "", t.KeyCap("⏎", i18n.T("key.shrink"))+"   "+t.KeyCap("esc", i18n.T("key.cancel")))
	return m.box(ui.CatGain, lines...)
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

// oneLine joins an error's lines.
func oneLine(s string) string {
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' }), " · ")
}

// elapsed formats a duration as 1m 05s or 12s.
func elapsed(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm %02ds", int(d.Minutes()), int(d.Seconds())%60)
}
