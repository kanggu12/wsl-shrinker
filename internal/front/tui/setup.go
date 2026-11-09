package tui

import (
	"cmp"
	"context"
	"errors"
	"image/color"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/kanggu12/wsl-shrinker/internal/core/units"
	"github.com/kanggu12/wsl-shrinker/internal/engine/disks"
	"github.com/kanggu12/wsl-shrinker/internal/engine/shrink"
	"github.com/kanggu12/wsl-shrinker/internal/front/i18n"
	"github.com/kanggu12/wsl-shrinker/internal/front/ui"
)

// SetupStart is what the setup wizard starts from: the settings now, the
// disks found (to choose which to protect), and where the choices go.
type SetupStart struct {
	// Language, Theme, Mode and MinGainGB are the settings now.
	Language, Theme, Mode string
	MinGainGB             float64
	// Protect is the disks protected now, by name.
	Protect []string
	// Disks are the disks found; none when WSL has none yet.
	Disks []disks.Disk
	// AutoLanguage is what "auto" picks here: WSL_SHRINKER_LANG, or
	// Windows' display language.
	AutoLanguage i18n.Lang
	// Path is the settings file the choices are written to.
	Path string
	// First is set on a first run (no settings file yet) and when the
	// installer opened the wizard: esc keeps the defaults then, rather than
	// leave the choice for later.
	First bool
}

// SetupChoice is what the person chose.
type SetupChoice struct {
	Language, Theme, Mode string
	MinGainGB             float64
	Protect               []string
}

// The values each choice offers, in order.
var (
	setupLanguages = []string{"auto", "en", "ko"}
	setupThemes    = []string{"auto", "dark", "light"}
	setupModes     = []string{"tui", "cli"}
	setupGains     = []float64{1, 2, 5, 10, 20, 50}
)

// setupSteps are the wizard's steps after its welcome, as the rail lists and
// counts them.
var setupSteps = []string{"language", "theme", "mode", "gain", "protect", "ready"}

// The steps by number: 0 is the welcome, then setupSteps in order.
const (
	stepWelcome = iota
	stepLanguage
	stepTheme
	stepMode
	stepGain
	stepProtect
	stepReady
)

// setupModel is the setup wizard: a page a choice, the steps on a rail.
type setupModel struct {
	start  SetupStart
	choice SetupChoice
	// step is the page shown; disk is the cursor on the protect page.
	step, disk    int
	width, height int
	th            ui.Theme
	// termDark is the terminal's own background, for colors on "auto".
	termDark bool
	// langBefore is the language to go back to when the wizard is left
	// without saving: choosing a language shows it at once.
	langBefore i18n.Lang
	// saved is set on the last page, cancelled by ctrl+c; the wizard then
	// ends.
	saved, cancelled bool
}

// newSetupModel starts the wizard at its welcome, the choices as the
// settings are now.
func newSetupModel(start SetupStart) setupModel {
	m := setupModel{start: start, termDark: true, langBefore: i18n.Current(),
		choice: SetupChoice{Language: start.Language, Theme: start.Theme, Mode: start.Mode, MinGainGB: start.MinGainGB, Protect: slices.Clone(start.Protect)}}
	m.applyLook()
	return m
}

// ErrSetupCancelled is setup left with ctrl+c: nothing is to be written,
// and nothing is to follow.
var ErrSetupCancelled = errors.New("setup cancelled; nothing was written")

// RunSetup opens the setup wizard. saved is false when it was left with
// esc: nothing was chosen. Ctrl+C, or the program interrupted, is
// ErrSetupCancelled.
func RunSetup(ctx context.Context, start SetupStart) (choice SetupChoice, saved bool, err error) {
	return runSetup(ctx, start)
}

// runSetup is RunSetup with more of the program's options: the tests give
// it keys to read and nowhere to draw.
func runSetup(ctx context.Context, start SetupStart, opts ...tea.ProgramOption) (choice SetupChoice, saved bool, err error) {
	m := newSetupModel(start)
	end, err := tea.NewProgram(m, append([]tea.ProgramOption{tea.WithContext(ctx)}, opts...)...).Run()
	// The language shown while choosing goes back to what it was, unless
	// the choice is saved.
	final, ok := end.(setupModel)
	if !ok || !final.saved || err != nil {
		i18n.Set(m.langBefore)
	}
	switch {
	case errors.Is(err, tea.ErrProgramPanic):
		// A bug, which the program reports as one: Bubble Tea says so as a
		// kill, so this comes first.
		return SetupChoice{}, false, err
	case errors.Is(err, tea.ErrInterrupted) || (err != nil && ctx.Err() != nil):
		// Ctrl+C as a signal, or the program told to end.
		return SetupChoice{}, false, ErrSetupCancelled
	case err != nil:
		return SetupChoice{}, false, err
	case final.cancelled:
		return SetupChoice{}, false, ErrSetupCancelled
	}
	return final.choice, final.saved, nil
}

