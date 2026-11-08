package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/kanggu12/wsl-shrinker/internal/core/units"
	"github.com/kanggu12/wsl-shrinker/internal/engine/disks"
	"github.com/kanggu12/wsl-shrinker/internal/engine/shrink"
	"github.com/kanggu12/wsl-shrinker/internal/engine/survey"
	"github.com/kanggu12/wsl-shrinker/internal/front/i18n"
	"github.com/kanggu12/wsl-shrinker/internal/front/ui"
)

// runView follows a run: the steps of the disk running, its sizes before and
// after, and at the end what every disk gave back.
func (m model) runView() string {
	t := m.th
	r := m.run
	bold := lipgloss.NewStyle().Bold(true).Foreground(t.Ink)
	d := r.queue[r.at]
	multi := len(r.queue) > 1
	res := m.resultFor(d.Name)
	failed := r.done && res != nil && res.Err != ""
	// A failure is about its disk; a run of several, about all of them.
	var title string
	switch {
	case failed && res.Compacted:
		title = i18n.T("run.title.problem", d.Name)
	case failed:
		title = i18n.T("run.title.failed", d.Name)
	case multi && r.done:
		title = i18n.T("run.title.done.n", len(r.queue))
	case multi:
		title = i18n.T("run.title.n", len(r.queue))
	case r.done:
		title = i18n.T("run.title.done", d.Name)
	default:
		title = i18n.T("run.title", d.Name)
	}
	// Where the run is: which disk of several, and for how long.
	var where []string
	if multi {
		where = append(where, i18n.T("run.of", r.at+1, len(r.queue)))
	}
	if !r.done {
		where = append(where, elapsed(m.now.Sub(r.started)))
	}
	after := ui.Fg(t.Muted).Render("  " + strings.Join(where, "  "))
	var body []string
	switch {
	case multi && !failed:
		// The steps below are one disk's: a line above them names it.
		body = append(body, " "+bold.Render(title), "", " "+bold.Render(d.Name)+after)
	case len(where) > 0:
		body = append(body, " "+bold.Render(title)+after, "")
	default:
		body = append(body, " "+bold.Render(title), "")
	}
	// One row per step: its mark, its name, what it found or did.
	for _, s := range shrink.Steps {
		e, seen := r.steps[s]
		mark, color := "·", t.Muted
		detail := ""
		switch {
		case !seen:
			// Not reached yet.
		case e.Status == shrink.Running:
			mark, color = spinner[m.spin%len(spinner)], t.Cat[ui.CatLive]
			detail = elapsed(m.now.Sub(r.stepStart))
			if s == shrink.StepCompact {
				detail += "  " + i18n.T("run.compacting")
			}
		case e.Status == shrink.Done:
			mark, color, detail = "✓", t.Cat[ui.CatDone], stepDetail(e)
		case e.Status == shrink.Skipped && s == shrink.StepClean && e.Count > 0:
			// Items were chosen, but the distribution no longer ran.
			mark, color, detail = "–", t.Muted, i18n.T("run.skipped.clean.stopped")
		case e.Status == shrink.Skipped && s == shrink.StepRestart && r.steps[shrink.StepVerify].Status == shrink.Failed:
			// Not started again on purpose, after a disk that did not read
			// as before: in amber, as something to act on.
			mark, color, detail = "–", t.Cat[ui.CatWarn], i18n.T("run.skipped.restart.left")
		case e.Status == shrink.Skipped:
			mark, color, detail = "–", t.Muted, i18n.T("run.skipped."+string(s))
		case e.Status == shrink.Warned:
			mark, color, detail = "!", t.Cat[ui.CatWarn], stepDetail(e)
		case e.Status == shrink.Failed:
			mark, color, detail = "✗", t.Cat[ui.CatBad], m.failWords(e, d)
		}
		name := ui.Fit(i18n.T("step."+string(s)), 14)
		if seen {
			name = lipgloss.NewStyle().Foreground(t.Ink).Render(name)
		} else {
			name = ui.Fg(t.Muted).Render(name)
		}
		lead := "   " + ui.Fg(color).Render(mark) + "  " + name + "  "
		// A failure says why in full: its lines wrap under the detail column,
		// 22 cells in, up to a few of them.
		if seen && e.Status == shrink.Failed {
			wrapped := m.wrapTo(detail, m.width-22, "", ui.Fg(t.Muted))
			wrapped = wrapped[:min(len(wrapped), 6)]
			body = append(body, lead+wrapped[0])
			for _, l := range wrapped[1:] {
				body = append(body, strings.Repeat(" ", 22)+l)
			}
			continue
		}
		body = append(body, lead+ui.Fg(t.Muted).Render(ui.Fit(detail, m.width-26)))
	}
	// What the running step does, in a sentence.
	if !r.done {
		for _, s := range shrink.Steps {
			if e, ok := r.steps[s]; ok && e.Status == shrink.Running {
				body = append(body, "")
				about := i18n.T("run.about." + string(s))
				if s == shrink.StepCompact && d.Distro == "" {
					// A file WSL does not register: Windows compacts it, with
					// no distribution to stop and no trim.
					about = i18n.T("run.about.compact.file")
				}
				body = append(body, m.wrap(about, "   ", ui.Fg(t.Muted))...)
			}
		}
	}
	body = append(body, "")
	// Before and after, on one scale, as the block map draws a disk: before,
	// the data that stays in slate and what goes back in teal; after, the
	// data alone, the expected size until the measured one is known.
	var bars []string
	if e := d.Estimate; e != nil {
		// What the chosen items free comes off the data that stays, as the
		// question before the run counted it.
		var cleaned int64
		if m.run != nil && d.Running {
			for _, it := range m.run.clean[d.Name] {
				cleaned += it.Bytes
			}
		}
		stays := max(0, e.After-cleaned)
		bw := min(m.width-30, 60)
		before := t.Bar(bw, e.FileSize, ui.Part{Value: stays, Color: t.Stays}, ui.Part{Value: e.FileSize - stays, Color: t.Cat[ui.CatGain]})
		afterSize, label := stays, i18n.T("run.expected")
		if res := m.resultFor(d.Name); res != nil && res.File != nil {
			afterSize, label = res.File.OnDisk, i18n.T("run.after")
		}
		after := t.Bar(bw, e.FileSize, ui.Part{Value: afterSize, Color: t.Stays})
		bars = []string{
			"   " + ui.Fit(i18n.T("run.before"), 10) + before + "  " + units.Bytes(e.FileSize),
			"   " + ui.Fit(label, 10) + after + "  " + units.Bytes(afterSize), ""}
	}
	// The outcome, once there is one, in the rows the steps left. What the
	// run came to matters more than the bars: in a window too short for all
	// of it, the bars go first.
	if r.done {
		room := m.bodyHeight() - len(body)
		out := m.outcome(room - len(bars))
		if all := m.outcome(room); len(bars) > 0 && (len(all) > len(out) || len(out) > room-len(bars)) {
			bars, out = nil, all
		}
		body = append(append(body, bars...), out...)
	} else {
		body = append(body, bars...)
	}
	keys := m.keyBar([][2]string{{"⏎", i18n.T("key.back")}, {"q", i18n.T("key.quit")}})
	status := " " + ui.Fg(t.Muted).Render(i18n.T("run.wait"))
	if r.done {
		status = ""
	} else {
		keys = " " + ui.Fg(t.Muted).Render(i18n.T("run.nokeys"))
	}
	return m.page(body, status, keys)
}

