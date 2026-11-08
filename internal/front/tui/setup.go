package tui

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/kanggu12/wsl-shrinker/internal/core/units"
	"github.com/kanggu12/wsl-shrinker/internal/engine/disks"
	"github.com/kanggu12/wsl-shrinker/internal/engine/shrink"
	"github.com/kanggu12/wsl-shrinker/internal/front/i18n"
	"github.com/kanggu12/wsl-shrinker/internal/front/ui"
)

// SetupStart is what the setup screen starts from: the settings now, the
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
	// installer opened the screen: esc keeps the defaults then, rather than
	// leave the choice for later.
	First bool
}

// SetupChoice is what the person chose.
type SetupChoice struct {
	Language, Theme, Mode string
	MinGainGB             float64
	Protect               []string
}

// The values each setting row offers, in order.
var (
	setupLanguages = []string{"auto", "en", "ko"}
	setupThemes    = []string{"auto", "dark", "light"}
	setupModes     = []string{"tui", "cli"}
	setupGains     = []float64{1, 2, 5, 10, 20, 50}
)

// setupRows are the settings rows, top to bottom; the disks follow them.
var setupRows = []string{"language", "theme", "mode", "gain"}

// setupModel is the setup screen.
type setupModel struct {
	start  SetupStart
	choice SetupChoice
	// row is the cursor: a settings row, then one row a disk.
	row           int
	width, height int
	th            ui.Theme
	// termDark is the terminal's own background, for colors on "auto".
	termDark bool
	// langBefore is the language to go back to when the screen is left
	// without saving: changing the language row shows it at once.
	langBefore i18n.Lang
	// saved is set by enter, cancelled by ctrl+c; the screen then ends.
	saved, cancelled bool
	// top is the first disk row on screen when they do not all fit.
	top int
}

// newSetupModel starts the screen at the settings now.
func newSetupModel(start SetupStart) setupModel {
	m := setupModel{start: start, termDark: true, langBefore: i18n.Current(),
		choice: SetupChoice{Language: start.Language, Theme: start.Theme, Mode: start.Mode, MinGainGB: start.MinGainGB, Protect: slices.Clone(start.Protect)}}
	m.applyLook()
	return m
}

// ErrSetupCancelled is setup left with ctrl+c: nothing is to be written,
// and nothing is to follow.
var ErrSetupCancelled = errors.New("setup cancelled; nothing was written")

// RunSetup opens the setup screen. saved is false when it was left with
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

// SetupSnapshot draws one frame of the setup screen after keys, for the
// tests and the README's pictures.
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

// last is the cursor's last row.
func (m setupModel) last() int { return len(setupRows) + len(m.start.Disks) - 1 }

// key answers one key.
func (m setupModel) key(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "esc":
		return m, tea.Quit
	case "ctrl+c":
		// Out, choosing nothing and writing nothing.
		m.cancelled = true
		return m, tea.Quit
	case "enter":
		m.saved = true
		return m, tea.Quit
	case "up", "k":
		m.row = max(0, m.row-1)
	case "down", "j", "tab":
		m.row = min(m.last(), m.row+1)
	case "left", "h":
		m.change(-1)
	case "right", "l":
		m.change(+1)
	case "space":
		// On a disk: protect it, or lift the protection; on a setting, the
		// next value, as → does.
		if m.row >= len(setupRows) {
			name := m.start.Disks[m.row-len(setupRows)].Name
			if i := slices.IndexFunc(m.choice.Protect, func(p string) bool { return strings.EqualFold(p, name) }); i >= 0 {
				m.choice.Protect = slices.Delete(m.choice.Protect, i, i+1)
			} else {
				m.choice.Protect = append(m.choice.Protect, name)
			}
		} else {
			m.change(+1)
		}
	}
	return m, nil
}

// change moves the selected setting to its next or previous value, round.
func (m *setupModel) change(step int) {
	round := func(list []string, now string) string {
		i := slices.Index(list, now)
		return list[(max(i, 0)+step+len(list))%len(list)]
	}
	if m.row >= len(setupRows) {
		return
	}
	switch setupRows[m.row] {
	case "language":
		m.choice.Language = round(setupLanguages, m.choice.Language)
	case "theme":
		m.choice.Theme = round(setupThemes, m.choice.Theme)
	case "mode":
		m.choice.Mode = round(setupModes, m.choice.Mode)
	case "gain":
		// The steps, and the setting's own value among them when it is none
		// of them: it stays there to come back to.
		gains := slices.Clone(setupGains)
		if !slices.Contains(gains, m.start.MinGainGB) {
			gains = append(gains, m.start.MinGainGB)
			slices.Sort(gains)
		}
		i := slices.Index(gains, m.choice.MinGainGB)
		m.choice.MinGainGB = gains[(i+step+len(gains))%len(gains)]
	}
	m.applyLook()
}

