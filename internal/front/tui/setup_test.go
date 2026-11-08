package tui

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/charmbracelet/x/ansi"

	"github.com/kanggu12/wsl-shrinker/internal/engine/disks"
	"github.com/kanggu12/wsl-shrinker/internal/engine/estimate"
	"github.com/kanggu12/wsl-shrinker/internal/front/i18n"
	"github.com/kanggu12/wsl-shrinker/internal/machine/winapi"
)

// setupStart is the wizard on two disks, at the defaults, Work protected
// in lower case as a hand-written file may have it.
func setupStart(first bool) SetupStart {
	gb := int64(1 << 30)
	return SetupStart{Language: "auto", Theme: "auto", Mode: "tui", MinGainGB: 1, Protect: []string{"work"},
		Disks: []disks.Disk{
			{Name: "Dev", Kind: disks.KindDistro, Distro: "Dev", Estimate: &estimate.Result{FileSize: 40 * gb, After: 20 * gb, Reclaim: 20 * gb}},
			{Name: "Work", Kind: disks.KindDistro, Distro: "Work", Estimate: &estimate.Result{FileSize: 9 * gb, After: 8 * gb, Reclaim: gb}},
		},
		AutoLanguage: i18n.English, Path: `C:\Users\ann\AppData\Roaming\wsl-shrinker\config.toml`, First: first}
}

// press sends keys to the wizard.
func press(m setupModel, keys ...string) setupModel {
	for _, k := range keys {
		next, _ := m.key(k)
		m = next.(setupModel)
	}
	return m
}

// to is the wizard moved on to a step with enter, from its welcome.
func to(m setupModel, step int) setupModel {
	for m.step < step {
		m = press(m, "enter")
	}
	return m
}

// frame is one frame of the wizard after keys, its colors taken out.
func frame(start SetupStart, w, h int, keys ...string) string {
	return ansi.Strip(SetupSnapshot(start, w, h, true, keys))
}

// lineWith reports whether a line of the frame holds all of words.
func lineWith(frame string, words ...string) bool {
	return slices.ContainsFunc(strings.Split(frame, "\n"), func(l string) bool {
		return !slices.ContainsFunc(words, func(w string) bool { return !strings.Contains(l, w) })
	})
}

// enters is n presses of enter, as SetupSnapshot takes keys.
func enters(n int) []string {
	return slices.Repeat([]string{"enter"}, n)
}

func TestTheWizardGoesAStepAtATime(t *testing.T) {
	t.Cleanup(func() { i18n.Set(i18n.English) })
	m := newSetupModel(setupStart(false))
	// Every key that moves on does, a step each.
	for i, k := range []string{"enter", "right", "l", "tab"} {
		if m = press(m, k); m.step != i+1 {
			t.Fatalf("%s: step %d", k, m.step)
		}
	}
	// Every key that goes back does, and not past the welcome.
	for _, k := range []string{"left", "h", "backspace", "shift+tab", "left"} {
		m = press(m, k)
	}
	if m.step != stepWelcome || m.saved {
		t.Fatalf("step %d, saved %v", m.step, m.saved)
	}
	// Enter on every page but the last moves on; on the last it saves.
	m = to(m, stepReady)
	if m.saved {
		t.Fatal("saved before the last page")
	}
	if m = press(m, "enter"); !m.saved || m.cancelled {
		t.Fatalf("saved %v, cancelled %v", m.saved, m.cancelled)
	}
}

func TestUpAndDownChangeTheChoiceAtOnce(t *testing.T) {
	t.Cleanup(func() { i18n.Set(i18n.English) })
	m := to(newSetupModel(setupStart(false)), stepLanguage)
	// Language: auto, English, then Korean, shown at once; no further.
	if m = press(m, "down"); m.choice.Language != "en" || i18n.Current() != i18n.English {
		t.Fatalf("%q, shown in %v", m.choice.Language, i18n.Current())
	}
	if m = press(m, "j", "down"); m.choice.Language != "ko" || i18n.Current() != i18n.Korean {
		t.Fatalf("%q, shown in %v", m.choice.Language, i18n.Current())
	}
	if m = press(m, "up", "k"); m.choice.Language != "auto" {
		t.Fatalf("%q", m.choice.Language)
	}
	// Colors: dark and light, drawn in them at once.
	m = press(m, "enter", "down")
	if m.choice.Theme != "dark" || !m.th.Dark {
		t.Fatalf("%q, dark %v", m.choice.Theme, m.th.Dark)
	}
	if m = press(m, "down"); m.choice.Theme != "light" || m.th.Dark {
		t.Fatalf("%q, dark %v", m.choice.Theme, m.th.Dark)
	}
	// Mode: the screen or the command line.
	if m = press(m, "enter", "down"); m.choice.Mode != "cli" {
		t.Fatalf("%q", m.choice.Mode)
	}
	// Size: the steps in order, the last one the end.
	m = press(m, "enter", "down", "down", "down", "down", "down", "down")
	if m.choice.MinGainGB != 50 {
		t.Fatalf("%v", m.choice.MinGainGB)
	}
}

