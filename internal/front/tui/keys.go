package tui

import (
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/kanggu12/wsl-shrinker/internal/engine/disks"
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
		if s == "q" {
			return m, tea.Quit
		}
		m.mode = modeMain
		return m, nil
	case modeConfirm:
		return m.keyConfirm(s)
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
				m.say(why, ui.CatSafe)
			} else {
				m.marked[d.Name] = !m.marked[d.Name]
				if !m.marked[d.Name] {
					delete(m.marked, d.Name)
				}
			}
		}
		return m, m.move(1)
	case "r":
		// Read everything again, the inside of the selected disk included.
		m.loading = true
		if d, ok := m.selected(); ok && d.Running && d.Distro != "" {
			delete(m.surveys, d.Distro)
		}
		return m, m.loadDisks()
	case "c":
		return m.openClean()
	case "s", "enter":
		return m.openConfirm()
	case "h":
		m.hist = m.b.History()
		m.mode = modeHistory
	case "?":
		m.mode = modeHelp
	}
	return m, nil
}

// openClean opens the list of what can go inside the selected distribution.
func (m model) openClean() (tea.Model, tea.Cmd) {
	d, ok := m.selected()
	switch {
	case !ok:
		return m, nil
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
		return m, m.surveySelected()
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
	case "esc", "q":
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
	q := m.queueFor()
	if len(q) == 0 {
		if d, ok := m.selected(); ok {
			m.say(m.blocked(d), ui.CatSafe)
		}
		return m, nil
	}
	m.mode = modeConfirm
	return m, nil
}

// keyConfirm answers the question.
func (m model) keyConfirm(s string) (tea.Model, tea.Cmd) {
	switch s {
	case "c":
		// Choose what to remove inside first, as the box offers.
		m.mode = modeMain
		return m.openClean()
	case "enter", "y":
		q := m.queueFor()
		// The disks may have become blocked while the box was open.
		if len(q) == 0 {
			m.mode = modeMain
			if d, ok := m.selected(); ok {
				m.say(m.blocked(d), ui.CatSafe)
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
			if err := m.b.Reopen(reopenArgs(q, clean)); err != nil {
				m.mode = modeMain
				m.say(i18n.T("toast.reopen.failed", err.Error()), ui.CatBad)
				return m, nil
			}
			return m, tea.Quit
		}
		return m.startRun(q, clean)
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