// SetupSnapshot draws one frame of the wizard after keys, for the tests and
// the README's pictures.
func SetupSnapshot(start SetupStart, w, h int, dark bool, keys []string) string {
	m := newSetupModel(start)
	m.width, m.height, m.termDark = w, h, dark
	m.applyLook()
	for _, k := range keys {
		next, _ := m.Update(snapshotKey(k))
		m = next.(setupModel)
	}
	out := m.View().Content
	if !m.saved {
		i18n.Set(m.langBefore)
	}
	return out
}

// applyLook shows the chosen language and colors at once.
func (m *setupModel) applyLook() {
	switch m.choice.Language {
	case "en":
		i18n.Set(i18n.English)
	case "ko":
		i18n.Set(i18n.Korean)
	default:
		i18n.Set(m.start.AutoLanguage)
	}
	dark := m.termDark
	if m.choice.Theme == "dark" || m.choice.Theme == "light" {
		dark = m.choice.Theme == "dark"
	}
	m.th = ui.NewTheme(dark)
}

func (m setupModel) Init() tea.Cmd { return tea.RequestBackgroundColor }

func (m setupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.BackgroundColorMsg:
		m.termDark = msg.IsDark()
		m.applyLook()
	case tea.KeyPressMsg:
		return m.key(msg.String())
	}
	return m, nil
}

// key answers one key.
func (m setupModel) key(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "esc":
		return m, tea.Quit
	case "ctrl+c":
		// Out, choosing nothing and writing nothing.
		m.cancelled = true
		return m, tea.Quit
	case "enter", "right", "l", "tab":
		// The next page; the last one saves.
		if m.step == stepReady {
			m.saved = true
			return m, tea.Quit
		}
		m.step++
	case "left", "h", "backspace", "shift+tab":
		m.step = max(stepWelcome, m.step-1)
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(+1)
	case "space":
		// On the protect page: protect the disk, or lift the protection.
		if m.step == stepProtect && len(m.start.Disks) > 0 {
			name := m.start.Disks[m.disk].Name
			if i := slices.IndexFunc(m.choice.Protect, func(p string) bool { return strings.EqualFold(p, name) }); i >= 0 {
				m.choice.Protect = slices.Delete(m.choice.Protect, i, i+1)
			} else {
				m.choice.Protect = append(m.choice.Protect, name)
			}
		}
	}
	return m, nil
}

// move moves the cursor of the page: on a choice page the choice itself,
// shown at once; on the protect page, the disk.
func (m *setupModel) move(by int) {
	next := func(list []string, now string) string {
		i := max(0, slices.Index(list, now))
		return list[min(len(list)-1, max(0, i+by))]
	}
	switch m.step {
	case stepLanguage:
		m.choice.Language = next(setupLanguages, m.choice.Language)
	case stepTheme:
		m.choice.Theme = next(setupThemes, m.choice.Theme)
	case stepMode:
		m.choice.Mode = next(setupModes, m.choice.Mode)
	case stepGain:
		gains := m.gains()
		i := max(0, slices.Index(gains, m.choice.MinGainGB))
		m.choice.MinGainGB = gains[min(len(gains)-1, max(0, i+by))]
	case stepProtect:
		m.disk = min(max(0, len(m.start.Disks)-1), max(0, m.disk+by))
	}
	m.applyLook()
}

// gains are the sizes the gain page offers: the steps, and the setting's
// own value among them when it is none of them, there to come back to.
func (m setupModel) gains() []float64 {
	gains := slices.Clone(setupGains)
	if !slices.Contains(gains, m.start.MinGainGB) {
		gains = append(gains, m.start.MinGainGB)
		slices.Sort(gains)
	}
	return gains
}

// protected reports whether the choice protects a disk.
func (m setupModel) protected(name string) bool {
	return slices.ContainsFunc(m.choice.Protect, func(p string) bool { return strings.EqualFold(p, name) })
}