func TestTheGainPageKeepsTheSettingsOwnValueToComeBackTo(t *testing.T) {
	// 3.5 GB is none of the steps: it is offered among them, where it
	// falls, chosen, and stays there when another is.
	start := setupStart(false)
	start.MinGainGB = 3.5
	m := to(newSetupModel(start), stepGain)
	if m = press(m, "down"); m.choice.MinGainGB != 5 {
		t.Fatalf("%v", m.choice.MinGainGB)
	}
	if m = press(m, "up"); m.choice.MinGainGB != 3.5 {
		t.Fatalf("%v", m.choice.MinGainGB)
	}
	if f := frame(start, 100, 26, enters(stepGain)...); !strings.Contains(f, "2 GB") || !strings.Contains(f, "3.5 GB") {
		t.Fatalf("\n%s", f)
	}
}

func TestSpaceProtectsADiskByName(t *testing.T) {
	m := newSetupModel(setupStart(false))
	// Space on any other page does nothing.
	if m = press(m, "enter", "space"); len(m.choice.Protect) != 1 {
		t.Fatalf("%v", m.choice.Protect)
	}
	// On the protect page it protects the disk under the cursor.
	m = press(to(m, stepProtect), "space")
	if strings.Join(m.choice.Protect, ",") != "work,Dev" {
		t.Fatalf("%v", m.choice.Protect)
	}
	// Work was protected as "work": the same disk, so space lifts it.
	m = press(m, "j", "space")
	if strings.Join(m.choice.Protect, ",") != "Dev" {
		t.Fatalf("%v", m.choice.Protect)
	}
	// The cursor stops at the last disk.
	if m = press(m, "down", "down"); m.disk != 1 {
		t.Fatalf("disk %d", m.disk)
	}
}

func TestEscKeepsWhatWasThereAndCtrlCCancels(t *testing.T) {
	t.Cleanup(func() { i18n.Set(i18n.English) })
	// Esc on any page leaves with nothing saved, whatever was chosen.
	for step := stepWelcome; step <= stepReady; step++ {
		m := press(to(newSetupModel(setupStart(false)), step), "down", "esc")
		if m.saved || m.cancelled {
			t.Fatalf("step %d: saved %v, cancelled %v", step, m.saved, m.cancelled)
		}
	}
	// Ctrl+C cancels: nothing saved, and nothing is to follow.
	if m := press(to(newSetupModel(setupStart(true)), stepReady), "ctrl+c"); m.saved || !m.cancelled {
		t.Fatalf("ctrl+c: saved %v, cancelled %v", m.saved, m.cancelled)
	}
}

func TestTheWizardSaysWhatLeavingDoes(t *testing.T) {
	t.Cleanup(func() { i18n.Set(i18n.English) })
	// On a first start esc keeps the settings shown; later it quits,
	// changing nothing.
	for _, c := range []struct {
		first bool
		want  []string
	}{
		{true, []string{"Before you start", "Five quick choices", "Esc keeps these:", "esc  keep the defaults", "⏎  start"}},
		{false, []string{"Change the settings", "The five choices of the first start", "Now:", "esc  quit", "⏎  start"}},
	} {
		f := frame(setupStart(c.first), 100, 26)
		for _, w := range c.want {
			if !strings.Contains(f, w) {
				t.Errorf("no %q in:\n%s", w, f)
			}
		}
	}
	// The last page says where the choices go, and that enter saves.
	f := frame(setupStart(true), 100, 26, enters(stepReady)...)
	for _, w := range []string{"Save these choices?", "⏎ writes them to", `C:\Users\ann\AppData\Roaming\wsl-shrinker\config.toml`, "⏎  save"} {
		if !strings.Contains(f, w) {
			t.Errorf("no %q in:\n%s", w, f)
		}
	}
}

