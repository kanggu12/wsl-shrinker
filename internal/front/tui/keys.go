package tui

import (
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/kanggu12/wsl-shrinker/internal/app"
	"github.com/kanggu12/wsl-shrinker/internal/engine/disks"
	"github.com/kanggu12/wsl-shrinker/internal/engine/journal"
	"github.com/kanggu12/wsl-shrinker/internal/engine/survey"
	"github.com/kanggu12/wsl-shrinker/internal/front/i18n"
	"github.com/kanggu12/wsl-shrinker/internal/front/ui"
)

// key handles a key press for the current mode.
func (m model) key(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := k.String()
	// ctrl+c always leaves, except while a disk is being compacted: leaving
	// then would not stop WSL, only hide what it does.
	if s == "ctrl+c" && !(m.mode == modeRun && m.run != nil && !m.run.done) {
		return m, tea.Quit
	}
	// While a run asked for on the command line gets ready, only q works.
	if m.preparing && s != "q" {
		return m, nil
	}
	switch m.mode {
	case modeHelp, modeHistory:
		return m.keyPage(s)
	case modeConfirm:
		return m.keyConfirm(s)
	case modeForget:
		return m.keyForget(s)
	case modeClean:
		return m.keyClean(s)
	case modeRun:
		return m.keyRun(s)
	}
	switch s {
	case "q":
		return m, tea.Quit
	case "up", "k":
		return m, m.move(-1)
	case "down", "j":
		return m, m.move(1)
	case "home", "g":
		return m, m.move(-1 << 20)
	case "end", "G":
		return m, m.move(1 << 20)
	case "space":
		// Mark the disk for a run of several; a disk that cannot be shrunk
		// now stays out, and the status line says why.
		if d, ok := m.selected(); ok {
			if why := m.blocked(d); why != "" {
				// In the color of the reason: protected, in use, or cannot.
				// The cursor stays on the disk the message is about.
				m.say(why, m.category(d))
				return m, nil
			} else {
				m.marked[d.Name] = !m.marked[d.Name]
				if !m.marked[d.Name] {
					delete(m.marked, d.Name)
				}
			}
		}
		return m, m.move(1)
	case "r":
		// Read everything again, the inside of the selected disk included,
		// and WSL's version when it did not answer before.
		m.loading = true
		if d, ok := m.selected(); ok && d.Running && d.Distro != "" {
			delete(m.surveys, d.Distro)
		}
		if !m.infoKnown {
			return m, tea.Batch(m.loadDisks(), m.loadInfo())
		}
		return m, m.loadDisks()
	case "c":
		if d, ok := m.selected(); ok {
			return m.openClean(d)
		}
	case "s", "enter":
		return m.openConfirm()
	case "R":
		// Finish a run that was cut off: its distribution and containers
		// started again. One try at a time: a second R while the first is
		// at work would start the same things twice.
		if m.unfinishedState != journal.CutOff || m.settling {
			return m, nil
		}
		b, ctx := m.b, m.ctx
		m.settling = true
		m.say(i18n.T("toast.recovering"), ui.CatLive)
		return m, func() tea.Msg {
			said, err := b.Recover(ctx)
			return recoverMsg{said: said, err: err}
		}
	case "F":
		// Forget a run R could not finish. Offered only after R failed to
		// start again what the run had stopped, and asked about first.
		if m.unfinishedState != journal.CutOff || !m.recoverFailed || m.settling {
			return m, nil
		}
		// The question stands alone, as the one before a run does.
		m.toast = ""
		m.mode = modeForget
	case "h":
		// The runs read with the disks show at once; read again, any run
		// since takes its place.
		m.mode, m.pageTop = modeHistory, 0
		return m, m.loadHistory()
	case "?":
		m.mode, m.pageTop = modeHelp, 0
	}
	return m, nil
}

