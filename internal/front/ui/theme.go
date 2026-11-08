// Package ui holds what the screen and the command line share: the colors,
// keycaps, bars and fitting text to a width.
package ui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Category groups disks and findings by what they mean to the person
// looking, which is what the color says.
type Category int

const (
	// CatGain: worth shrinking, enough to give back.
	CatGain Category = iota
	// CatSmall: little to give back.
	CatSmall
	// CatLive: running, or in use.
	CatLive
	// CatDone: shrunk, or a step that finished.
	CatDone
	// CatSafe: protected, or a choice that costs a download.
	CatSafe
	// CatBad: cannot be shrunk, or a step that failed.
	CatBad
	// CatWarn: a warning, worth a second look.
	CatWarn
)

// Theme is one set of colors, for a dark or a light terminal. wsl-shrinker's
// identity is the block map: slate for data that stays, mint for space that
// goes back to Windows, and the other colors for what a disk is doing.
type Theme struct {
	Dark bool
	// Ink and Muted are the two text levels; Rule draws borders; Faint is
	// for separators between pieces of text, quieter than Muted but still
	// read on either background.
	Ink, Muted, Rule, Faint color.Color
	// Mark is the brand color: the name chip, marks and chosen items.
	Mark color.Color
	// Stays is the slate of data that stays in a disk.
	Stays color.Color
	// Cat holds the color of each category.
	Cat map[Category]color.Color
	// Selected is the background of the selected row; Key the keycaps';
	// Track the empty part of a bar.
	Selected, Key, Track color.Color
}

// NewTheme returns the theme for a dark or a light background.
func NewTheme(dark bool) Theme {
	ld := lipgloss.LightDark(dark)
	return Theme{
		Dark:  dark,
		Ink:   ld(lipgloss.Color("#14171F"), lipgloss.Color("#E8ECF2")),
		Muted: ld(lipgloss.Color("#5B6474"), lipgloss.Color("#9AA4B2")),
		Rule:  ld(lipgloss.Color("#D7DDE5"), lipgloss.Color("#2E3541")),
		Faint: ld(lipgloss.Color("#8A93A3"), lipgloss.Color("#6B7585")),
		Mark:  ld(lipgloss.Color("#0B8A7C"), lipgloss.Color("#34D3BF")),
		Stays: ld(lipgloss.Color("#9AA3B2"), lipgloss.Color("#5D6878")),
		Cat: map[Category]color.Color{
			CatGain:  ld(lipgloss.Color("#0B8A7C"), lipgloss.Color("#34D3BF")),
			CatSmall: ld(lipgloss.Color("#5B6474"), lipgloss.Color("#9AA4B2")),
			CatLive:  ld(lipgloss.Color("#4F46E5"), lipgloss.Color("#8B93FF")),
			CatDone:  ld(lipgloss.Color("#0B8A7C"), lipgloss.Color("#34D3BF")),
			CatSafe:  ld(lipgloss.Color("#C0266D"), lipgloss.Color("#F472B6")),
			CatBad:   ld(lipgloss.Color("#C8323C"), lipgloss.Color("#FF6B6B")),
			CatWarn:  ld(lipgloss.Color("#B45309"), lipgloss.Color("#F5B942")),
		},
		Selected: ld(lipgloss.Color("#EAF6F4"), lipgloss.Color("#1B2430")),
		Key:      ld(lipgloss.Color("#E9EDF2"), lipgloss.Color("#232A35")),
		Track:    ld(lipgloss.Color("#E4E8EE"), lipgloss.Color("#262D38")),
	}
}

// Fg is a style with a foreground color.
func Fg(c color.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }

// Dot is the colored block that leads a row: one cell wide in every
// terminal, unlike "●", which East Asian settings draw two cells wide.
func (t Theme) Dot(c Category) string { return Fg(t.Cat[c]).Render(Block) }

// Block is the identity's unit: a small square, one cell wide everywhere.
const Block = "▪"

// KeyCap draws a key as a small raised cap followed by what it does.
func (t Theme) KeyCap(key, label string) string {
	// ⏎ is a word where the console has no font for it.
	k := lipgloss.NewStyle().Background(t.Key).Foreground(t.Ink).Bold(true).Padding(0, 1).Render(Words(key))
	return k + " " + Fg(t.Muted).Render(label)
}