// failWords says why a step failed: a refused check in the screen's own
// language, from the code the engine gave it; anything else in the
// engine's words, which are WSL's or Windows' own.
func (m model) failWords(e shrink.Event, d disks.Disk) string {
	switch e.Code {
	case string(shrink.BlockProtected):
		return i18n.T("toast.protected", d.Name)
	case string(shrink.BlockUnreadable):
		return i18n.T("toast.unreadable", d.Name)
	case string(shrink.BlockEncrypted):
		return i18n.T("toast.encrypted", d.Name)
	case string(shrink.BlockFSErrors):
		return i18n.T("toast.fserrors", d.Name)
	case string(shrink.BlockHeld):
		return i18n.T(heldKey(d, "toast.held"), d.Name)
	case string(shrink.BlockNoCompact):
		return i18n.T("toast.nocompact")
	case shrink.CodeNoDistro:
		return i18n.T("check.fail.nodistro", d.Name)
	case shrink.CodeBusy:
		return i18n.T("check.fail.busy")
	case shrink.CodeWSL:
		// WSL's own message says what went wrong: it follows.
		return i18n.T("check.fail.wsl") + " (" + e.Detail + ")"
	}
	return e.Detail
}

// stepDetail says what a finished step found or did, in the language in
// use, from the event's numbers.
func stepDetail(e shrink.Event) string {
	switch e.Step {
	case shrink.StepCheck:
		return i18n.T("detail.check", units.Bytes(e.Bytes))
	case shrink.StepClean:
		if e.Status == shrink.Warned {
			return i18n.T("detail.clean.some", e.Count)
		}
		return i18n.TN("detail.clean", e.Count, e.Count, units.Bytes(e.Bytes))
	case shrink.StepStop:
		s := i18n.TN("detail.stop", e.Count, e.Count)
		if e.Hooks > 0 {
			s += ", " + i18n.TN("detail.hooks", e.Hooks, e.Hooks)
		}
		if e.Status == shrink.Warned {
			s += ", " + i18n.T("detail.hooks.failed")
		}
		return s
	case shrink.StepCompact:
		return elapsed(e.Took)
	case shrink.StepVerify:
		return i18n.T("detail.verify", units.Bytes(e.Bytes))
	case shrink.StepRestart:
		s := i18n.T("detail.running")
		if e.SecondStart {
			s = i18n.T("detail.second")
		}
		switch {
		case e.NotRestarted > 0:
			s += ", " + i18n.TN("detail.notstarted", e.NotRestarted, e.NotRestarted)
		case e.Count > 0:
			s += ", " + i18n.TN("detail.started", e.Count, e.Count)
		}
		return s
	}
	return e.Detail
}