func TestTheSummarySaysEveryChoice(t *testing.T) {
	t.Cleanup(func() { i18n.Set(i18n.English) })
	// What the welcome shows as it stands, and the last page as chosen:
	// "auto" with what it picks, the protected disks, a size as written.
	f := frame(setupStart(false), 100, 26)
	for _, w := range []string{"Language          auto (English)", "Colors            auto", "Mode              TUI",
		"Worth shrinking   1 GB", "Protected disks   work"} {
		if !strings.Contains(f, w) {
			t.Errorf("no %q in:\n%s", w, f)
		}
	}
	// English, dark, CLI, 5 GB and both disks protected.
	keys := append(enters(1), "down", "enter", "down", "enter", "down", "enter", "down", "down", "enter", "space", "enter")
	f = frame(setupStart(false), 100, 26, keys...)
	for _, w := range []string{"Language          English", "Colors            dark", "Mode              CLI",
		"Worth shrinking   5 GB", "Protected disks   work, Dev"} {
		if !strings.Contains(f, w) {
			t.Errorf("no %q in:\n%s", w, f)
		}
	}
}

func TestTheWelcomeSaysWhatTheScreenWillCount(t *testing.T) {
	t.Cleanup(func() { i18n.Set(i18n.English) })
	// Dev gives back 20 GB; Work is protected: the screen's header counts
	// Dev alone, and so does the welcome.
	f := frame(setupStart(true), 100, 26)
	if !strings.Contains(f, "2 WSL disks found") || !strings.Contains(f, "20.0 GB can be given back now") {
		t.Fatalf("\n%s", f)
	}
	// No disk: nothing to count, and no count said.
	start := setupStart(true)
	start.Disks = nil
	if f := frame(start, 100, 26); strings.Contains(f, "found") || strings.Contains(f, "given back") {
		t.Fatalf("\n%s", f)
	}
}

func TestTheGainPreviewColorsTheDisksAsTheScreenWill(t *testing.T) {
	t.Cleanup(func() { i18n.Set(i18n.English) })
	gb := int64(1 << 30)
	start := setupStart(false)
	// Docker Desktop's disk in use, and a disk that was not read.
	start.Disks = append(start.Disks,
		disks.Disk{Name: "Docker Desktop data", Kind: disks.KindDocker, File: winapi.FileInfo{Held: true},
			Estimate: &estimate.Result{FileSize: 30 * gb, Reclaim: 9 * gb}},
		disks.Disk{Name: "Broken", Kind: disks.KindDistro, Distro: "Broken", Err: "cannot open"})
	// At 1 GB: Dev is worth it; Work is protected; Docker's is in use; the
	// one not read is not listed.
	f := frame(start, 100, 26, enters(stepGain)...)
	if !strings.Contains(f, "At 1 GB, 1 disk is worth shrinking:") {
		t.Errorf("no count in:\n%s", f)
	}
	for name, says := range map[string]string{"Dev": "gives back 20.0 GB", "Work": "protected", "Docker Desktop data": "in use"} {
		if !lineWith(f, " "+name+" ", says) {
			t.Errorf("%s does not say %q in:\n%s", name, says, f)
		}
	}
	if strings.Contains(f, "Broken") {
		t.Errorf("a disk not read is listed:\n%s", f)
	}
	// At 50 GB none is.
	if f := frame(start, 100, 26, append(enters(stepGain), slices.Repeat([]string{"down"}, 5)...)...); !strings.Contains(f, "At 50 GB, 0 disks are worth shrinking:") {
		t.Fatalf("\n%s", f)
	}
}

func TestTheModePreviewShowsWhatEachModeOpens(t *testing.T) {
	t.Cleanup(func() { i18n.Set(i18n.English) })
	start := setupStart(false)
	start.Disks[0].Name = "My Dev"
	// The screen: its name and its keys.
	f := frame(start, 100, 26, enters(stepMode)...)
	if !strings.Contains(f, "s shrink   q quit") || !strings.Contains(f, "My Dev") {
		t.Fatalf("\n%s", f)
	}
	// The command line: the table and a command to type, the name quoted
	// as a shell takes it.
	f = frame(start, 100, 26, append(enters(stepMode), "down")...)
	if !strings.Contains(f, "$ wsl-shrinker") || !strings.Contains(f, "wsl-shrinker shrink 'My Dev'") {
		t.Fatalf("\n%s", f)
	}
}

func TestTheLanguageShownWhileChoosingGoesBack(t *testing.T) {
	// Korean shows at once while it is chosen, and a frame left without
	// saving puts the language back.
	t.Cleanup(func() { i18n.Set(i18n.English) })
	i18n.Set(i18n.English)
	f := frame(setupStart(false), 100, 26, "enter", "down", "down")
	if !strings.Contains(f, "어떤 언어로 볼까요?") || i18n.Current() != i18n.English {
		t.Fatalf("shown in %v:\n%s", i18n.Current(), f)
	}
}

