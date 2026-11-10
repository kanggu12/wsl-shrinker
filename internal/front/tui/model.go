// Package tui is wsl-shrinker's terminal screen: every WSL disk with what it
// would give back, what fills the running ones, and a run that shrinks the
// chosen disks step by step.
package tui

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/kanggu12/wsl-shrinker/internal/app"
	"github.com/kanggu12/wsl-shrinker/internal/engine/disks"
	"github.com/kanggu12/wsl-shrinker/internal/engine/history"
	"github.com/kanggu12/wsl-shrinker/internal/engine/journal"
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
	modeForget              // the question before a cut-off run is forgotten
)

// Messages the model receives from its commands.
type (
	disksMsg struct {
		list []disks.Disk
		err  error
		// unfinished is a run that has not ended, read with the disks.
		unfinished      journal.Entry
		unfinishedState journal.State
	}
	// reopenMsg is how continuing in a new window went: nil once the new
	// window said it started.
	reopenMsg struct{ err error }
	// recoverMsg is how finishing a cut-off run went.
	recoverMsg struct {
		said string
		err  error
	}
	// forgetMsg is how forgetting a cut-off run went, and the run it was.
	forgetMsg struct {
		run journal.Entry
		err error
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
	// bugMsg is a bug in the goroutine a run goes in, past the engine's own
	// recovery: the screen ends, and the program says it was a bug.
	bugMsg BugError
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
	// bug is what ended the screen, when a bug did.
	bug *BugError
	th  ui.Theme
	// themeFixed is set when the settings chose dark or light.
	themeFixed bool

	width, height int
	now           time.Time

	info wsl.Info
	// infoKnown is set once WSL's answer arrived: before, no distribution is
	// called one this WSL cannot compact.
	infoKnown bool
	list      []disks.Disk
	err       error
	loaded    bool
	loading   bool

	// sel is the selected row; selName keeps it on the same disk across
	// reloads. marked holds the disks chosen with space, by name.
	sel     int
	selName string
	marked  map[string]bool
	// tableTop is the first disk the table shows when the list is longer
	// than the window; pageTop the first line of the help or the history.
	tableTop int
	pageTop  int

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

	toast    string
	toastCat ui.Category
	// unfinished is a run that has not ended, from the journal: one going
	// on in another process holds shrinking back, one cut off as well,
	// until R finishes it.
	unfinished      journal.Entry
	unfinishedState journal.State
	toastUntil      time.Time
	// settling is set while R or F is at work on the cut-off run: neither
	// starts a second time meanwhile. recoverFailed is set once R could not
	// start again what the run had stopped: from then on F offers to
	// forget the run, until it is no longer cut off.
	settling      bool
	recoverFailed bool
	// reopening is set while a new window is being opened for a run: no
	// second run starts meanwhile.
	reopening bool

	// pending is a run asked for by the options, started after the first
	// load; preparing is set while that run looks inside first, when keys
	// that would start another run are ignored.
	pending   *Options
	preparing bool
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
	end, err := tea.NewProgram(m).Run()
	return ended(end, err)
}

// BugError is the screen ended by a bug in a run's goroutine: what it was
// and where, for a report. Bubble Tea catches the screen's own; this is the
// one goroutine the program starts itself.
type BugError struct {
	Value any
	Stack []byte
}

func (e *BugError) Error() string { return fmt.Sprintf("stopped on a bug: %v", e.Value) }

// ended is how the screen ended: Bubble Tea's error, or the bug that ended
// it, or nil.
func ended(end tea.Model, err error) error {
	if err != nil {
		return err
	}
	if m, ok := end.(model); ok && m.bug != nil {
		return m.bug
	}
	return nil
}

// Init loads the disks and WSL's version, and asks the terminal for its
// background so the colors suit it.
func (m model) Init() tea.Cmd {
	return tea.Batch(tea.RequestBackgroundColor, m.loadDisks(), m.loadInfo(), frameTick())
}

// loadDisks reads the disks in the background.
func (m model) loadDisks() tea.Cmd {
	b, ctx := m.b, m.ctx
	return func() tea.Msg {
		l, err := b.Disks(ctx)
		e, st := b.Pending()
		return disksMsg{list: l, err: err, unfinished: e, unfinishedState: st}
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
		// A WSL that did not answer leaves what it can do unknown: no
		// distribution is held back for it, and the run's own calls say
		// what is wrong.
		m.info, m.infoKnown = wsl.Info(msg), !msg.NoAnswer
		return m, nil
	case disksMsg:
		m.loading, m.loaded = false, true
		m.list, m.err, m.now = msg.list, msg.err, time.Now()
		// Another run than the one R failed on: F waits for R to fail on
		// this one too, and a question about the other closes.
		if !msg.unfinished.Same(m.unfinished) {
			m.recoverFailed = false
			if m.mode == modeForget {
				m.mode = modeMain
			}
		}
		m.unfinished, m.unfinishedState = msg.unfinished, msg.unfinishedState
		// A run no longer cut off has nothing left to forget: the question
		// about it closes, and F is offered again only after R fails anew.
		if m.unfinishedState != journal.CutOff {
			m.recoverFailed = false
			if m.mode == modeForget {
				m.mode = modeMain
			}
		}
		m.hist = m.b.History()
		m.reselect()
		if m.pending != nil && m.err == nil {
			p := m.pending
			m.pending, m.preparing = nil, true
			return m, m.prepareRun(*p)
		}
		// A look inside starts from the disk list only: under an open clean
		// list a new answer would change the rows being chosen from.
		if m.mode != modeMain {
			return m, nil
		}
		return m, m.surveySelected()
	case autoRunMsg:
		m.preparing = false
		if msg.err != nil {
			m.say(msg.err.Error(), ui.CatBad)
			return m, nil
		}
		return m.startRun(msg.queue, msg.clean)
	case reopenMsg:
		// The new window took the run: this one has nothing left to do.
		m.reopening = false
		if msg.err == nil {
			return m, tea.Quit
		}
		m.mode = modeMain
		m.say(i18n.T("toast.reopen.failed", msg.err.Error()), ui.CatBad)
		return m, nil
	case recoverMsg:
		// Said either way; the disks are read again, and with them whether
		// a run is still unfinished.
		m.settling = false
		if msg.err != nil {
			// A start that failed leaves the run cut off: F forgets it from
			// now on. Any other failure is one to wait out or look into.
			var still app.StillCutOff
			if errors.As(msg.err, &still) {
				m.recoverFailed = true
			}
			m.say(oneLine(msg.err.Error()), ui.CatBad)
		} else {
			// A line, as every toast: what was started again. A note the
			// command line adds under it does not fit one.
			said, _, _ := strings.Cut(msg.said, "\n")
			m.say(said, ui.CatDone)
		}
		m.loading = true
		return m, m.loadDisks()
	case forgetMsg:
		m.settling = false
		switch {
		case errors.Is(msg.err, app.ErrNothingToForget):
			// Finished elsewhere while the question stood: nothing holds
			// shrinking back, and nothing was forgotten.
			m.unfinished, m.unfinishedState, m.recoverFailed = journal.Entry{}, journal.None, false
			m.say(i18n.T("toast.forgot.gone"), ui.CatDone)
		case msg.err != nil:
			// Still cut off: R and F stay as they were.
			m.say(oneLine(msg.err.Error()), ui.CatBad)
		default:
			// Closed: nothing holds shrinking back from here on, before
			// the disks are read again as well.
			m.unfinished, m.unfinishedState, m.recoverFailed = journal.Entry{}, journal.None, false
			if msg.run.Disk == "" {
				m.say(i18n.T("toast.forgot.unknown"), ui.CatDone)
			} else {
				m.say(i18n.T("toast.forgot", msg.run.Disk), ui.CatDone)
			}
		}
		m.loading = true
		return m, m.loadDisks()
	case surveyMsg:
		// A reload may have dropped the state this answer was for.
		s := m.surveys[msg.distro]
		if s == nil {
			s = &surveyState{}
			m.surveys[msg.distro] = s
		}
		s.loading, s.r, s.err, s.at = false, msg.r, msg.err, time.Now()
		// The clean list follows what was found: a cursor past its end goes
		// to the last item, and an empty list closes.
		if m.mode == modeClean && m.pickFor == msg.distro {
			if n := len(m.cleanItems()); n == 0 {
				m.mode = modeMain
			} else {
				m.pickSel = min(m.pickSel, n-1)
				m.scrollClean()
			}
		}
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
	case bugMsg:
		bug := BugError(msg)
		m.bug = &bug
		return m, tea.Quit
	case frameMsg:
		// The clock keeps going for the whole session: spinners turn while
		// something loads, a run's times count up, passing messages expire.
		m.now = time.Time(msg)
		m.spin++
		return m, frameTick()
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
	// The table follows the selected disk into view.
	m.tableTop = m.tableWindow(m.tableRows())
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
	m.tableTop = m.tableWindow(m.tableRows())
	return m.surveySelected()
}

// tableRows is how many disks the table shows at most: the rows under its
// heading, less, in a narrow window, room under the table for the selected
// disk's name and verdict.
func (m model) tableRows() int {
	n := m.bodyHeight() - 1
	if m.width < sideBySide {
		n -= 3
	}
	return max(1, n)
}

// tableWindow is the first disk the table shows when n fit: the window moves
// only as far as it must to keep the selected disk in it.
func (m model) tableWindow(n int) int {
	top := min(m.tableTop, m.sel)
	if m.sel >= top+n {
		top = m.sel - n + 1
	}
	return max(0, min(top, len(m.list)-n))
}

// say shows a short message in the status line for a few seconds.
func (m *model) say(text string, c ui.Category) {
	m.toast, m.toastCat, m.toastUntil = text, c, time.Now().Add(5*time.Second)
}

// startRun begins a run of the queue: the engine runs in the background and
// its events come back through a channel, one message at a time.
func (m model) startRun(queue []disks.Disk, clean map[string][]survey.Item) (tea.Model, tea.Cmd) {
	// One run at a time, and never one of nothing.
	if len(queue) == 0 || m.run != nil && !m.run.done {
		m.mode = modeMain
		return m, nil
	}
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
		// A bug past the engine's own recovery (which settles the disk on
		// its own) ends the screen, which then says so, rather than the
		// program with the terminal still the screen's. A run it cut off
		// stays in the journal, for the next start to finish.
		defer func() {
			if r := recover(); r != nil {
				ch <- bugMsg{Value: r, Stack: debug.Stack()}
			}
		}()
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
	// The disk as the run left it, at once: the header counts it so before
	// the disks are read again.
	if res.After != nil {
		m.list = slices.Clone(m.list)
		for i := range m.list {
			if m.list[i].Name == res.Disk {
				m.list[i].Estimate = res.After
				if res.File != nil {
					m.list[i].File = *res.File
				}
			}
		}
	}
	// A disk that was compacted is done with, even when a later step went
	// wrong: marked again, it would be shrunk a second time.
	if res.Err == "" || res.Compacted {
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
		if m.marked[d.Name] && m.block(d) == "" {
			q = append(q, d)
		}
	}
	if len(q) == 0 {
		if d, ok := m.selected(); ok && m.block(d) == "" {
			q = append(q, d)
		}
	}
	return q
}

// block is the first reason a disk cannot be shrunk now, "" when it can:
// the engine's own rule, the one a run's check stops on.
func (m model) block(d disks.Disk) shrink.Block {
	// Until WSL has answered, no distribution is held back for its WSL.
	canCompact := m.info.CanCompact || !m.infoKnown
	if bs := shrink.Preflight(d, canCompact, m.b.Protected(d.Name)); len(bs) > 0 {
		return bs[0]
	}
	return ""
}

// blocked says why a disk cannot be shrunk now, in words, or "" when it can.
func (m model) blocked(d disks.Disk) string {
	return blockWords(d, m.block(d))
}

// blockWords is a reason a disk cannot be shrunk now in the screen's words;
// "" only for no reason at all.
func blockWords(d disks.Disk, b shrink.Block) string {
	switch b {
	case "":
		// Nothing stands in the way.
		return ""
	case shrink.BlockProtected:
		return i18n.T("toast.protected", d.Name)
	case shrink.BlockUnreadable:
		return i18n.T("toast.unreadable", d.Name)
	case shrink.BlockEncrypted:
		return i18n.T("toast.encrypted", d.Name)
	case shrink.BlockFSErrors:
		return i18n.T("toast.fserrors", d.Name)
	case shrink.BlockFixed:
		return i18n.T("toast.fixed", d.Name)
	case shrink.BlockHeld:
		return i18n.T(heldKey(d, "toast.held"), d.Name)
	case shrink.BlockNoCompact:
		return i18n.T("toast.nocompact")
	}
	// A reason the screen has no words for yet is said in the engine's: a
	// disk that cannot be shrunk never reads as one that can.
	return d.Name + ": " + b.Reason(d)
}

// kindOrder is the order of the groups in the clean list: what is rebuilt
// on its own first, history last.
var kindOrder = []survey.Kind{survey.KindCache, survey.KindDownload, survey.KindImage, survey.KindLog}

// cleanItems returns the clean list's items in group order, largest first
// within a group. The cursor (pickSel) is an index into this order.
func (m model) cleanItems() []survey.Item {
	s := m.surveys[m.pickFor]
	if s == nil {
		return nil
	}
	var out []survey.Item
	for _, k := range kindOrder {
		for _, it := range s.r.Items {
			if it.Kind == k {
				out = append(out, it)
			}
		}
	}
	return out
}

// cleanRow is one line of the clean list: a blank line between groups
// (item -2), a group's header (item -1) or an item, by its index in
// cleanItems.
type cleanRow struct {
	kind survey.Kind
	item int
}

// cleanRows lays out the clean list: each group's header, then its items.
func (m model) cleanRows(items []survey.Item) []cleanRow {
	var rows []cleanRow
	for i, it := range items {
		if i == 0 || items[i-1].Kind != it.Kind {
			if i > 0 {
				rows = append(rows, cleanRow{kind: it.Kind, item: -2})
			}
			rows = append(rows, cleanRow{kind: it.Kind, item: -1})
		}
		rows = append(rows, cleanRow{kind: it.Kind, item: i})
	}
	return rows
}

// cleanVisible is how many list rows fit under the clean list's head.
func (m model) cleanVisible() int { return max(1, m.bodyHeight()-len(m.cleanHead())) }

// scrollClean keeps the cursor's row, and its group's header when it fits,
// on screen.
func (m *model) scrollClean() {
	rows := m.cleanRows(m.cleanItems())
	at := slices.IndexFunc(rows, func(r cleanRow) bool { return r.item == m.pickSel })
	if at < 0 {
		return
	}
	// Show the header above the first item of a group with it.
	want := at
	if at > 0 && rows[at-1].item < 0 {
		want = at - 1
	}
	visible := m.cleanVisible()
	if want < m.pickTop {
		m.pickTop = want
	}
	if at >= m.pickTop+visible {
		m.pickTop = at - visible + 1
	}
	m.pickTop = max(0, min(m.pickTop, len(rows)-visible))
}

// choosable reports whether the confirm box offers c: the run is of one
// running distribution, and its look inside found something to choose.
func (m model) choosable(q []disks.Disk) bool {
	if len(q) != 1 || !q[0].Running || q[0].Distro == "" {
		return false
	}
	s := m.surveys[q[0].Distro]
	return s != nil && !s.loading && s.err == nil && len(s.r.Items) > 0
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
	m.info, m.infoKnown = b.Info(ctx), true
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