// resultFor is the finished result of a disk in this run, if any.
func (m model) resultFor(name string) *shrink.Result {
	for i := range m.run.results {
		if m.run.results[i].Disk == name {
			return &m.run.results[i]
		}
	}
	return nil
}

// outcome sums up a finished run in at most room rows.
func (m model) outcome(room int) (lines []string) {
	t := m.th
	// Where the note on restarted containers goes: under the line that sums
	// the run up.
	at := 0
	defer func() {
		// The history is the record a bug report starts from: a run it
		// could not keep says so, under whatever else is said.
		for _, res := range m.run.results {
			if n := res.Notes["history"]; n != "" {
				lines = append(lines, m.wrap(i18n.T("run.nohistory", oneLine(n)), " ", ui.Fg(t.Cat[ui.CatWarn]))...)
				break
			}
		}
		// The note goes in whole or not at all: in a window too short for
		// it, a sentence cut in the middle would say less than none.
		if notes := m.idleNotes(); len(notes) > 0 && len(lines)+len(notes) <= room {
			lines = slices.Insert(lines, at, notes...)
		}
	}()
	// What the disks that went through gave back; a disk that failed says
	// what happened to it on its own.
	var freed int64
	var took time.Duration
	var failed *shrink.Result
	for i, res := range m.run.results {
		took += res.Took
		if res.Err != "" {
			failed = &m.run.results[i]
		} else {
			freed += res.Freed
		}
	}
	if failed != nil {
		// Where things stand now matters more than the error, which the
		// step's line already shows: whether the disk changed, and whether
		// the distribution runs.
		// The failed disk is the last one run: its steps are on screen.
		state := m.failureState(failed)
		// The title says it was not shrunk; here is what that leaves.
		lines = append(m.perDisk(), m.wrap(state, " ", ui.Fg(t.Ink))...)
		at = len(lines)
		lines = append(lines, " "+ui.Fg(t.Muted).Render(i18n.T("run.next")))
		if freed > 0 {
			lines = append(lines, " "+i18n.T("run.partial", units.Bytes(freed)))
		}
		return lines
	}
	lines = append(m.perDisk(), " "+ui.Fg(t.Cat[ui.CatDone]).Bold(true).Render(i18n.T("run.freed", units.Bytes(freed), elapsed(took))))
	at = len(lines)
	// A step that warned is worth a second look.
	for _, res := range m.run.results {
		for _, e := range res.Events {
			if e.Status == shrink.Warned {
				return append(lines, " "+ui.Fg(t.Cat[ui.CatWarn]).Render(i18n.T("run.warned")))
			}
		}
	}
	return lines
}

// idleNotes says, under a run, how long the containers it started again
// run: as long as WSL keeps their distribution running, which it stops
// once nothing from Windows runs in it (shrink.IdleNote). One note for
// each disk whose run started containers again, in the muted color.
func (m model) idleNotes() (lines []string) {
	for _, res := range m.run.results {
		if res.Left == shrink.LeftRunning && shrink.Idle(res.DistroStarted, res.ContainersStarted) {
			lines = append(lines, m.wrap(i18n.T("run.idle", res.Disk), " ", ui.Fg(m.th.Muted))...)
		}
	}
	return lines
}