func (m setupModel) View() tea.View {
	// The marks a console's font may not have, swapped once all is laid out.
	v := tea.NewView(ui.Marks(m.render()))
	v.AltScreen = true
	v.WindowTitle = "wsl-shrinker setup"
	return v
}

// The wizard's measures: the rail shows from railFrom columns on, the card
// is at most cardMax wide, and railGap columns lie between the two.
const (
	railFrom = 78
	cardMax  = 74
	railGap  = 3
)

// render draws the wizard in a frame as wide as it needs, in the middle of
// the terminal: the header and the progress on top, the rail of steps
// beside the page's card, the keys at the bottom, all on the same edges.
func (m setupModel) render() string {
	t := m.th
	w, h := m.width, m.height
	if w < 40 || h < 12 {
		return m.fill(wrapLines(i18n.T("small"), w-2, " ", ui.Fg(t.Muted)))
	}
	// The rail, on a wide terminal: its column and the gap after it.
	var rail []string
	railW := 0
	if w >= railFrom {
		rail = m.rail()
		railW = lipgloss.Width(rail[0]) + railGap
	}
	// The frame: the rail and the card, with a column of margin at least
	// on each side; a wider terminal centers it.
	cardW := min(cardMax, w-2-railW)
	frameW := railW + cardW
	left := strings.Repeat(" ", (w-frameW)/2)
	// The header: the name on the left, how far along on the right, both
	// on the frame's edges.
	title := t.Chip("wsl-shrinker") + "  " + lipgloss.NewStyle().Bold(true).Foreground(t.Ink).Render(i18n.T("setup.title"))
	count := ""
	if m.step > stepWelcome {
		count = ui.Fg(t.Muted).Render(i18n.T("setup.count", m.step, len(setupSteps)))
	}
	header := left + title + strings.Repeat(" ", max(1, frameW-lipgloss.Width(title)-lipgloss.Width(count))) + count
	lines := []string{header, left + m.progress(frameW)}
	// The room between the progress and the keys, a blank line above the
	// keys kept; the card takes it all but a blank line under the
	// progress.
	room := h - len(lines) - 2
	// The card's top is where the tallest page's would be in the middle of
	// the room, so the question does not move from page to page.
	tallest := 0
	for step := stepWelcome; step <= stepReady; step++ {
		other := m
		other.step = step
		tallest = max(tallest, len(other.card(cardW, room-1)))
	}
	for range max(1, (room-tallest)/2) {
		lines = append(lines, "")
	}
	// The rail's lines beside the card's, the step now joined to the card
	// across the gap, as a tab of it.
	card := m.card(cardW, room-1)
	for i := range max(len(card), len(rail)) {
		row := ""
		if rail != nil {
			r, gap := strings.Repeat(" ", railW-railGap), strings.Repeat(" ", railGap)
			if i < len(rail) {
				r = rail[i]
			}
			if i == m.step && m.step > stepWelcome && i < len(card) {
				gap = ui.Highlight("", m.panel(), railGap)
			}
			row = r + gap
		}
		if i < len(card) {
			row += card[i]
		}
		lines = append(lines, left+row)
	}
	// The keys on the last line, on the frame's left edge.
	for len(lines) < h-1 {
		lines = append(lines, "")
	}
	lines = append(lines[:h-1], left+m.keys(m.pageKeys(), w-len(left)))
	return m.fill(lines)
}

// progress draws the steps as blocks across w columns, the way the logo
// draws a disk: the steps done in teal, the one now in indigo, the rest on
// the track. The first steps take a block more when w does not divide, so
// the bar ends on the frame's edge.
func (m setupModel) progress(w int) string {
	t := m.th
	n := len(setupSteps)
	per, extra := max(1, (w-(n-1))/n), max(0, (w-(n-1))%n)
	parts := make([]string, n)
	for i := range setupSteps {
		c := t.Track
		switch step := i + 1; {
		case step < m.step:
			c = t.Mark
		case step == m.step:
			c = t.Cat[ui.CatLive]
		}
		k := per
		if i < extra {
			k++
		}
		parts[i] = ui.Fg(c).Render(strings.Repeat(ui.Block, k))
	}
	return strings.Join(parts, " ")
}

