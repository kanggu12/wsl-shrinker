package tui

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/charmbracelet/x/ansi"

	"github.com/kanggu12/wsl-shrinker/internal/engine/disks"
	"github.com/kanggu12/wsl-shrinker/internal/engine/estimate"
	"github.com/kanggu12/wsl-shrinker/internal/front/i18n"
)

// setupStart is the setup screen on two disks, at the defaults.
func setupStart(first bool) SetupStart {
	gb := int64(1 << 30)
	return SetupStart{Language: "auto", Theme: "auto", Mode: "tui", MinGainGB: 1, Protect: []string{"work"},
		Disks: []disks.Disk{
			{Name: "Dev", Kind: disks.KindDistro, Estimate: &estimate.Result{FileSize: 40 * gb, After: 20 * gb, Reclaim: 20 * gb}},
			{Name: "Work", Kind: disks.KindDistro, Estimate: &estimate.Result{FileSize: 9 * gb, After: 8 * gb, Reclaim: gb}},
		},
		AutoLanguage: i18n.English, Path: `C:\Users\ann\AppData\Roaming\wsl-shrinker\config.toml`, First: first}
}

// press sends keys to the setup screen.
func press(m setupModel, keys ...string) setupModel {
	for _, k := range keys {
		next, _ := m.key(k)
		m = next.(setupModel)
	}
	return m
}

func TestSetupChangesEachValueRound(t *testing.T) {
	t.Cleanup(func() { i18n.Set(i18n.English) })
	m := newSetupModel(setupStart(false))
	// Language: auto → en, then back past auto round to ko.
	if m = press(m, "right"); m.choice.Language != "en" {
		t.Fatalf("%q", m.choice.Language)
	}
	if m = press(m, "left", "left"); m.choice.Language != "ko" || i18n.Current() != i18n.Korean {
		t.Fatalf("%q, shown in %v", m.choice.Language, i18n.Current())
	}
	// Colors and mode, space as → on a setting.
	if m = press(m, "down", "space"); m.choice.Theme != "dark" {
		t.Fatalf("%q", m.choice.Theme)
	}
	if m = press(m, "down", "right"); m.choice.Mode != "cli" {
		t.Fatalf("%q", m.choice.Mode)
	}
	if m = press(m, "l"); m.choice.Mode != "tui" {
		t.Fatalf("%q", m.choice.Mode)
	}
	// The gain goes round from its first step to its last.
	if m = press(m, "down", "left"); m.choice.MinGainGB != 50 {
		t.Fatalf("%v", m.choice.MinGainGB)
	}
}

func TestSetupKeepsAGainOfTheSettingsToComeBackTo(t *testing.T) {
	// 3.5 GB is none of the steps: it shows as it is, and stays among them.
	start := setupStart(false)
	start.MinGainGB = 3.5
	m := press(newSetupModel(start), "down", "down", "down")
	if v := m.value("gain"); v != "3.5 GB" {
		t.Fatalf("%q", v)
	}
	if m = press(m, "right", "left"); m.choice.MinGainGB != 3.5 {
		t.Fatalf("%v", m.choice.MinGainGB)
	}
}

func TestSpaceProtectsADiskByName(t *testing.T) {
	m := newSetupModel(setupStart(false))
	// Down past the four settings to Dev, then Work.
	m = press(m, "down", "down", "down", "down", "space")
	if strings.Join(m.choice.Protect, ",") != "work,Dev" {
		t.Fatalf("%v", m.choice.Protect)
	}
	// Work was protected as "work": the same disk, so space lifts it.
	m = press(m, "j", "space")
	if strings.Join(m.choice.Protect, ",") != "Dev" {
		t.Fatalf("%v", m.choice.Protect)
	}
	// The cursor stops at the last disk.
	if m = press(m, "down", "down"); m.row != m.last() {
		t.Fatalf("row %d of %d", m.row, m.last())
	}
}

func TestEnterSavesAndEscKeepsWhatWasThere(t *testing.T) {
	t.Cleanup(func() { i18n.Set(i18n.English) })
	if m := press(newSetupModel(setupStart(false)), "right", "enter"); !m.saved || m.choice.Language != "en" {
		t.Fatalf("%+v", m)
	}
	if m := press(newSetupModel(setupStart(false)), "right", "esc"); m.saved {
		t.Fatalf("saved on esc")
	}
	// ctrl+c cancels: nothing saved, and nothing is to follow.
	if m := press(newSetupModel(setupStart(true)), "right", "ctrl+c"); m.saved || !m.cancelled {
		t.Fatalf("ctrl+c: saved %v, cancelled %v", m.saved, m.cancelled)
	}
}