func TestEveryPageFitsItsTerminal(t *testing.T) {
	// On every page, in both languages, from the smallest terminal the
	// wizard draws in to a wide one: as many lines as the terminal has,
	// none wider, the question always there, and nothing cut where there
	// is room.
	t.Cleanup(func() { i18n.Set(i18n.English) })
	questions := []string{"Before you start", "Which language?", "Which colors?", "What should wsl-shrinker open?",
		"From what size is a disk worth shrinking?", "Any disks never to shrink?", "Save these choices?"}
	for _, size := range [][2]int{{140, 40}, {120, 30}, {100, 26}, {80, 24}, {60, 16}, {40, 12}} {
		for _, lang := range []string{"auto", "ko"} {
			for step := stepWelcome; step <= stepReady; step++ {
				start := setupStart(true)
				start.Language = lang
				f := frame(start, size[0], size[1], enters(step)...)
				lines := strings.Split(f, "\n")
				if len(lines) != size[1] {
					t.Fatalf("%v %s step %d: %d lines", size, lang, step, len(lines))
				}
				for _, l := range lines {
					if w := ansi.StringWidth(l); w != size[0] {
						t.Errorf("%v %s step %d: %d cells: %q", size, lang, step, w, l)
					}
					if size[0] >= 80 && strings.Contains(l, "…") {
						t.Errorf("%v %s step %d: cut: %q", size, lang, step, l)
					}
				}
				if lang == "auto" && !strings.Contains(strings.Join(strings.Fields(f), " "), questions[step]) {
					t.Errorf("%v step %d: no question in:\n%s", size, step, f)
				}
			}
		}
	}
}

func TestATooShortPageLosesItsExtrasWhole(t *testing.T) {
	t.Cleanup(func() { i18n.Set(i18n.English) })
	// 60x16 holds the welcome's text and count, not the settings under
	// them: those go whole, not their first lines alone.
	f := frame(setupStart(true), 60, 16)
	if !strings.Contains(f, "2 WSL disks found") || strings.Contains(f, "Esc keeps these:") || strings.Contains(f, "Language") {
		t.Fatalf("\n%s", f)
	}
	// The mode page keeps its choices; the preview and the note go.
	f = frame(setupStart(true), 60, 16, enters(stepMode)...)
	if !strings.Contains(f, "a screen like this one") || strings.Contains(f, "Preview") || strings.Contains(f, "either way") {
		t.Fatalf("\n%s", f)
	}
	// Taller, they are all there.
	f = frame(setupStart(true), 80, 24, enters(stepMode)...)
	if !strings.Contains(f, "Preview") || !strings.Contains(f, "either way") {
		t.Fatalf("\n%s", f)
	}
}

func TestTheDiskUnderTheCursorShowsOnAShortTerminal(t *testing.T) {
	t.Cleanup(func() { i18n.Set(i18n.English) })
	// Ten disks on 60x16: the list scrolls so the cursor's disk is drawn.
	start := setupStart(false)
	gb := int64(1 << 30)
	start.Disks = nil
	for _, name := range []string{"d0", "d1", "d2", "d3", "d4", "d5", "d6", "d7", "d8", "last"} {
		start.Disks = append(start.Disks, disks.Disk{Name: name, Kind: disks.KindDistro, Distro: name,
			Estimate: &estimate.Result{FileSize: 9 * gb, Reclaim: gb}})
	}
	f := frame(start, 60, 16, append(enters(stepProtect), slices.Repeat([]string{"down"}, 9)...)...)
	if !strings.Contains(f, "last") || strings.Contains(f, "d0 ") {
		t.Fatalf("\n%s", f)
	}
}

func TestTheCardKeepsABlankLineAboveAndBelow(t *testing.T) {
	t.Cleanup(func() { i18n.Set(i18n.English) })
	// However tall the page and the terminal, a blank line parts the card
	// from the progress and from the keys.
	for h := 12; h <= 30; h++ {
		for step := stepWelcome; step <= stepReady; step++ {
			lines := strings.Split(frame(setupStart(true), 100, h, enters(step)...), "\n")
			if strings.TrimSpace(lines[2]) != "" || strings.TrimSpace(lines[h-2]) != "" {
				t.Fatalf("100x%d step %d:\n%s", h, step, strings.Join(lines, "\n"))
			}
		}
	}
}