// rail lists the steps, a line each under a blank one, so a step sits
// level with the card's question: those done with a check, the one now
// raised like the card and marked, the rest waiting. Every line is as wide
// as the longest.
func (m setupModel) rail() []string {
	t := m.th
	wide := 0
	for _, s := range setupSteps {
		wide = max(wide, lipgloss.Width(i18n.T("setup.row."+s)))
	}
	out := []string{strings.Repeat(" ", wide+4)}
	for i, s := range setupSteps {
		label := ui.Fit(i18n.T("setup.row."+s), wide)
		switch step := i + 1; {
		case step < m.step:
			out = append(out, " "+ui.Fg(t.Mark).Render("✓")+" "+ui.Fg(t.Muted).Render(label)+" ")
		case step == m.step:
			out = append(out, ui.Highlight(" "+ui.Fg(t.Cat[ui.CatLive]).Render(ui.Block)+" "+
				lipgloss.NewStyle().Foreground(t.Ink).Bold(true).Render(label)+" ", m.panel(), wide+4))
		default:
			out = append(out, " "+ui.Fg(t.Faint).Render(ui.Block)+" "+ui.Fg(t.Muted).Render(label)+" ")
		}
	}
	return out
}

// panel is the raised surface of the card and of the step now.
func (m setupModel) panel() color.Color { return m.th.Key }

// sunken is what is set into the panel, the choice and the preview: darker
// than the panel on either background.
func (m setupModel) sunken() color.Color {
	if m.th.Dark {
		return m.th.Selected
	}
	return m.th.Rule
}

// cardLine is a line of the card: its text, whether it is set into the
// panel, and the part of the page it belongs to: 0 for what the page is
// for, more for what may go when the page does not fit, the most first.
// A line that scrolls, a choice, may go from the top so the one chosen
// stays in sight.
type cardLine struct {
	s      string
	sunken bool
	extra  int
	scroll bool
}