// protected reports whether the choice protects a disk.
func (m setupModel) protected(name string) bool {
	return slices.ContainsFunc(m.choice.Protect, func(p string) bool { return strings.EqualFold(p, name) })
}

// value is a setting row's value in words.
func (m setupModel) value(row string) string {
	switch row {
	case "language":
		return i18n.T("setup.language." + m.choice.Language)
	case "theme":
		return i18n.T("setup.theme." + m.choice.Theme)
	case "mode":
		return i18n.T("setup.mode." + m.choice.Mode)
	}
	// A whole number of GB reads as one: "5 GB", not "5.0 GB".
	return gainWords(m.choice.MinGainGB) + " GB"
}

// widths are the label and value columns, as wide as their longest words
// in this language and a gap: the hints keep the room that is left.
func (m setupModel) widths() (int, int) {
	labelW, valueW := 0, 0
	for _, r := range setupRows {
		labelW = max(labelW, lipgloss.Width(i18n.T("setup.row."+r)))
	}
	// Every value a row can take, and a gain from the settings that is none
	// of the steps.
	var values []string
	for _, v := range setupLanguages {
		values = append(values, i18n.T("setup.language."+v))
	}
	for _, v := range setupThemes {
		values = append(values, i18n.T("setup.theme."+v))
	}
	for _, v := range setupModes {
		values = append(values, i18n.T("setup.mode."+v))
	}
	// The setting's own gain stays among the values, chosen or not, so the
	// column keeps its width while the values go round.
	for _, g := range append(slices.Clone(setupGains), m.start.MinGainGB, m.choice.MinGainGB) {
		values = append(values, gainWords(g)+" GB")
	}
	for _, v := range values {
		valueW = max(valueW, lipgloss.Width("‹ "+v+" ›"))
	}
	return labelW + 3, valueW + 3
}

// hint says what a setting row's value means.
func (m setupModel) hint(row string) string {
	switch row {
	case "language":
		if m.choice.Language == "auto" {
			return i18n.T("setup.language.hint", i18n.T("setup.language."+string(m.start.AutoLanguage)))
		}
	case "theme":
		if m.choice.Theme == "auto" {
			return i18n.T("setup.theme.hint")
		}
	case "mode":
		return i18n.T("setup.mode.hint." + m.choice.Mode)
	case "gain":
		return i18n.T("setup.gain.hint")
	}
	return ""
}

func (m setupModel) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "wsl-shrinker setup"
	return v
}