// stateEnds is the ending failureState adds to its step's key for each way
// the engine can leave a distribution. It is a table so the catalog test
// can list every key failureState may build.
var stateEnds = map[shrink.Left]string{
	shrink.LeftRunning: ".running",
	shrink.LeftDown:    ".down",
	shrink.LeftSuspect: ".suspect",
	// Not running before, or a file with nothing to run: both stay so.
	shrink.LeftStopped: ".stopped",
	shrink.LeftFile:    ".stopped",
}

// stateWarned is the ending of a distribution running again with some of
// its containers not started.
const stateWarned = ".warned"

// failureState says where a failed run left its disk, from what the engine
// reports: the step that failed, and how the distribution was left. A
// combination with no words of its own says what the step alone says.
func (m model) failureState(res *shrink.Result) string {
	at := res.FailedAt
	if at == "" {
		at = shrink.StepCheck
	}
	base := "run.state." + string(at)
	// The ending for how the distribution was left; none for a Left the
	// table does not know.
	end := stateEnds[res.Left]
	if res.Left == shrink.LeftRunning && res.ContainersNotStarted > 0 {
		// Running again, but not with all of its containers.
		end = stateWarned
	}
	key := base + end
	if !i18n.Has(i18n.Current(), key) {
		key = base
	}
	state := i18n.T(key)
	if strings.Contains(state, "%") {
		state = fmt.Sprintf(state, res.Disk)
	}
	return state
}

// perDisk is one line per disk of a run of several: the steps on screen are
// the last disk's, so the others' outcomes are said here.
func (m model) perDisk() []string {
	t := m.th
	if len(m.run.queue) < 2 {
		return nil
	}
	var lines []string
	for _, res := range m.run.results {
		if res.Err != "" {
			lines = append(lines, "   "+ui.Fg(t.Cat[ui.CatBad]).Render("✗")+"  "+ui.Fit(res.Disk, 22)+ui.Fg(t.Muted).Render(i18n.T("run.disk.failed")))
			continue
		}
		var before, after int64
		if res.Before != nil {
			before = res.Before.FileSize
		}
		if res.File != nil {
			after = res.File.OnDisk
		}
		lines = append(lines, "   "+ui.Fg(t.Cat[ui.CatDone]).Render("✓")+"  "+ui.Fit(res.Disk, 22)+
			ui.Fg(t.Muted).Render(i18n.T("run.disk.done", units.Bytes(before), units.Bytes(after), units.Bytes(res.Freed))))
	}
	// A failure stops the queue: the disks after it never ran.
	for _, d := range m.run.queue[min(len(m.run.results), len(m.run.queue)):] {
		lines = append(lines, "   "+ui.Fg(t.Muted).Render("–")+"  "+ui.Fit(d.Name, 22)+ui.Fg(t.Muted).Render(i18n.T("run.disk.skipped")))
	}
	return append(lines, "")
}