// card is the page as lines: a question, what it means and the choice, on
// a raised panel w wide and at most h lines tall.
func (m setupModel) card(w, h int) []string {
	t := m.th
	inner := max(10, w-6)
	bold := lipgloss.NewStyle().Bold(true).Foreground(t.Ink)
	muted := ui.Fg(t.Muted)
	var lines []cardLine
	// add puts lines in the part given; text wraps a paragraph into it.
	add := func(extra int, l ...string) {
		for _, s := range l {
			lines = append(lines, cardLine{s: s, extra: extra})
		}
	}
	text := func(extra int, s string, st lipgloss.Style) { add(extra, wrapLines(s, inner, "", st)...) }
	switch m.step {
	case stepWelcome:
		// Before a first start, or a change of what was chosen then.
		if m.start.First {
			text(0, i18n.T("setup.welcome.title"), bold)
			add(0, "")
			text(0, i18n.T("setup.welcome.first"), muted)
		} else {
			text(0, i18n.T("setup.welcome.title.again"), bold)
			add(0, "")
			text(0, i18n.T("setup.welcome.again"), muted)
		}
		// What it found, and what can come back now, as the screen's
		// header will say it.
		if n := len(m.start.Disks); n > 0 {
			add(1, "", ui.Fg(t.Mark).Render(ui.Block)+" "+lipgloss.NewStyle().Foreground(t.Ink).Render(i18n.TN("setup.welcome.found", n, n)))
			var now int64
			for _, d := range m.start.Disks {
				if m.category(d) == ui.CatGain {
					now += d.Estimate.Reclaim
				}
			}
			if now > 0 {
				add(1, ui.Fg(t.Cat[ui.CatGain]).Render(ui.Block+" "+i18n.T("header.total.now", units.Bytes(now))))
			}
		}
		// The settings as they stand: on a first run, what esc keeps.
		label := i18n.T("setup.welcome.now")
		if m.start.First {
			label = i18n.T("setup.welcome.keeps")
		}
		add(2, "", muted.Render(label))
		add(2, m.summary(inner)...)
	case stepLanguage:
		text(0, i18n.T("setup.q.language"), bold)
		add(0, "")
		var opts [][2]string
		for _, v := range setupLanguages {
			desc := ""
			if v == "auto" {
				desc = i18n.T("setup.language.hint", i18n.T("setup.language."+string(m.start.AutoLanguage)))
			}
			opts = append(opts, [2]string{i18n.T("setup.language." + v), desc})
		}
		lines = append(lines, m.options(opts, slices.Index(setupLanguages, m.choice.Language))...)
	case stepTheme:
		text(0, i18n.T("setup.q.theme"), bold)
		add(0, "")
		opts := [][2]string{
			{i18n.T("setup.theme.auto"), i18n.T("setup.theme.hint")},
			{i18n.T("setup.theme.dark"), i18n.T("setup.theme.desc.dark")},
			{i18n.T("setup.theme.light"), i18n.T("setup.theme.desc.light")},
		}
		lines = append(lines, m.options(opts, slices.Index(setupThemes, m.choice.Theme))...)
	case stepMode:
		text(0, i18n.T("setup.q.mode"), bold)
		add(0, "")
		var opts [][2]string
		for _, v := range setupModes {
			opts = append(opts, [2]string{i18n.T("setup.mode." + v), i18n.T("setup.mode.hint." + v)})
		}
		lines = append(lines, m.options(opts, slices.Index(setupModes, m.choice.Mode))...)
		// The preview goes after the note when the page does not fit.
		add(1, "", muted.Render(i18n.T("setup.preview")))
		lines = append(lines, m.modePreview()...)
		add(2, "")
		text(2, i18n.T("setup.mode.note"), muted)
	case stepGain:
		text(0, i18n.T("setup.q.gain"), bold)
		add(0, "")
		gains := m.gains()
		var opts [][2]string
		for _, g := range gains {
			opts = append(opts, [2]string{gainWords(g) + " GB", ""})
		}
		lines = append(lines, m.options(opts, slices.Index(gains, m.choice.MinGainGB))...)
		add(1, "")
		add(1, m.gainPreview(inner)...)
	case stepProtect:
		text(0, i18n.T("setup.q.protect"), bold)
		add(0, "")
		if len(m.start.Disks) == 0 {
			text(0, i18n.T("setup.disks.none"), muted)
			break
		}
		// What space does: an extra, the key bar says it too, so the disks
		// have the room when there is little.
		text(1, i18n.T("setup.disks"), muted)
		add(1, "")
		// As many disks as fit under the question, the one under the cursor
		// among them, in columns the same for every row.
		cols := m.diskCols(inner)
		fit := max(1, h-2-len(slices.DeleteFunc(slices.Clone(lines), func(l cardLine) bool { return l.extra > 0 })))
		top := max(0, min(m.disk-fit+1, len(m.start.Disks)-fit))
		for i := top; i < len(m.start.Disks) && i < top+fit; i++ {
			lines = append(lines, cardLine{s: m.diskRow(m.start.Disks[i], cols), sunken: i == m.disk})
		}
	case stepReady:
		text(0, i18n.T("setup.q.ready"), bold)
		add(0, "")
		add(0, m.summary(inner)...)
		// Where they go, the path on a line of its own, so a long one
		// breaks no sentence, and broken only after a folder's name, never
		// inside one. Saying where goes whole or not at all: in a window too
		// short for the path, "writes them to" alone says less.
		add(1, "")
		text(1, i18n.T("setup.saves"), muted)
		for _, l := range pathLines(m.start.Path, inner) {
			add(1, lipgloss.NewStyle().Foreground(t.Ink).Render(l))
		}
	}
	// A page taller than the room loses its extras, the last part first;
	// then its bottom, the question and the choice coming first.
	for limit := max(1, h-2); len(lines) > limit; {
		last := 0
		for _, l := range lines {
			last = max(last, l.extra)
		}
		if last == 0 {
			// The choices above the one chosen go first, so it is still
			// drawn; then whatever is below the room.
			for s := slices.IndexFunc(lines, func(l cardLine) bool { return l.sunken }); s >= limit; s-- {
				i := slices.IndexFunc(lines, func(l cardLine) bool { return l.scroll && !l.sunken })
				if i < 0 || i > s {
					break
				}
				lines = slices.Delete(lines, i, i+1)
			}
			lines = lines[:limit]
			break
		}
		lines = slices.DeleteFunc(lines, func(l cardLine) bool { return l.extra == last })
	}
	// On the panel: a blank line above and below, three columns each side;
	// a sunken line is set into it between those sides.
	panel, sunken := m.panel(), m.sunken()
	side := ui.Highlight("", panel, 3)
	blank := ui.Highlight("", panel, inner+6)
	out := []string{blank}
	for _, l := range lines {
		bg := panel
		if l.sunken {
			bg = sunken
		}
		out = append(out, side+ui.Highlight(ui.Fit(l.s, inner), bg, inner)+side)
	}
	return append(out, blank)
}