func TestTheChoiceStaysInSightOnTheSmallestTerminal(t *testing.T) {
	t.Cleanup(func() { i18n.Set(i18n.English) })
	// 40x12 holds three sizes under the question: going down to 50 GB,
	// the sizes above it give way.
	f := frame(setupStart(false), 40, 12, append(enters(stepGain), slices.Repeat([]string{"down"}, 5)...)...)
	if !strings.Contains(f, "50 GB") || strings.Contains(f, " 1 GB") {
		t.Fatalf("\n%s", f)
	}
	// The disks keep their names whole there, what each gives back left
	// out for them.
	start := setupStart(false)
	start.Disks[0].Name = "Docker Desktop data"
	if f := frame(start, 40, 12, enters(stepProtect)...); !strings.Contains(f, "[ ] ▪ Docker Desktop data") {
		t.Fatalf("\n%s", f)
	}
}

func TestTheProtectPageColumnsStayAsADiskIsProtected(t *testing.T) {
	t.Cleanup(func() { i18n.Set(i18n.English) })
	// Protecting Dev changes what its row says, not where the rows'
	// columns are.
	col := func(keys ...string) int {
		f := frame(setupStart(false), 100, 26, append(enters(stepProtect), keys...)...)
		for _, l := range strings.Split(f, "\n") {
			if i := strings.Index(l, " Work "); i >= 0 {
				return strings.Index(l[i:], "protected")
			}
		}
		return -1
	}
	if before, after := col(), col("space"); before < 0 || before != after {
		t.Fatalf("column %d, then %d", before, after)
	}
}

func TestTheQuestionStaysPutFromPageToPage(t *testing.T) {
	t.Cleanup(func() { i18n.Set(i18n.English) })
	// The card's top does not move as the pages go by; the rail's step now
	// is level with its line of the card, the first with the question.
	at := -1
	for step := stepWelcome; step <= stepReady; step++ {
		lines := strings.Split(frame(setupStart(true), 100, 26, enters(step)...), "\n")
		q := slices.IndexFunc(lines, func(l string) bool {
			return strings.Contains(l, "Before you start") || strings.Contains(l, "?")
		})
		if at == -1 {
			at = q
		}
		if q != at {
			t.Fatalf("step %d: question on line %d, not %d", step, q, at)
		}
		if step > stepWelcome {
			label := []string{"Language", "Colors", "Mode", "Worth shrinking", "Protected disks", "Ready"}[step-1]
			if !strings.Contains(lines[at+step-1], " "+label+" ") {
				t.Fatalf("step %d: %q is not on line %d:\n%s", step, label, at+step-1, strings.Join(lines, "\n"))
			}
		}
	}
}

func TestTheRailShowsOnlyWhereThereIsRoom(t *testing.T) {
	t.Cleanup(func() { i18n.Set(i18n.English) })
	// 80 columns hold the rail beside the card; 60 do not, and the
	// progress and the count say how far along.
	if f := frame(setupStart(true), 80, 24, enters(stepTheme)...); !strings.Contains(f, "Protected disks") {
		t.Fatalf("\n%s", f)
	}
	f := frame(setupStart(true), 60, 16, enters(stepTheme)...)
	if strings.Contains(f, "Protected disks") || !strings.Contains(f, "step 2 of 6") {
		t.Fatalf("\n%s", f)
	}
	// Smaller than the wizard draws in, it says so, in words not cut.
	if f := frame(setupStart(true), 30, 11); !strings.Contains(f, "Make the window larger") || strings.Contains(f, "…") {
		t.Fatalf("\n%s", f)
	}
}

func TestTheSetupProgramTellsCtrlCFromEsc(t *testing.T) {
	// The program itself, keys read as a terminal sends them: enter through
	// the pages saves, Ctrl+C cancels.
	t.Cleanup(func() { i18n.Set(i18n.English) })
	run := func(keys string) (SetupChoice, bool, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return runSetup(ctx, setupStart(false), tea.WithInput(strings.NewReader(keys)), tea.WithOutput(io.Discard), tea.WithoutSignalHandler())
	}
	// Past the welcome, English, then on to the last page and enter.
	if c, saved, err := run("\r\x1b[B\r\r\r\r\r\r"); err != nil || !saved || c.Language != "en" {
		t.Fatalf("enter: %+v %v %v", c, saved, err)
	}
	if _, saved, err := run("\r\x03"); saved || !errors.Is(err, ErrSetupCancelled) {
		t.Fatalf("ctrl+c: %v %v", saved, err)
	}
}