// Chip is the name in the header, as the logo draws it: two blocks of data
// that stay, one that goes back, then the name.
func (t Theme) Chip(s string) string {
	return Fg(t.Stays).Render(Block+Block) + Fg(t.Mark).Render(Block) + " " + lipgloss.NewStyle().Foreground(t.Ink).Bold(true).Render(s)
}

// Bar draws parts of a whole as one bar w cells wide: each part takes cells
// in proportion to its value, in its color, and the rest is the track. A
// part above zero always gets at least one cell, so it stays visible.
func (t Theme) Bar(w int, total int64, parts ...Part) string {
	if w <= 0 {
		return ""
	}
	cells := make([]int, len(parts))
	used := 0
	for i, p := range parts {
		if p.Value <= 0 || total <= 0 {
			continue
		}
		cells[i] = int(float64(p.Value) / float64(total) * float64(w))
		if cells[i] == 0 {
			cells[i] = 1
		}
		used += cells[i]
	}
	// Parts that make up the whole fill the bar: rounding down must not leave
	// a cell of track that reads as a part of its own.
	var sum int64
	for _, p := range parts {
		sum += max(0, p.Value)
	}
	if sum >= total && used < w && len(parts) > 0 {
		cells[len(parts)-1] += w - used
		used = w
	}
	// Rounding can overshoot; take the excess from the largest part.
	for used > w {
		big := 0
		for i := range cells {
			if cells[i] > cells[big] {
				big = i
			}
		}
		cells[big]--
		used--
	}
	// Each cell is a block, as on a disk; the empty part is the track.
	var b strings.Builder
	for i, p := range parts {
		b.WriteString(Fg(p.Color).Render(strings.Repeat(Block, cells[i])))
	}
	b.WriteString(Fg(t.Track).Render(strings.Repeat(Block, w-used)))
	return b.String()
}

// Part is one segment of a Bar.
type Part struct {
	Value int64
	Color color.Color
}

// Fit pads or cuts s to exactly w cells, with "…" where it was cut, right
// after the last word kept: "Docker Desktop…", never "Docker Desktop …".
func Fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) > w {
		s = strings.TrimRight(ansi.Truncate(s, w-1, ""), " ") + "…"
	}
	return s + strings.Repeat(" ", max(0, w-lipgloss.Width(s)))
}

// FitRight is Fit with the text against the right edge.
func FitRight(s string, w int) string {
	if lipgloss.Width(s) > w {
		return Fit(s, w)
	}
	return strings.Repeat(" ", w-lipgloss.Width(s)) + s
}

// Middle shortens a long path in its middle, keeping the drive and the file
// name, which say the most: C:\Users\…\LocalState\ext4.vhdx.
func Middle(s string, w int) string {
	if lipgloss.Width(s) <= w || w < 8 {
		return Fit(s, w)
	}
	keep := w - 1
	head := keep / 3
	tail := keep - head
	// Cut by cells, not letters: a Korean letter takes two.
	return Fit(ansi.Truncate(s, head, "")+"…"+ansi.TruncateLeft(s, lipgloss.Width(s)-tail, ""), w)
}

// Highlight paints a whole row's background, w cells wide. Styled parts of
// the row end with a reset that would clear the background too, so the
// background is set again after every reset.
func Highlight(row string, bg color.Color, w int) string {
	// The escape sequence that turns this background on, taken from a
	// rendered space.
	on := lipgloss.NewStyle().Background(bg).Render(" ")
	on = on[:strings.Index(on, " ")]
	row = strings.ReplaceAll(Fit(row, w), "\x1b[m", "\x1b[m"+on)
	row = strings.ReplaceAll(row, "\x1b[0m", "\x1b[0m"+on)
	return on + row + "\x1b[m"
}

// ShellArg is a name as it goes into a command to type: as it is when it
// is one word, in quotes when it holds spaces or anything a shell reads,
// in PowerShell and in Linux shells alike.
func ShellArg(s string) string {
	plain := s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._-/:", r))
	}) < 0
	switch {
	case plain:
		return s
	case !strings.ContainsAny(s, "'"):
		// Single quotes take everything in them as it is, in both shells.
		return "'" + s + "'"
	}
	// A name with a single quote: double quotes, which it does not end.
	return `"` + s + `"`
}