// options draws a page's choices, one a line: the one chosen set into the
// panel, its mark in the brand color; what each means beside it, in a
// column of its own.
func (m setupModel) options(opts [][2]string, chosen int) []cardLine {
	t := m.th
	labelW := 0
	for _, o := range opts {
		labelW = max(labelW, lipgloss.Width(o[0]))
	}
	var out []cardLine
	for i, o := range opts {
		mark, label := ui.Fg(t.Faint).Render(ui.Block), lipgloss.NewStyle().Foreground(t.Ink).Render(ui.Fit(o[0], labelW))
		if i == chosen {
			mark, label = ui.Fg(t.Mark).Render(ui.Block), lipgloss.NewStyle().Foreground(t.Ink).Bold(true).Render(ui.Fit(o[0], labelW))
		}
		row := " " + mark + " " + label
		if o[1] != "" {
			row += "   " + ui.Fg(t.Muted).Render(o[1])
		}
		out = append(out, cardLine{s: row, sunken: i == chosen, scroll: true})
	}
	return out
}

// shown are the disks the mode's preview draws: those worth shrinking now,
// what they give back most first, two at most.
func (m setupModel) shown() []disks.Disk {
	var out []disks.Disk
	for _, d := range m.start.Disks {
		if m.category(d) == ui.CatGain {
			out = append(out, d)
		}
	}
	slices.SortStableFunc(out, func(a, b disks.Disk) int { return cmp.Compare(b.Estimate.Reclaim, a.Estimate.Reclaim) })
	return out[:min(2, len(out))]
}

// modePreview is what the chosen mode opens, drawn small and set into the
// panel like a screen: the screen, or the table and a command. It is the
// first of the page's extras.
func (m setupModel) modePreview() []cardLine {
	t := m.th
	ds := m.shown()
	nameW := 4
	for _, d := range ds {
		nameW = max(nameW, lipgloss.Width(d.Name))
	}
	// A disk as both draw it: its dot, its name, what it gives back.
	row := func(d disks.Disk) string {
		return t.Dot(ui.CatGain) + " " + lipgloss.NewStyle().Foreground(t.Ink).Render(ui.Fit(d.Name, nameW)) +
			"  " + ui.Fg(t.Cat[ui.CatGain]).Render(ui.FitRight(units.Bytes(d.Estimate.Reclaim), 8))
	}
	var body []string
	if m.choice.Mode == "cli" {
		body = append(body, ui.Fg(t.Muted).Render("$ ")+lipgloss.NewStyle().Foreground(t.Ink).Bold(true).Render("wsl-shrinker"))
		for _, d := range ds {
			body = append(body, row(d))
		}
		if len(ds) > 0 {
			body = append(body, ui.Fg(t.Muted).Render("wsl-shrinker shrink "+ui.ShellArg(ds[0].Name)))
		}
	} else {
		body = append(body, t.Chip("wsl-shrinker"))
		for _, d := range ds {
			body = append(body, row(d)+" "+ui.Fg(t.Cat[ui.CatGain]).Render(strings.Repeat(ui.Block, 3)))
		}
		body = append(body, ui.Fg(t.Muted).Render("s "+i18n.T("key.shrink")+"   q "+i18n.T("key.quit")))
	}
	// A line of air above and below, two columns on the left.
	out := []cardLine{{sunken: true, extra: 1}}
	for _, l := range body {
		out = append(out, cardLine{s: "  " + l, sunken: true, extra: 1})
	}
	return append(out, cardLine{sunken: true, extra: 1})
}

// category is the color the screen gives a disk, by the engine's own rule,
// with the choices so far: protected, in use, cannot, worth it or little.
func (m setupModel) category(d disks.Disk) ui.Category {
	block := shrink.Block("")
	if bs := shrink.Preflight(d, true, m.protected(d.Name)); len(bs) > 0 {
		block = bs[0]
	}
	switch block {
	case "":
		if d.Estimate.Reclaim >= int64(m.choice.MinGainGB*(1<<30)) {
			return ui.CatGain
		}
		return ui.CatSmall
	case shrink.BlockProtected:
		return ui.CatSafe
	case shrink.BlockHeld:
		return ui.CatLive
	}
	return ui.CatBad
}