func TestTheDiskUnderTheCursorShowsOnAShortTerminal(t *testing.T) {
	// 60x14 leaves no room under a wrapped intro: the intro gives way, and
	// the disk the cursor is on is drawn.
	t.Cleanup(func() { i18n.Set(i18n.English) })
	frame := ansi.Strip(SetupSnapshot(setupStart(true), 60, 14, true, []string{"down", "down", "down", "down", "down"}))
	if !strings.Contains(frame, "Work") || strings.Contains(frame, "First run") {
		t.Fatalf("\n%s", frame)
	}
	if n := len(strings.Split(frame, "\n")); n != 14 {
		t.Fatalf("%d lines", n)
	}
	// A terminal with room keeps the intro.
	if frame := ansi.Strip(SetupSnapshot(setupStart(true), 100, 24, true, nil)); !strings.Contains(frame, "First run") {
		t.Fatalf("\n%s", frame)
	}
}

func TestTheValueColumnKeepsItsWidth(t *testing.T) {
	// A gain from the settings that is none of the steps counts for the
	// column while another value is shown, so the hints do not move.
	start := setupStart(false)
	start.MinGainGB = 1234.75
	m := newSetupModel(start)
	_, before := m.widths()
	m = press(m, "down", "down", "down", "right")
	if _, after := m.widths(); after != before || m.choice.MinGainGB == 1234.75 {
		t.Fatalf("width %d then %d, gain %v", before, after, m.choice.MinGainGB)
	}
}

func TestTheSetupScreenSaysWhatLeavingDoes(t *testing.T) {
	// On a first start esc keeps the defaults; later it quits, changing nothing.
	first := ansi.Strip(SetupSnapshot(setupStart(true), 100, 24, true, nil))
	later := ansi.Strip(SetupSnapshot(setupStart(false), 100, 24, true, nil))
	for _, c := range []struct {
		frame string
		want  []string
	}{
		{first, []string{"First run.", "esc  keep the defaults", "⏎ writes C:\\Users\\ann"}},
		{later, []string{"Nothing on your disks changes here.", "esc  quit"}},
	} {
		for _, w := range c.want {
			if !strings.Contains(c.frame, w) {
				t.Errorf("no %q in:\n%s", w, c.frame)
			}
		}
	}
	// The disks with what each gives back; the protected one says so.
	if !strings.Contains(later, "[ ] ▪ Dev") || !strings.Contains(later, "gives back 20.0 GB") || !strings.Contains(later, "[✓] ▪ Work") {
		t.Fatalf("\n%s", later)
	}
}

func TestTheLanguageShownWhileChoosingGoesBack(t *testing.T) {
	// Korean shows at once while it is chosen, and a frame left without
	// saving puts the language back.
	t.Cleanup(func() { i18n.Set(i18n.English) })
	i18n.Set(i18n.English)
	frame := ansi.Strip(SetupSnapshot(setupStart(false), 100, 24, true, []string{"left"}))
	if !strings.Contains(frame, "언어") || i18n.Current() != i18n.English {
		t.Fatalf("shown in %v:\n%s", i18n.Current(), frame)
	}
}

func TestTheSetupScreenFitsEightyColumns(t *testing.T) {
	// The narrow terminal the screen is made for: no line runs past it, and
	// the hints are cut only when they must be.
	t.Cleanup(func() { i18n.Set(i18n.English) })
	for _, keys := range [][]string{nil, {"left"}} {
		frame := ansi.Strip(SetupSnapshot(setupStart(true), 80, 24, true, keys))
		lines := strings.Split(frame, "\n")
		if len(lines) != 24 {
			t.Fatalf("%d lines", len(lines))
		}
		for _, l := range lines {
			if w := ansi.StringWidth(l); w > 80 {
				t.Errorf("%d cells: %q", w, l)
			}
			if strings.Contains(l, "…") {
				t.Errorf("cut: %q", l)
			}
		}
	}
}

func TestTheSetupProgramTellsCtrlCFromEsc(t *testing.T) {
	// The program itself, keys read as a terminal sends them: Enter saves,
	// Esc leaves with nothing chosen, Ctrl+C cancels.
	t.Cleanup(func() { i18n.Set(i18n.English) })
	run := func(keys string) (SetupChoice, bool, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return runSetup(ctx, setupStart(false), tea.WithInput(strings.NewReader(keys)), tea.WithOutput(io.Discard), tea.WithoutSignalHandler())
	}
	if c, saved, err := run("\x1b[C\r"); err != nil || !saved || c.Language != "en" {
		t.Fatalf("enter: %+v %v %v", c, saved, err)
	}
	if _, saved, err := run("\x03"); saved || !errors.Is(err, ErrSetupCancelled) {
		t.Fatalf("ctrl+c: %v %v", saved, err)
	}
}
