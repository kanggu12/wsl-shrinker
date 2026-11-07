// Package tui is wsl-shrinker's terminal screen: every WSL disk with what it
// would give back, what fills the running ones, and a run that shrinks the
// chosen disks step by step.
package tui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/kanggu12/wsl-shrinker/internal/engine/disks"
	"github.com/kanggu12/wsl-shrinker/internal/engine/history"
	"github.com/kanggu12/wsl-shrinker/internal/engine/shrink"
	"github.com/kanggu12/wsl-shrinker/internal/engine/survey"
	"github.com/kanggu12/wsl-shrinker/internal/front/i18n"
	"github.com/kanggu12/wsl-shrinker/internal/front/ui"
	"github.com/kanggu12/wsl-shrinker/internal/machine/wsl"
)

// mode is what the screen shows.
type mode int

const (
	modeMain    mode = iota // the disks
	modeClean               // choosing what to remove inside a distribution
	modeConfirm             // the question before a run
	modeRun                 // a run in progress, then its result
	modeHistory             // past runs
	modeHelp                // keys and how it works
)

// Messages the model receives from its commands.
type (
	disksMsg struct {
		list []disks.Disk
		err  error
	}
	infoMsg   wsl.Info
	surveyMsg struct {
		distro string
		r      survey.Result
		err    error
	}
	// eventMsg is one step's progress; doneMsg ends one disk's run.
	eventMsg shrink.Event
	doneMsg  shrink.Result
	// frameMsg advances the spinner and the elapsed time while a run goes.
	frameMsg time.Time
	// autoRunMsg starts the run the options asked for.
	autoRunMsg struct {
		queue []disks.Disk
		clean map[string][]survey.Item
		err   error
	}
)

// surveyState is what is known about one distribution's inside.
type surveyState struct {
	loading bool
	r       survey.Result
	err     error
	at      time.Time
}

// runState is one run of one or more disks.
type runState struct {
	// queue holds the disks to run, in order; at is the one running.
	queue []disks.Disk
	at    int
	// clean is what to remove first, by disk name.
	clean map[string][]survey.Item
	// steps holds each step's latest event for the disk running.
	steps map[shrink.Step]shrink.Event
	// started is when the current disk began; stepStart when its step did.
	started, stepStart time.Time
	// results holds every finished disk's result.
	results []shrink.Result
	// done is set when the queue is finished.
	done bool
	// events carries the engine's progress into the model.
	events chan tea.Msg
}

// model is the screen's state.
type model struct {
	b   Backend
	ctx context.Context
	th  ui.Theme
	// themeFixed is set when the settings chose dark or light.
	themeFixed bool

	width, height int
	now           time.Time

	info    wsl.Info
	list    []disks.Disk
	err     error
	loaded  bool
	loading bool

	// sel is the selected row; selName keeps it on the same disk across
	// reloads. marked holds the disks chosen with space, by name.
	sel     int
	selName string
	marked  map[string]bool

	surveys map[string]*surveyState

	mode mode
	// pick is the clean list's cursor, for one distribution; picked holds the
	// chosen items of every distribution, by pickKey: item ids repeat across
	// distributions ("apt", "docker-build-cache").
	pickFor string
	pickSel int
	picked  map[string]bool
	pickTop int

	run  *runState
	hist []history.Entry
	spin int

	toast      string
	toastCat   ui.Category
	toastUntil time.Time

	// pending is a run asked for by the options, started after the first load.
	pending *Options
}

// newModel builds the screen for a backend.
func newModel(ctx context.Context, b Backend) model {
	return model{b: b, ctx: ctx, th: ui.NewTheme(true), now: time.Now(), width: 120, height: 34,
		marked: map[string]bool{}, surveys: map[string]*surveyState{}, picked: map[string]bool{}}
}

// Options are how the screen starts.
type Options struct {
	// Theme is "auto", "dark" or "light".
	Theme string
	// Run starts a run of these disks at once, already confirmed: the new
	// window a run moved to from inside WSL. Clean holds what to remove
	// inside each, as survey item ids.
	Run   []string
	Clean map[string][]string
}

// Run opens the screen on the terminal.
func Run(ctx context.Context, b Backend, opt Options) error {
	m := newModel(ctx, b)
	if len(opt.Run) > 0 {
		m.pending = &opt
	}
	if opt.Theme == "light" || opt.Theme == "dark" {
		m.th, m.themeFixed = ui.NewTheme(opt.Theme == "dark"), true
	}
	_, err := tea.NewProgram(m).Run()
	return err
}

// Init loads the disks and WSL's version, and asks the terminal for its
// background so the colors suit it.
func (m model) Init() tea.Cmd {
	return tea.Batch(tea.RequestBackgroundColor, m.loadDisks(), m.loadInfo())
}

// loadDisks reads the disks in the background.
func (m model) loadDisks() tea.Cmd {
	b, ctx := m.b, m.ctx
	return func() tea.Msg {
		l, err := b.Disks(ctx)
		return disksMsg{list: l, err: err}
	}
}