// gainPreview is the person's disks as the chosen size colors them, under
// how many are worth shrinking: teal when they give back that much or
// more, grey when less; those in use, protected or that cannot be shrunk
// in their own colors, counted in neither.
func (m setupModel) gainPreview(w int) []string {
	t := m.th
	var ds []disks.Disk
	for _, d := range m.start.Disks {
		if d.Estimate != nil && d.Err == "" {
			ds = append(ds, d)
		}
	}
	if len(ds) == 0 {
		return wrapLines(i18n.T("setup.gain.hint"), w, "", ui.Fg(t.Muted))
	}
	worth := 0
	for _, d := range ds {
		if m.category(d) == ui.CatGain {
			worth++
		}
	}
	out := []string{ui.Fg(t.Muted).Render(i18n.TN("setup.gain.count", worth, gainWords(m.choice.MinGainGB)+" GB", worth))}
	nameW := 4
	for _, d := range ds {
		nameW = max(nameW, lipgloss.Width(d.Name))
	}
	// " ▪ ", the name, two spaces, then what a shrink gives back at its
	// longest.
	nameW = min(nameW, max(4, w-5-lipgloss.Width(i18n.T("setup.disk.gives", "1023.9 GB"))))
	// The disks the line above counts come first, each with a check, so it
	// says which they are by more than their color.
	slices.SortStableFunc(ds, func(a, b disks.Disk) int {
		return cmp.Compare(boolRank(m.category(b) == ui.CatGain), boolRank(m.category(a) == ui.CatGain))
	})
	for _, d := range ds[:min(5, len(ds))] {
		c := m.category(d)
		what := i18n.T("setup.disk.gives", units.Bytes(d.Estimate.Reclaim))
		switch c {
		case ui.CatSafe:
			what = i18n.T("setup.disk.protected")
		case ui.CatLive:
			what = i18n.T("state.inuse")
		case ui.CatBad:
			what = i18n.T("setup.disk.cannot")
		}
		mark := t.Dot(c)
		if c == ui.CatGain {
			mark = ui.Fg(t.Cat[c]).Render("✓")
		}
		out = append(out, " "+mark+" "+lipgloss.NewStyle().Foreground(t.Ink).Render(ui.Fit(d.Name, nameW))+
			"  "+ui.Fg(t.Cat[c]).Render(what))
	}
	return out
}

// summary is what will be written: each choice beside its step's name.
func (m setupModel) summary(w int) []string {
	t := m.th
	protect := i18n.T("setup.ready.none")
	if len(m.choice.Protect) > 0 {
		protect = strings.Join(m.choice.Protect, ", ")
	}
	// "auto" says what it picks here.
	language := i18n.T("setup.language." + m.choice.Language)
	if m.choice.Language == "auto" {
		language += " (" + i18n.T("setup.language."+string(m.start.AutoLanguage)) + ")"
	}
	values := []string{
		language,
		i18n.T("setup.theme." + m.choice.Theme),
		i18n.T("setup.mode." + m.choice.Mode),
		gainWords(m.choice.MinGainGB) + " GB",
		protect,
	}
	labelW := 0
	for _, s := range setupSteps[:len(values)] {
		labelW = max(labelW, lipgloss.Width(i18n.T("setup.row."+s)))
	}
	var out []string
	for i, v := range values {
		// A grey dot: a list, with no meaning for a color to carry.
		out = append(out, " "+ui.Fg(t.Muted).Render(ui.Block)+" "+ui.Fg(t.Muted).Render(ui.Fit(i18n.T("setup.row."+setupSteps[i]), labelW))+
			"   "+lipgloss.NewStyle().Foreground(t.Ink).Bold(true).Render(ui.Fit(v, max(4, w-labelW-7))))
	}
	return out
}

// pageKeys are the keys the page takes, the most needed last: they stay
// when the bar is narrow.
func (m setupModel) pageKeys() [][2]string {
	leave := i18n.T("key.quit")
	if m.start.First {
		leave = i18n.T("key.defaults")
	}
	back := [2]string{"←", i18n.T("key.back")}
	switch m.step {
	case stepWelcome:
		return [][2]string{{"esc", leave}, {"⏎", i18n.T("key.start")}}
	case stepProtect:
		return [][2]string{{"esc", leave}, back, {"↑↓", i18n.T("key.choose")}, {"space", i18n.T("key.protect")}, {"⏎", i18n.T("key.next")}}
	case stepReady:
		return [][2]string{{"esc", leave}, back, {"⏎", i18n.T("key.save")}}
	}
	return [][2]string{{"esc", leave}, back, {"↑↓", i18n.T("key.choose")}, {"⏎", i18n.T("key.next")}}
}

// diskCols are the protect page's columns: how wide the names are, and
// whether the files' sizes, and what a shrink gives back, fit beside them.
type diskCols struct {
	name       int
	size, what bool
}