// cleanView lists what can go inside a distribution, to choose from, in
// groups by what removing costs: caches, downloads, Docker images, logs.
func (m model) cleanView() string {
	t := m.th
	items := m.cleanItems()
	body := m.cleanHead()
	rows := m.cleanRows(items)
	visible := m.cleanVisible()
	top := min(m.pickTop, max(0, len(rows)-visible))
	var chosen int64
	n := 0
	for _, it := range items {
		if m.picked[pickKey(m.pickFor, it.ID)] {
			chosen += it.Bytes
			n++
		}
	}
	for r := top; r < len(rows) && r < top+visible; r++ {
		row := rows[r]
		switch row.item {
		case -2:
			body = append(body, "")
			continue
		case -1:
			body = append(body, m.groupHeader(row.kind, items))
			continue
		}
		it := items[row.item]
		box := ui.Fg(t.Muted).Render("[ ]")
		if m.picked[pickKey(m.pickFor, it.ID)] {
			box = ui.Fg(t.Mark).Render("[✓]")
		}
		note := ""
		if it.Note != "" {
			note = ui.Fg(t.Muted).Render("  " + since(it.Note))
		}
		cursor := " "
		if row.item == m.pickSel {
			cursor = ui.Fg(t.Mark).Render("›")
		}
		line := " " + cursor + box + " " + ui.FitRight(units.Bytes(it.Bytes), 8) + "  " + itemLabel(it) + note
		if row.item == m.pickSel {
			line = ui.Highlight(line, t.Selected, m.width)
		}
		body = append(body, line)
	}
	status := i18n.TN("clean.chosen", n, n, units.Bytes(chosen))
	// Where the window is in a list longer than it, counted in items: the
	// blank lines and group headers in between are not choices.
	if len(rows) > visible {
		first, last := -1, -1
		for r := top; r < len(rows) && r < top+visible; r++ {
			if rows[r].item >= 0 {
				if first < 0 {
					first = rows[r].item
				}
				last = rows[r].item
			}
		}
		if first >= 0 {
			if first == last {
				// One item shows: its place, not a range of one.
				status += " · " + i18n.T("clean.position.one", first+1, len(items))
			} else {
				status += " · " + i18n.T("clean.position", first+1, last+1, len(items))
			}
		}
	}
	keys := m.keyBar([][2]string{{"space", i18n.T("key.choose")}, {"a", i18n.T("key.caches")}, {"n", i18n.T("key.none")},
		{"⏎", i18n.T("key.continue")}, {"esc", i18n.T("key.back")}})
	return m.page(body, " "+ui.Fg(t.Muted).Render(status), keys)
}

// cleanHead is the lines above the clean list: its title, and what it
// promises, which wraps rather than lose its end in a narrow window.
func (m model) cleanHead() []string {
	t := m.th
	bold := lipgloss.NewStyle().Bold(true).Foreground(t.Ink)
	head := []string{" " + bold.Render(i18n.T("clean.title", m.pickFor))}
	head = append(head, m.wrap(i18n.T("clean.what"), " ", ui.Fg(t.Muted))...)
	head = append(head, m.wrap(i18n.T("clean.safe"), " ", ui.Fg(t.Muted))...)
	return append(head, "")
}

// groupHeader is the line above a group: its name, what removing it costs,
// and how much it holds; a narrow window leaves the cost out.
func (m model) groupHeader(k survey.Kind, items []survey.Item) string {
	t := m.th
	var n int
	var total int64
	for _, it := range items {
		if it.Kind == k {
			n++
			total += it.Bytes
		}
	}
	name := lipgloss.NewStyle().Bold(true).Foreground(t.Ink).Render(i18n.T("group." + string(k)))
	count := i18n.TN("group.count", n, n, units.Bytes(total))
	if full := " " + name + ui.Fg(t.Muted).Render("  "+i18n.T("group."+string(k)+".cost")+" · "+count); lipgloss.Width(full) <= m.width {
		return full
	}
	return " " + name + ui.Fg(t.Muted).Render("  "+count)
}

// pageVisible is how many lines of the help or the history fit under their
// title.
func (m model) pageVisible() int { return max(1, m.bodyHeight()-2) }

// pageLen is how many lines the help or the history has.
func (m model) pageLen() int {
	if m.mode == modeHelp {
		return len(m.helpLines())
	}
	return len(m.historyLines())
}

// pageOf lays out the help or the history: its title, the window of its
// lines, where that window is when they do not all fit, and its keys.
func (m model) pageOf(title string, lines []string, status string) string {
	t := m.th
	visible := m.pageVisible()
	top := max(0, min(m.pageTop, len(lines)-visible))
	body := []string{" " + lipgloss.NewStyle().Bold(true).Foreground(t.Ink).Render(title), ""}
	body = append(body, lines[top:min(len(lines), top+visible)]...)
	keys := [][2]string{{"esc", i18n.T("key.back")}, {"q", i18n.T("key.quit")}}
	if len(lines) > visible {
		where := i18n.T("page.position", top+1, min(top+visible, len(lines)), len(lines))
		if status != "" {
			status += " · "
		}
		status += where
		keys = append([][2]string{{"↑↓", i18n.T("key.scroll")}}, keys...)
	}
	if status != "" {
		status = " " + ui.Fg(t.Muted).Render(status)
	}
	return m.page(body, status, m.keyBar(keys))
}

// historyView lists past runs, newest first.
func (m model) historyView() string {
	var total int64
	for _, e := range m.hist {
		total += e.Freed
	}
	return m.pageOf(i18n.T("history.title"), m.historyLines(), i18n.T("history.total", units.Bytes(total)))
}