// keyPage scrolls the help or the history; any other key goes back.
func (m model) keyPage(s string) (tea.Model, tea.Cmd) {
	last := max(0, m.pageLen()-m.pageVisible())
	page := max(1, m.pageVisible()-1)
	// A window made taller may leave the top past what is left to show.
	m.pageTop = min(m.pageTop, last)
	switch s {
	case "q":
		return m, tea.Quit
	case "up", "k":
		m.pageTop = max(0, m.pageTop-1)
	case "down", "j":
		m.pageTop = min(last, m.pageTop+1)
	case "pgup":
		m.pageTop = max(0, m.pageTop-page)
	case "pgdown":
		m.pageTop = min(last, m.pageTop+page)
	case "home", "g":
		m.pageTop = 0
	case "end", "G":
		m.pageTop = last
	default:
		m.mode, m.pageTop = modeMain, 0
	}
	return m, nil
}

// openClean opens the list of what can go inside a distribution.
func (m model) openClean(d disks.Disk) (tea.Model, tea.Cmd) {
	switch {
	case d.Distro == "":
		m.say(i18n.T("toast.clean.notdistro", d.Name), ui.CatSmall)
		return m, nil
	case !d.Running:
		m.say(i18n.T("toast.clean.notrunning", d.Name), ui.CatSmall)
		return m, nil
	}
	s := m.surveys[d.Distro]
	if s == nil || s.loading {
		m.say(i18n.T("toast.clean.loading"), ui.CatSmall)
		// Look inside this disk, the one asked about, if no look is under way.
		if s == nil {
			return m, m.loadSurvey(d.Distro)
		}
		return m, nil
	}
	// A look that failed found nothing because it could not look: say
	// so, not that there is nothing to remove.
	if s.err != nil {
		m.say(i18n.T("inside.failed", oneLine(s.err.Error())), ui.CatBad)
		return m, nil
	}
	if len(s.r.Items) == 0 {
		m.say(i18n.T("toast.clean.nothing", d.Name), ui.CatSmall)
		return m, nil
	}
	m.mode, m.pickFor, m.pickSel, m.pickTop = modeClean, d.Distro, 0, 0
	return m, nil
}

// keyClean moves through the clean list and toggles items.
func (m model) keyClean(s string) (tea.Model, tea.Cmd) {
	items := m.cleanItems()
	// Nothing left to choose from (a look inside came back empty).
	if len(items) == 0 {
		m.mode = modeMain
		return m, nil
	}
	last := len(items) - 1
	m.pickSel = min(max(m.pickSel, 0), last)
	page := max(1, m.cleanVisible()-1)
	switch s {
	case "q":
		// Quits, as everywhere but in a question.
		return m, tea.Quit
	case "esc":
		m.mode = modeMain
		return m, nil
	case "up", "k":
		m.pickSel = max(m.pickSel-1, 0)
	case "down", "j":
		m.pickSel = min(m.pickSel+1, last)
	case "pgup":
		m.pickSel = max(m.pickSel-page, 0)
	case "pgdown":
		m.pickSel = min(m.pickSel+page, last)
	case "home", "g":
		m.pickSel = 0
	case "end", "G":
		m.pickSel = last
	case "space":
		k := pickKey(m.pickFor, items[m.pickSel].ID)
		m.picked[k] = !m.picked[k]
		if !m.picked[k] {
			delete(m.picked, k)
		}
		m.pickSel = min(m.pickSel+1, last)
	case "a":
		// Every cache, nothing that needs a download or holds history.
		for _, it := range items {
			if it.Kind == survey.KindCache {
				m.picked[pickKey(m.pickFor, it.ID)] = true
			}
		}
	case "n":
		for _, it := range items {
			delete(m.picked, pickKey(m.pickFor, it.ID))
		}
	case "enter":
		// Continue to the run with these chosen: this disk joins it, with the
		// disks marked before, so what was chosen is not left for later.
		m.mode = modeMain
		if len(m.marked) > 0 {
			if i := slices.IndexFunc(m.list, func(d disks.Disk) bool { return d.Distro == m.pickFor }); i >= 0 {
				m.marked[m.list[i].Name] = true
			}
		}
		return m.openConfirm()
	}
	m.scrollClean()
	return m, nil
}

