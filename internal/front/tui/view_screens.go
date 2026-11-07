package tui

import (
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/kanggu12/wsl-shrinker/internal/core/units"
	"github.com/kanggu12/wsl-shrinker/internal/engine/shrink"
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
	var body []string
	title := i18n.T("run.title", d.Name)
	if r.done {
		title = i18n.T("run.title.done", d.Name)
	}
	if len(r.queue) > 1 {
		title += ui.Fg(t.Muted).Render("  " + i18n.T("run.of", r.at+1, len(r.queue)))
	}
	if !r.done {
		title += ui.Fg(t.Muted).Render("  " + elapsed(m.now.Sub(r.started)))
	}
	body = append(body, " "+bold.Render(title), "")
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
		case e.Status == shrink.Skipped:
			mark, color, detail = "–", t.Muted, i18n.T("run.skipped."+string(s))
		case e.Status == shrink.Warned:
			mark, color, detail = "!", t.Cat[ui.CatGain], stepDetail(e)
		case e.Status == shrink.Failed:
			mark, color, detail = "✗", t.Cat[ui.CatBad], e.Detail
		}
		name := ui.Fit(i18n.T("step."+string(s)), 14)
		if seen {
			name = lipgloss.NewStyle().Foreground(t.Ink).Render(name)
		} else {
			name = ui.Fg(t.Muted).Render(name)
		}
		body = append(body, "   "+ui.Fg(color).Render(mark)+"  "+name+"  "+ui.Fg(t.Muted).Render(ui.Fit(detail, m.width-26)))
	}
	body = append(body, "")
	// Before and after, on one scale: the after bar fills in at the end.
	if e := d.Estimate; e != nil {
		bw := min(m.width-30, 60)
		before := t.Bar(bw, e.FileSize, ui.Part{Value: e.FileSize, Color: t.Cat[ui.CatSmall]})
		afterSize, label := e.After, i18n.T("run.expected")
		if res := m.resultFor(d.Name); res != nil && res.File != nil {
			afterSize, label = res.File.OnDisk, i18n.T("run.after")
		}
		after := t.Bar(bw, e.FileSize, ui.Part{Value: afterSize, Color: t.Cat[ui.CatLive]})
		body = append(body,
			"   "+ui.Fit(i18n.T("run.before"), 10)+before+"  "+units.Bytes(e.FileSize),
			"   "+ui.Fit(label, 10)+after+"  "+units.Bytes(afterSize), "")
	}
	// The outcome, once there is one.
	if r.done {
		body = append(body, m.outcome()...)
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

// outcome sums up a finished run.
func (m model) outcome() []string {
	t := m.th
	var freed int64
	var took time.Duration
	var failed *shrink.Result
	for i, res := range m.run.results {
		freed += res.Freed
		took += res.Took
		if res.Err != "" {
			failed = &m.run.results[i]
		}
	}
	if failed != nil {
		lines := []string{" " + ui.Fg(t.Cat[ui.CatBad]).Bold(true).Render(i18n.T("run.failed", failed.Disk))}
		lines = append(lines, " "+ui.Fg(t.Muted).Render(oneLine(failed.Err)))
		if freed > 0 {
			lines = append(lines, " "+i18n.T("run.partial", units.Bytes(freed)))
		}
		return lines
	}
	return []string{" " + ui.Fg(t.Cat[ui.CatDone]).Bold(true).Render(i18n.T("run.freed", units.Bytes(freed), elapsed(took)))}
}

// cleanView lists what can go inside a distribution, to choose from.
func (m model) cleanView() string {
	t := m.th
	s := m.surveys[m.pickFor]
	items := s.r.Items
	bold := lipgloss.NewStyle().Bold(true).Foreground(t.Ink)
	// The promise is two short lines, so it fits a narrow window whole.
	body := []string{" " + bold.Render(i18n.T("clean.title", m.pickFor)),
		" " + ui.Fg(t.Muted).Render(i18n.T("clean.what")), " " + ui.Fg(t.Muted).Render(i18n.T("clean.safe")), ""}
	// Keep the cursor on screen.
	rows := m.bodyHeight() - len(body) - 2
	if m.pickSel < m.pickTop {
		m.pickTop = m.pickSel
	}
	if m.pickSel >= m.pickTop+rows {
		m.pickTop = m.pickSel - rows + 1
	}
	var chosen int64
	n := 0
	for _, it := range items {
		if m.picked[pickKey(m.pickFor, it.ID)] {
			chosen += it.Bytes
			n++
		}
	}
	for i := m.pickTop; i < len(items) && i < m.pickTop+rows; i++ {
		it := items[i]
		box := ui.Fg(t.Muted).Render("[ ]")
		if m.picked[pickKey(m.pickFor, it.ID)] {
			box = ui.Fg(t.Mark).Render("[✓]")
		}
		note := ""
		if it.Note != "" {
			note = ui.Fg(t.Muted).Render("  " + it.Note)
		}
		cursor := " "
		if i == m.pickSel {
			cursor = ui.Fg(t.Mark).Render("›")
		}
		row := " " + cursor + box + " " + ui.FitRight(units.Bytes(it.Bytes), 8) + "  " + m.kindTag(it.Kind) + "  " + itemLabel(it) + note
		if i == m.pickSel {
			row = ui.Highlight(row, t.Selected, m.width)
		}
		body = append(body, row)
	}
	status := " " + ui.Fg(t.Muted).Render(i18n.TN("clean.chosen", n, n, units.Bytes(chosen)))
	keys := m.keyBar([][2]string{{"space", i18n.T("key.choose")}, {"a", i18n.T("key.caches")}, {"n", i18n.T("key.none")},
		{"⏎", i18n.T("key.continue")}, {"esc", i18n.T("key.back")}})
	return m.page(body, status, keys)
}

// historyView lists past runs, newest first.
func (m model) historyView() string {
	t := m.th
	bold := lipgloss.NewStyle().Bold(true).Foreground(t.Ink)
	body := []string{" " + bold.Render(i18n.T("history.title")), ""}
	if len(m.hist) == 0 {
		body = append(body, " "+ui.Fg(t.Muted).Render(i18n.T("history.empty")))
	}
	var total int64
	for _, e := range m.hist {
		total += e.Freed
	}
	for i := len(m.hist) - 1; i >= 0 && len(body) < m.bodyHeight(); i-- {
		e := m.hist[i]
		when := ui.Fg(t.Muted).Render(e.At.Local().Format("2006-01-02 15:04"))
		var what string
		if e.Err != "" {
			what = ui.Fg(t.Cat[ui.CatBad]).Render(ui.Fit(i18n.T("history.failed"), 12)) + ui.Fg(t.Muted).Render(oneLine(e.Err))
		} else {
			what = ui.Fg(t.Cat[ui.CatDone]).Render(ui.FitRight(units.Bytes(e.Freed), 9)) + "   " +
				ui.Fg(t.Muted).Render(units.Bytes(e.Before)+" → "+units.Bytes(e.After)+" · "+elapsed(time.Duration(e.Took*float64(time.Second))))
		}
		body = append(body, " "+when+"  "+ui.Fit(e.Disk, 22)+"  "+what)
	}
	status := " " + ui.Fg(t.Muted).Render(i18n.T("history.total", units.Bytes(total)))
	return m.page(body, status, m.keyBar([][2]string{{"esc", i18n.T("key.back")}, {"q", i18n.T("key.quit")}}))
}

// helpView explains the keys and what a shrink does.
func (m model) helpView() string {
	t := m.th
	bold := lipgloss.NewStyle().Bold(true).Foreground(t.Ink)
	body := []string{" " + bold.Render(i18n.T("help.title")), ""}
	for _, k := range [][2]string{
		{"↑↓", "help.select"}, {"space", "help.mark"}, {"s ⏎", "help.shrink"}, {"c", "help.clean"},
		{"r", "help.reload"}, {"h", "help.history"}, {"q", "help.quit"},
	} {
		body = append(body, "  "+t.KeyCap(ui.Fit(k[0], 5), "")+" "+i18n.T(k[1]))
	}
	body = append(body, "", " "+bold.Render(i18n.T("help.how")))
	for _, l := range strings.Split(i18n.T("help.steps"), "\n") {
		body = append(body, "  "+ui.Fg(t.Muted).Render(l))
	}
	return m.page(body, "", m.keyBar([][2]string{{"esc", i18n.T("key.back")}, {"q", i18n.T("key.quit")}}))
}