// historyLines is one line per past run, newest first.
func (m model) historyLines() []string {
	t := m.th
	if len(m.hist) == 0 {
		return []string{" " + ui.Fg(t.Muted).Render(i18n.T("history.empty"))}
	}
	var lines []string
	for i := len(m.hist) - 1; i >= 0; i-- {
		e := m.hist[i]
		when := ui.Fg(t.Muted).Render(e.At.Local().Format("2006-01-02 15:04"))
		var what string
		if e.Err != "" {
			what = ui.Fg(t.Cat[ui.CatBad]).Render(ui.Fit(i18n.T("history.failed"), 12)) + ui.Fg(t.Muted).Render(oneLine(e.Err))
		} else {
			what = ui.Fg(t.Cat[ui.CatDone]).Render(ui.FitRight(units.Bytes(e.Freed), 9)) + "   " +
				ui.Fg(t.Muted).Render(units.Bytes(e.Before)+" → "+units.Bytes(e.After)+" · "+elapsed(time.Duration(e.Took*float64(time.Second))))
		}
		lines = append(lines, " "+when+"  "+ui.Fit(e.Disk, 22)+"  "+what)
	}
	return lines
}

// helpView explains the keys and what a shrink does.
func (m model) helpView() string {
	return m.pageOf(i18n.T("help.title"), m.helpLines(), "")
}

// helpLines is the help under its title: the keys, the clean list's keys,
// the steps of a shrink and what the colors mean.
func (m model) helpLines() []string {
	t := m.th
	bold := lipgloss.NewStyle().Bold(true).Foreground(t.Ink)
	var body []string
	keys := [][2]string{
		{"↑↓", "help.select"}, {"space", "help.mark"}, {"s ⏎", "help.shrink"}, {"c", "help.clean"},
		{"r", "help.reload"}, {"R", "help.recover"}, {"h", "help.history"}, {"q", "help.quit"},
	}
	// The keys' column is as wide as its widest key, as the console shows
	// it: ⏎ is a word where its font has no ⏎.
	w := 5
	for _, k := range keys {
		w = max(w, lipgloss.Width(ui.Words(k[0])))
	}
	for _, k := range keys {
		// What a key does wraps under its own words, never cut.
		lead := "  " + t.KeyCap(ui.Fit(ui.Words(k[0]), w), "") + " "
		body = append(body, m.wrapHang(i18n.T(k[1]), m.width, lead, strings.Repeat(" ", lipgloss.Width(lead)), lipgloss.NewStyle())...)
	}
	body = append(body, "")
	body = append(body, m.wrap(i18n.T("help.cleankeys"), "  ", ui.Fg(t.Muted))...)
	body = append(body, "", " "+bold.Render(i18n.T("help.how")))
	for _, l := range strings.Split(i18n.T("help.steps"), "\n") {
		// A step that wraps continues under its text, past its number.
		wrapped := m.wrap(l, "  ", ui.Fg(t.Muted))
		for i := 1; i < len(wrapped); i++ {
			wrapped[i] = "   " + wrapped[i]
		}
		body = append(body, wrapped...)
	}
	body = append(body, "")
	return append(body, m.legend()...)
}

// legend says what each color means, each in its own color, a meaning never
// split across lines.
func (m model) legend() []string {
	t := m.th
	parts := []string{
		ui.Fg(t.Cat[ui.CatGain]).Render(i18n.T("help.legend.gain")),
		ui.Fg(t.Cat[ui.CatSmall]).Render(i18n.T("help.legend.small")),
		ui.Fg(t.Cat[ui.CatLive]).Render(i18n.T("help.legend.live")),
		ui.Fg(t.Cat[ui.CatSafe]).Render(i18n.T("help.legend.safe")),
		ui.Fg(t.Cat[ui.CatWarn]).Render(i18n.T("help.legend.warn")),
		ui.Fg(t.Cat[ui.CatBad]).Render(i18n.T("help.legend.bad")),
	}
	sep := ui.Fg(t.Muted).Render(" · ")
	lines := []string{" " + ui.Fg(t.Muted).Render(i18n.T("help.legend"))}
	cur := " "
	for _, p := range parts {
		next := cur + p
		if cur != " " {
			next = cur + sep + p
		}
		if lipgloss.Width(next) > m.width-2 && cur != " " {
			lines = append(lines, cur)
			next = " " + p
		}
		cur = next
	}
	return append(lines, cur)
}
