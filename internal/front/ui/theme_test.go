package ui

import (
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/kanggu12/wsl-shrinker/internal/engine/disks"
	"github.com/kanggu12/wsl-shrinker/internal/engine/estimate"
	"github.com/kanggu12/wsl-shrinker/internal/engine/shrink"
)

func TestFitCutsRightAfterTheLastWordKept(t *testing.T) {
	// "Docker Desktop…", never "Docker Desktop …".
	if got := Fit("Docker Desktop data", 15); got != "Docker Desktop…" {
		t.Fatalf("%q", got)
	}
	if got := Fit("Debian", 8); got != "Debian  " {
		t.Fatalf("%q", got)
	}
}

func TestMiddleCountsCellsNotLetters(t *testing.T) {
	// A Korean sentence around a path: two cells a letter, the end kept,
	// the line exactly as wide as asked.
	s := "⏎ 누르면 C:\\Users\\ann\\AppData\\Roaming\\wsl-shrinker\\config.toml에 저장"
	for _, w := range []int{20, 40, 58} {
		got := Middle(s, w)
		if lipgloss.Width(got) != w {
			t.Errorf("%d: %d cells: %q", w, lipgloss.Width(got), got)
		}
		if w >= 40 && got[len(got)-len("에 저장"):] != "에 저장" {
			t.Errorf("%d: lost its end: %q", w, got)
		}
	}
}

func TestShellArgQuotesANameWithSpaces(t *testing.T) {
	// As PowerShell and a Linux shell both take it.
	for name, want := range map[string]string{
		"Ubuntu-24.04":        "Ubuntu-24.04",
		"Docker Desktop data": "'Docker Desktop data'",
		"data/ext4.vhdx":      "data/ext4.vhdx",
		"a$b":                 "'a$b'",
		"Ann's disk":          `"Ann's disk"`,
	} {
		if got := ShellArg(name); got != want {
			t.Errorf("%q: %s", name, got)
		}
	}
}

func TestPlainMarksKeepEachMarksWidth(t *testing.T) {
	// A screen is laid out with the marks, then swapped: each stand-in
	// takes the cells its mark took, and ⏎ becomes a word only where the
	// words are not measured yet.
	t.Cleanup(func() { SetMarks(MarksUnicode) })
	lackingMarks := "✓✗↻⏎⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"
	wideMarks := "▪·…→←↑↓–—│╰●•›"
	if Marks(lackingMarks+wideMarks) != lackingMarks+wideMarks || Words(lackingMarks) != lackingMarks {
		t.Fatal("marks swapped with the set left as it is")
	}
	for _, c := range []struct {
		set     MarkSet
		swapped string
		kept    string
	}{
		{MarksPlain, lackingMarks, wideMarks},
		{MarksASCII, lackingMarks + wideMarks, ""},
	} {
		SetMarks(c.set)
		for _, m := range c.swapped {
			s := string(m)
			if got := Marks(s); lipgloss.Width(got) != lipgloss.Width(s) || len(got) != 1 || got[0] > 0x7e {
				t.Errorf("set %d: %s stands as %q", c.set, s, got)
			}
		}
		for _, m := range c.kept {
			if got := Marks(string(m)); got != string(m) {
				t.Errorf("set %d: %c swapped for %q", c.set, m, got)
			}
		}
		if got := Words("⏎ writes them to"); got != "Enter writes them to" {
			t.Errorf("set %d: %q", c.set, got)
		}
		// The words themselves, Korean included, pass as they are.
		if got := Marks("Ubuntu-24.04 줄일 만한 크기"); got != "Ubuntu-24.04 줄일 만한 크기" {
			t.Errorf("set %d: %q", c.set, got)
		}
	}
}

func TestEveryFrontColorsADiskByOneRule(t *testing.T) {
	gb := int64(1 << 30)
	worth := disks.Disk{Estimate: &estimate.Result{Reclaim: 5 * gb}}
	little := disks.Disk{Estimate: &estimate.Result{Reclaim: gb / 2}}
	for _, c := range []struct {
		d    disks.Disk
		b    shrink.Block
		want Category
	}{
		{worth, "", CatGain},
		{little, "", CatSmall},
		// Exactly the size worth shrinking is worth it, as the engine has it.
		{disks.Disk{Estimate: &estimate.Result{Reclaim: gb}}, "", CatGain},
		// No estimate, though nothing is said in the way: never worth it.
		{disks.Disk{}, "", CatSmall},
		{worth, shrink.BlockProtected, CatSafe},
		{worth, shrink.BlockHeld, CatLive},
		{worth, shrink.BlockFixed, CatBad},
		{worth, shrink.BlockNoCompact, CatBad},
		{worth, shrink.BlockUnreadable, CatBad},
	} {
		if got := CategoryOf(c.d, c.b, gb); got != c.want {
			t.Errorf("%q, reclaim %v: %v, want %v", c.b, c.d.Estimate, got, c.want)
		}
	}
}