// loadInfo asks WSL for its version.
func (m model) loadInfo() tea.Cmd {
	b, ctx := m.b, m.ctx
	return func() tea.Msg { return infoMsg(b.Info(ctx)) }
}

// loadSurvey looks inside a running distribution.
func (m *model) loadSurvey(distro string) tea.Cmd {
	s := m.surveys[distro]
	if s == nil {
		s = &surveyState{}
		m.surveys[distro] = s
	}
	if s.loading {
		return nil
	}
	s.loading = true
	b, ctx := m.b, m.ctx
	return func() tea.Msg {
		r, err := b.Survey(ctx, distro)
		return surveyMsg{distro: distro, r: r, err: err}
	}
}

// frameTick drives the spinner while a run goes.
func frameTick() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg { return frameMsg(t) })
}

// Update handles one message.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.BackgroundColorMsg:
		if !m.themeFixed {
			m.th = ui.NewTheme(msg.IsDark())
		}
		return m, nil
	case infoMsg:
		m.info = wsl.Info(msg)
		return m, nil
	case disksMsg:
		m.loading, m.loaded = false, true
		m.list, m.err, m.now = msg.list, msg.err, time.Now()
		m.hist = m.b.History()
		m.reselect()
		if m.pending != nil && m.err == nil {
			p := m.pending
			m.pending = nil
			return m, m.prepareRun(*p)
		}
		return m, m.surveySelected()
	case autoRunMsg:
		if msg.err != nil {
			m.say(msg.err.Error(), ui.CatBad)
			return m, nil
		}
		return m.startRun(msg.queue, msg.clean)
	case surveyMsg:
		s := m.surveys[msg.distro]
		s.loading, s.r, s.err, s.at = false, msg.r, msg.err, time.Now()
		return m, nil
	case eventMsg:
		if m.run != nil {
			e := shrink.Event(msg)
			m.run.steps[e.Step] = e
			if e.Status == shrink.Running {
				m.run.stepStart = e.At
			}
		}
		return m, m.waitRun()
	case doneMsg:
		return m.finishDisk(shrink.Result(msg))
	case frameMsg:
		m.now = time.Time(msg)
		m.spin++
		if m.mode == modeRun && m.run != nil && !m.run.done {
			return m, frameTick()
		}
		return m, nil
	case tea.KeyPressMsg:
		return m.key(msg)
	}
	return m, nil
}

// reselect keeps the selection on the same disk after a reload.
func (m *model) reselect() {
	if i := slices.IndexFunc(m.list, func(d disks.Disk) bool { return d.Name == m.selName }); i >= 0 {
		m.sel = i
	}
	m.sel = min(max(m.sel, 0), max(len(m.list)-1, 0))
	if len(m.list) > 0 {
		m.selName = m.list[m.sel].Name
	}
	// A disk that left the list cannot stay marked.
	for name := range m.marked {
		if !slices.ContainsFunc(m.list, func(d disks.Disk) bool { return d.Name == name }) {
			delete(m.marked, name)
		}
	}
}

// selected returns the selected disk, if any.
func (m model) selected() (disks.Disk, bool) {
	if m.sel < 0 || m.sel >= len(m.list) {
		return disks.Disk{}, false
	}
	return m.list[m.sel], true
}

// surveySelected starts a survey of the selected disk when it is a running
// distribution not looked at in the last minute.
func (m *model) surveySelected() tea.Cmd {
	d, ok := m.selected()
	if !ok || d.Distro == "" || !d.Running {
		return nil
	}
	if s := m.surveys[d.Distro]; s != nil && (s.loading || time.Since(s.at) < time.Minute) {
		return nil
	}
	return m.loadSurvey(d.Distro)
}

// move shifts the selection by n rows.
func (m *model) move(n int) tea.Cmd {
	if len(m.list) == 0 {
		return nil
	}
	m.sel = min(max(m.sel+n, 0), len(m.list)-1)
	m.selName = m.list[m.sel].Name
	return m.surveySelected()
}

// say shows a short message in the status line for a few seconds.
func (m *model) say(text string, c ui.Category) {
	m.toast, m.toastCat, m.toastUntil = text, c, time.Now().Add(5*time.Second)
}

// startRun begins a run of the queue: the engine runs in the background and
// its events come back through a channel, one message at a time.
func (m model) startRun(queue []disks.Disk, clean map[string][]survey.Item) (tea.Model, tea.Cmd) {
	m.mode = modeRun
	m.run = &runState{queue: queue, clean: clean, events: make(chan tea.Msg, 64)}
	return m, tea.Batch(m.startDisk(), frameTick())
}

// startDisk runs the current disk of the queue.
func (m *model) startDisk() tea.Cmd {
	r := m.run
	r.steps = map[shrink.Step]shrink.Event{}
	r.started, r.stepStart = time.Now(), time.Now()
	d := r.queue[r.at]
	opt := shrink.Options{Clean: r.clean[d.Name]}
	b, ctx, ch := m.b, m.ctx, r.events
	go func() {
		res := b.Shrink(ctx, d, opt, func(e shrink.Event) { ch <- eventMsg(e) })
		ch <- doneMsg(res)
	}()
	return m.waitRun()
}