// openConfirm asks before a run.
func (m model) openConfirm() (tea.Model, tea.Cmd) {
	// A run is already on its way to a new window.
	if m.reopening {
		return m, nil
	}
	// A run that has not ended comes first: a new one would lose what the
	// cut-off one stopped, or meet the other one's disk.
	switch m.unfinishedState {
	case journal.CutOff:
		m.say(i18n.T("toast.cutoff"), ui.CatWarn)
		return m, nil
	case journal.Going:
		m.say(i18n.T("toast.going", m.unfinished.Disk), ui.CatLive)
		return m, nil
	}
	q := m.queueFor()
	if len(q) == 0 {
		if d, ok := m.selected(); ok {
			m.say(m.blocked(d), m.category(d))
		}
		return m, nil
	}
	// The question stands alone: a note from before would read as part of it.
	m.toast = ""
	m.mode = modeConfirm
	return m, nil
}

// keyConfirm answers the question.
func (m model) keyConfirm(s string) (tea.Model, tea.Cmd) {
	switch s {
	case "c":
		// Choose what to remove inside first, where the box offers it: the
		// list is the one disk of the run's, which may not be the selected
		// one when it was marked.
		q := m.queueFor()
		if !m.choosable(q) {
			return m, nil
		}
		m.mode = modeMain
		return m.openClean(q[0])
	case "enter", "y":
		q := m.queueFor()
		// The disks may have become blocked while the box was open.
		if len(q) == 0 {
			m.mode = modeMain
			if d, ok := m.selected(); ok {
				m.say(m.blocked(d), m.category(d))
			}
			return m, nil
		}
		clean := map[string][]survey.Item{}
		for _, d := range q {
			if d.Distro != "" && d.Running {
				clean[d.Name] = m.chosenItems(d.Distro)
			}
		}
		// From inside WSL, stopping a running distribution would close
		// this terminal: continue in a new window instead.
		if m.hostsAny(q) {
			// Opening the window and waiting for its word take a few
			// seconds: the screen goes on meanwhile.
			b, args := m.b, app.HandoffArgs(string(i18n.Current()), q, clean)
			m.mode, m.reopening = modeMain, true
			m.say(i18n.T("toast.reopening"), ui.CatLive)
			return m, func() tea.Msg { return reopenMsg{err: b.Reopen(args)} }
		}
		return m.startRun(q, clean)
	case "esc", "n", "q":
		m.mode = modeMain
	}
	return m, nil
}

// keyForget answers the question before a cut-off run is forgotten.
func (m model) keyForget(s string) (tea.Model, tea.Cmd) {
	switch s {
	case "enter", "y":
		m.mode = modeMain
		// The box is open only while the run is cut off and neither R nor F
		// is at work: a reload that finds the run ended closes it, and F
		// waits for R. The check keeps an answer from acting should that
		// ever stop holding.
		if m.unfinishedState != journal.CutOff || m.settling {
			return m, nil
		}
		// The run as the box showed it goes with the answer, for the words
		// after it.
		b, ctx, e := m.b, m.ctx, m.unfinished
		m.settling = true
		return m, func() tea.Msg { return forgetMsg{run: e, err: b.Forget(ctx, e)} }
	case "esc", "n", "q":
		m.mode = modeMain
	}
	return m, nil
}

// keyRun handles keys while a run goes and after it.
func (m model) keyRun(s string) (tea.Model, tea.Cmd) {
	if m.run == nil || !m.run.done {
		return m, nil
	}
	switch s {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "enter", "esc":
		m.mode, m.run = modeMain, nil
		// What was cleaned is gone: forget the choices and look again.
		m.picked = map[string]bool{}
		m.surveys = map[string]*surveyState{}
		return m, m.surveySelected()
	}
	return m, nil
}
