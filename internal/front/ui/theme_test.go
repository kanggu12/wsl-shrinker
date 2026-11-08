package ui

import (
	"testing"

	"charm.land/lipgloss/v2"
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