// waitRun receives the next message from the running engine.
func (m model) waitRun() tea.Cmd {
	if m.run == nil {
		return nil
	}
	ch := m.run.events
	return func() tea.Msg { return <-ch }
}

// finishDisk records a disk's result and starts the next one.
func (m model) finishDisk(res shrink.Result) (tea.Model, tea.Cmd) {
	r := m.run
	if r == nil {
		return m, nil
	}
	r.results = append(r.results, res)
	if res.Err == "" {
		delete(m.marked, res.Disk)
	}
	// A failure stops the queue: the next disk can wait for a look.
	if res.Err != "" || r.at+1 >= len(r.queue) {
		r.done = true
		m.hist = m.b.History()
		return m, m.loadDisks()
	}
	r.at++
	return m, m.startDisk()
}

// queueFor returns the disks a run would take: the marked ones in list order,
// or the selected one when none is marked. A disk that cannot be shrunk now
// (protected, unreadable, held, encrypted) never goes.
func (m model) queueFor() []disks.Disk {
	var q []disks.Disk
	for _, d := range m.list {
		if m.marked[d.Name] && m.blocked(d) == "" {
			q = append(q, d)
		}
	}
	if len(q) == 0 {
		if d, ok := m.selected(); ok && m.blocked(d) == "" {
			q = append(q, d)
		}
	}
	return q
}

// blocked says why a disk cannot be shrunk now, or "" when it can. These
// are the reasons a run's first check would stop on, said before it starts.
func (m model) blocked(d disks.Disk) string {
	switch {
	case m.b.Protected(d.Name):
		return i18n.T("toast.protected", d.Name)
	case d.Estimate == nil:
		return i18n.T("toast.unreadable", d.Name)
	case d.File.Encrypted:
		return i18n.T("toast.encrypted", d.Name)
	case d.Distro == "" && d.File.Held:
		return i18n.T("toast.held", d.Name)
	case d.Distro != "" && m.info.Version != "" && !m.info.CanCompact:
		return i18n.T("toast.nocompact")
	}
	return ""
}

// pickKey names one item of one distribution.
func pickKey(distro, id string) string { return distro + "\x00" + id }

// chosenItems returns the survey items picked for a distribution.
func (m model) chosenItems(distro string) []survey.Item {
	s := m.surveys[distro]
	if s == nil {
		return nil
	}
	var out []survey.Item
	for _, it := range s.r.Items {
		if m.picked[pickKey(distro, it.ID)] {
			out = append(out, it)
		}
	}
	return out
}

// prepareRun finds the disks and the items to clean that the options name,
// looking inside each distribution again so only items found now are used.
func (m model) prepareRun(p Options) tea.Cmd {
	list, b, ctx := m.list, m.b, m.ctx
	m.info = b.Info(ctx)
	return func() tea.Msg {
		var q []disks.Disk
		clean := map[string][]survey.Item{}
		for _, name := range p.Run {
			i := slices.IndexFunc(list, func(d disks.Disk) bool { return strings.EqualFold(d.Name, name) })
			if i < 0 {
				return autoRunMsg{err: errors.New(i18n.T("toast.nodisk", name))}
			}
			if why := m.blocked(list[i]); why != "" {
				return autoRunMsg{err: errors.New(why)}
			}
			d := list[i]
			q = append(q, d)
			// The names in Clean come from the command line: any case.
			var ids []string
			for name, v := range p.Clean {
				if strings.EqualFold(name, d.Name) {
					ids = append(ids, v...)
				}
			}
			if len(ids) == 0 {
				continue
			}
			if !d.Running {
				return autoRunMsg{err: errors.New(i18n.T("toast.clean.notrunning", d.Name))}
			}
			r, err := b.Survey(ctx, d.Distro)
			if err != nil {
				return autoRunMsg{err: err}
			}
			// Every id must still be found inside: one that is gone stops the
			// run rather than shrink without what was asked.
			for _, id := range ids {
				j := slices.IndexFunc(r.Items, func(it survey.Item) bool { return it.ID == id })
				if j < 0 {
					return autoRunMsg{err: errors.New(i18n.T("toast.clean.gone", id, d.Name))}
				}
				clean[d.Name] = append(clean[d.Name], r.Items[j])
			}
		}
		return autoRunMsg{queue: q, clean: clean}
	}
}

// reopenArgs is the command line that continues this run in a new window.
func reopenArgs(q []disks.Disk, clean map[string][]survey.Item) []string {
	args := []string{"--reopened", "--lang", string(i18n.Current())}
	for _, d := range q {
		args = append(args, "--run", d.Name)
		var ids []string
		for _, it := range clean[d.Name] {
			ids = append(ids, it.ID)
		}
		if len(ids) > 0 {
			args = append(args, "--clean", d.Name+"="+strings.Join(ids, ","))
		}
	}
	return args
}
