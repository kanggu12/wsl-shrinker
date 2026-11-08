package ui

import "strings"

// MarkSet is which characters the screen and the commands draw their marks
// with: ✓ for a step done, ✗ for one failed, ⏎ for the Enter key, ↻ while the
// disks are read again, a spinner of Braille dots while something runs, and
// the blocks, dots, arrows and lines between them.
type MarkSet int

const (
	// MarksUnicode draws them all as they are: Windows Terminal, VS Code's
	// terminal and Linux terminals have fonts for every one.
	MarksUnicode MarkSet = iota
	// MarksPlain swaps the marks the Windows console's own fonts lack
	// (Consolas, Lucida Console) for ASCII ones, a box showing otherwise.
	MarksPlain
	// MarksASCII swaps every character that is not ASCII, but for the
	// words themselves: the Windows console on an East Asian code page
	// draws a character Unicode leaves ambiguous, as an arrow, a dot or a
	// line, two cells wide, where every other terminal draws one, which
	// would push a screen measured with one off its lines.
	MarksASCII
)

// set is the marks this process draws.
var set = MarksUnicode

// SetMarks sets the marks this process draws; main does it once, first.
func SetMarks(m MarkSet) { set = m }

// Plain reports whether marks are swapped at all.
func Plain() bool { return set != MarksUnicode }

// Each stand-in is ASCII, which every font has, one cell wide like the mark
// it replaces, so a screen measured with the marks still fits it. ⏎ alone
// becomes a word, put in before anything is measured (Words); in a screen
// already laid out it becomes one cell too (Marks), as a last resort.
var (
	// lacking are the marks the console's own fonts have no glyph for. A
	// step done is +, not v: v reads as a word, and as the arrow down.
	lacking = []string{"✓", "+", "✗", "x", "↻", "~",
		"⠋", "|", "⠙", "/", "⠹", "-", "⠸", "\\", "⠼", "|", "⠴", "/", "⠦", "-", "⠧", "\\", "⠇", "|", "⠏", "/"}
	// wide are the characters an East Asian console draws two cells wide:
	// marks, arrows and the lines of a box, whose corners become +. A step
	// waiting is ., apart from - for one skipped.
	wide = []string{"▪", "#", "·", ".", "…", "~", "→", ">", "←", "<", "↑", "^", "↓", "v", "–", "-", "—", "-",
		"│", "|", "─", "-", "╭", "+", "╮", "+", "╰", "+", "╯", "+", "┌", "+", "┐", "+", "└", "+", "┘", "+",
		"├", "+", "┤", "+", "┬", "+", "┴", "+", "┼", "+", "═", "=", "║", "|", "●", "*", "•", "*", "›", ">"}
	// arrows are the arrows in words not measured yet, two cells each, so
	// "312 GB → 248 GB" does not read as "greater than".
	arrows = []string{"→", "->", "←", "<-"}
	// laidOut and unmeasured are the swaps for each set: for a screen
	// already laid out, and for words not measured yet.
	laidOut = map[MarkSet]*strings.Replacer{
		MarksPlain: strings.NewReplacer(append([]string{"⏎", ">"}, lacking...)...),
		MarksASCII: strings.NewReplacer(append(append([]string{"⏎", ">"}, lacking...), wide...)...),
	}
	unmeasured = map[MarkSet]*strings.Replacer{
		MarksPlain: strings.NewReplacer(append([]string{"⏎", "Enter"}, lacking...)...),
		// The arrows go first: a pair that comes earlier wins.
		MarksASCII: strings.NewReplacer(append(append(append([]string{"⏎", "Enter"}, arrows...), lacking...), wide...)...),
	}
)

// Marks returns s with the marks swapped for the set this process draws,
// each for a stand-in of its own width: for a screen already laid out. A
// name in it, a distribution's or a file's, is swapped as well; the font
// that lacks the mark could not show it there either.
func Marks(s string) string {
	if r := laidOut[set]; r != nil {
		return r.Replace(s)
	}
	return s
}

// Words returns s with the marks swapped for the set this process draws, ⏎
// for the word Enter: for words not measured yet, as a key or a sentence.
func Words(s string) string {
	if r := unmeasured[set]; r != nil {
		return r.Replace(s)
	}
	return s
}