// render draws the screen.
func (m setupModel) render() string {
	t := m.th
	w, h := m.width, m.height
	if w < 40 || h < 12 {
		return m.fill([]string{ui.Fg(t.Muted).Render(i18n.T("small"))})
	}
	header := " " + t.Chip("wsl-shrinker") + "  " + lipgloss.NewStyle().Bold(true).Foreground(t.Ink).Render(i18n.T("setup.title"))
	// The first run says so, and that leaving keeps the defaults.
	intro := i18n.T("setup.intro")
	if m.start.First {
		intro = i18n.T("setup.intro.first")
	}
	introLines := wrapLines(intro, w-2, " ", ui.Fg(t.Muted))
	// Above the disks: the header, the intro, the settings and the disks'
	// title, a blank line between them; below, a blank line, the status and
	// the keys. On a short terminal the intro gives way to the disks: the
	// one the cursor is on must show.
	above := 2 + len(introLines) + 1 + len(setupRows) + 1 + 1
	if len(m.start.Disks) > 0 && h-above-3 < 1 {
		introLines = nil
	}
	lines := []string{header, ""}
	if len(introLines) > 0 {
		lines = append(lines, introLines...)
		lines = append(lines, "")
	}
	// The settings: a label, the value between arrows, what it means.
	labelW, valueW := m.widths()
	hints := w >= 70
	for i, r := range setupRows {
		label := ui.Fit(i18n.T("setup.row."+r), labelW)
		value := ui.Fit("‹ "+m.value(r)+" ›", valueW)
		row := "   " + lipgloss.NewStyle().Foreground(t.Ink).Render(label) + lipgloss.NewStyle().Bold(true).Foreground(t.Ink).Render(value)
		if hints {
			row += " " + ui.Fg(t.Muted).Render(m.hint(r))
		}
		lines = append(lines, m.line(row, i == m.row))
	}
	lines = append(lines, "")
	// The disks, to protect; the colors as the choices would show them.
	if len(m.start.Disks) == 0 {
		lines = append(lines, wrapLines(i18n.T("setup.disks.none"), w-2, " ", ui.Fg(t.Muted))...)
	} else {
		lines = append(lines, " "+lipgloss.NewStyle().Bold(true).Foreground(t.Muted).Render(i18n.T("setup.disks")))
		room := h - len(lines) - 3
		top := min(m.top, len(m.start.Disks)-1)
		sel := m.row - len(setupRows)
		if sel >= 0 {
			top = min(top, sel)
			if sel >= top+room {
				top = sel - room + 1
			}
		}
		top = max(0, min(top, len(m.start.Disks)-room))
		for i := top; i < len(m.start.Disks) && i < top+room; i++ {
			lines = append(lines, m.line(m.diskRow(m.start.Disks[i], w), m.row == len(setupRows)+i))
		}
	}
	for len(lines) < h-2 {
		lines = append(lines, "")
	}
	// Where the choices go, then the keys.
	status := " " + ui.Fg(t.Muted).Render(ui.Middle(i18n.T("setup.saves", m.start.Path), w-2))
	quit := i18n.T("key.quit")
	if m.start.First {
		quit = i18n.T("key.defaults")
	}
	keys := [][2]string{{"↑↓", i18n.T("key.select")}, {"←→", i18n.T("key.change")}, {"space", i18n.T("key.protect")}, {"⏎", i18n.T("key.save")}, {"esc", quit}}
	lines = append(lines[:h-2], status, m.keys(keys))
	return m.fill(lines)
}

// diskRow is one disk: whether it is protected, its dot in the color the
// choices give it, its name, its size, and what a shrink gives back.
func (m setupModel) diskRow(d disks.Disk, w int) string {
	t := m.th
	box, cat := "[ ]", ui.CatSmall
	what := ""
	// The colors the screen gives the disk, by the engine's own rule, as the
	// choices so far would make them; WSL's version is not asked here.
	block := shrink.Block("")
	if bs := shrink.Preflight(d, true, m.protected(d.Name)); len(bs) > 0 {
		block = bs[0]
	}
	switch block {
	case shrink.BlockProtected:
		box, cat, what = "[✓]", ui.CatSafe, i18n.T("setup.disk.protected")
	case shrink.BlockUnreadable:
		cat, what = ui.CatBad, i18n.T("setup.disk.unreadable")
	default:
		switch {
		case block == shrink.BlockHeld:
			// In use by its program: it can be shrunk once that stops.
			cat = ui.CatLive
		case block != "":
			cat = ui.CatBad
		case d.Estimate.Reclaim >= int64(m.choice.MinGainGB*(1<<30)):
			cat = ui.CatGain
		}
		what = i18n.T("setup.disk.gives", units.Bytes(d.Estimate.Reclaim))
	}
	size := ""
	if d.Estimate != nil {
		size = units.Bytes(d.Estimate.FileSize)
	}
	nameW := min(28, max(12, w-46))
	return "   " + box + " " + t.Dot(cat) + " " + lipgloss.NewStyle().Foreground(t.Ink).Render(ui.Fit(d.Name, nameW)) +
		ui.Fg(t.Muted).Render(ui.FitRight(size, 9)) + "  " + ui.Fg(t.Cat[cat]).Render(what)
}

// line marks the selected row with the selection's background.
func (m setupModel) line(row string, selected bool) string {
	if !selected {
		return row
	}
	return ui.Highlight(row, m.th.Selected, m.width)
}

// keys is the key bar, leaving out the first keys when it does not fit.
func (m setupModel) keys(keys [][2]string) string {
	for len(keys) > 1 {
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = m.th.KeyCap(k[0], k[1])
		}
		s := " " + strings.Join(parts, "  ")
		if lipgloss.Width(s) <= m.width {
			return s
		}
		keys = keys[1:]
	}
	return " " + m.th.KeyCap(keys[0][0], keys[0][1])
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