// diskCols fits the protect page's rows into w columns: every name whole
// when there is room, the sizes left out before a name is cut. What a row
// says is counted at its longest, protected or not, so the columns do not
// move as space protects a disk.
func (m setupModel) diskCols(w int) diskCols {
	name := 4
	what := max(lipgloss.Width(i18n.T("setup.disk.protected")), lipgloss.Width(i18n.T("setup.disk.unreadable")))
	for _, d := range m.start.Disks {
		name = max(name, lipgloss.Width(d.Name))
		if d.Estimate != nil {
			what = max(what, lipgloss.Width(i18n.T("setup.disk.gives", units.Bytes(d.Estimate.Reclaim))))
		}
	}
	// A row: " [ ] ▪ ", the name, the size in nine columns, two spaces,
	// and what a shrink gives back. The size goes first; then the name is
	// cut, but not short of twelve columns: what a shrink gives back goes
	// before that, the box and the dot saying what matters here.
	switch {
	case 7+name+9+2+what <= w:
		return diskCols{name: name, size: true, what: true}
	case w-7-2-what >= min(name, 12):
		return diskCols{name: min(name, w-7-2-what), what: true}
	}
	return diskCols{name: min(name, max(4, w-7))}
}

// diskRow is one disk: whether it is protected, its dot in the color the
// choices give it, its name, its size, and what a shrink gives back.
func (m setupModel) diskRow(d disks.Disk, cols diskCols) string {
	t := m.th
	box, cat := "[ ]", m.category(d)
	what := ""
	switch {
	case cat == ui.CatSafe:
		box, what = "[✓]", i18n.T("setup.disk.protected")
	case d.Estimate == nil || d.Err != "":
		what = i18n.T("setup.disk.unreadable")
	case cat == ui.CatLive && d.Distro == "":
		// Docker Desktop's disk while it runs, as step 4 and the screen say:
		// it gives back nothing until the program lets go of it.
		what = i18n.T("state.inuse")
	default:
		what = i18n.T("setup.disk.gives", units.Bytes(d.Estimate.Reclaim))
	}
	row := " " + box + " " + t.Dot(cat) + " " + lipgloss.NewStyle().Foreground(t.Ink).Render(ui.Fit(d.Name, cols.name))
	if cols.size {
		size := ""
		if d.Estimate != nil {
			size = units.Bytes(d.Estimate.FileSize)
		}
		row += ui.Fg(t.Muted).Render(ui.FitRight(size, 9))
	}
	if !cols.what {
		return row
	}
	return row + "  " + ui.Fg(t.Cat[cat]).Render(what)
}

// keys is the key bar in w columns, leaving out the first keys when it
// does not fit.
func (m setupModel) keys(keys [][2]string, w int) string {
	for len(keys) > 1 {
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = m.th.KeyCap(k[0], k[1])
		}
		s := strings.Join(parts, "  ")
		if lipgloss.Width(s) <= w {
			return s
		}
		keys = keys[1:]
	}
	return m.th.KeyCap(keys[0][0], keys[0][1])
}

// fill pads the screen to its size, every line its full width.
func (m setupModel) fill(lines []string) string {
	out := make([]string, m.height)
	for i := range out {
		if i < len(lines) {
			out[i] = ui.Fit(lines[i], m.width)
		} else {
			out[i] = strings.Repeat(" ", m.width)
		}
	}
	return strings.Join(out, "\n")
}

// wrapLines breaks s into lines of at most w cells, each indented.
func wrapLines(s string, w int, indent string, st lipgloss.Style) []string {
	var out []string
	for _, l := range strings.Split(lipgloss.NewStyle().Width(max(10, w)).Render(s), "\n") {
		out = append(out, indent+st.Render(strings.TrimRight(l, " ")))
	}
	return out
}

// gainWords is a number of GB as the settings file writes it.
func gainWords(gb float64) string { return strconv.FormatFloat(gb, 'f', -1, 64) }

// pathLines is a path in lines w cells wide, broken after a separator so
// every folder's name stays whole; a name wider than a line alone is broken
// where it must be.
func pathLines(p string, w int) []string {
	var lines []string
	line := ""
	for _, part := range strings.SplitAfter(p, `\`) {
		if line != "" && lipgloss.Width(line+part) > w {
			lines = append(lines, line)
			line = ""
		}
		line += part
		for lipgloss.Width(line) > w {
			cut := ansi.Truncate(line, w, "")
			lines = append(lines, cut)
			line = line[len(cut):]
		}
	}
	return append(lines, line)
}

// boolRank is 1 for true, for sorting by a test.
func boolRank(b bool) int {
	if b {
		return 1
	}
	return 0
}
