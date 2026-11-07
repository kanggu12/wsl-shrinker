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
)

// Theme is one set of colors, for a dark or a light terminal. The palette is
// zellij-reaper's, so the two tools look like one family.
type Theme struct {
	Dark bool
	// Ink and Muted are the two text levels; Rule draws borders.
	Ink, Muted, Rule color.Color
	// Mark is the brand highlighter of the name chip.
	Mark color.Color
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
		Ink:   ld(lipgloss.Color("#1D1C1D"), lipgloss.Color("#E3E4E5")),
		Muted: ld(lipgloss.Color("#616061"), lipgloss.Color("#9198A1")),
		Rule:  ld(lipgloss.Color("#D1D9E0"), lipgloss.Color("#3D444D")),
		Mark:  ld(lipgloss.Color("#F2C744"), lipgloss.Color("#ECB22E")),
		Cat: map[Category]color.Color{
			CatGain:  ld(lipgloss.Color("#A86A00"), lipgloss.Color("#ECB22E")),
			CatSmall: ld(lipgloss.Color("#616061"), lipgloss.Color("#9198A1")),
			CatLive:  ld(lipgloss.Color("#1264A3"), lipgloss.Color("#36C5F0")),
			CatDone:  ld(lipgloss.Color("#007A5A"), lipgloss.Color("#2EB67D")),
			CatSafe:  ld(lipgloss.Color("#7B3FA0"), lipgloss.Color("#C9A7EB")),
			CatBad:   ld(lipgloss.Color("#C0123E"), lipgloss.Color("#E01E5A")),
		},
		Selected: ld(lipgloss.Color("#E8F1FB"), lipgloss.Color("#262A31")),
		Key:      ld(lipgloss.Color("#ECECEE"), lipgloss.Color("#2C2F35")),
		Track:    ld(lipgloss.Color("#E4E7EB"), lipgloss.Color("#30343B")),
	}
}

// Fg is a style with a foreground color.
func Fg(c color.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }

// Dot is the colored "●" that leads a row.
func (t Theme) Dot(c Category) string { return Fg(t.Cat[c]).Render("●") }

// KeyCap draws a key as a small raised cap followed by what it does.
func (t Theme) KeyCap(key, label string) string {
	k := lipgloss.NewStyle().Background(t.Key).Foreground(t.Ink).Bold(true).Padding(0, 1).Render(key)
	return k + " " + Fg(t.Muted).Render(label)
}

// Chip is the name in the header: dark text on the brand yellow.
func (t Theme) Chip(s string) string {
	return lipgloss.NewStyle().Background(t.Mark).Foreground(lipgloss.Color("#1D1C1D")).Bold(true).Padding(0, 1).Render(s)
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
	var b strings.Builder
	for i, p := range parts {
		b.WriteString(Fg(p.Color).Render(strings.Repeat("█", cells[i])))
	}
	b.WriteString(Fg(t.Track).Render(strings.Repeat("█", w-used)))
	return b.String()
}

// Part is one segment of a Bar.
type Part struct {
	Value int64
	Color color.Color
}

// Fit pads or cuts s to exactly w cells, with "…" where it was cut.
func Fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) > w {
		s = ansi.Truncate(s, w, "…")
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
	r := []rune(s)
	keep := w - 1
	head := keep / 3
	tail := keep - head
	return string(r[:head]) + "…" + string(r[len(r)-tail:])
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
