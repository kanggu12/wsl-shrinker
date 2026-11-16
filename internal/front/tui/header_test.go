package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/kanggu12/wsl-shrinker/internal/front/i18n"
	"github.com/kanggu12/wsl-shrinker/internal/front/ui"
	buildversion "github.com/kanggu12/wsl-shrinker/internal/version"
)

// longVersion has the header measured with a version longer than any this
// tool will have soon, as a test build's "dev" leaves room a release's
// version does not.
func longVersion(t *testing.T) {
	was := buildversion.Version
	buildversion.Version = "v10.10.10"
	t.Cleanup(func() { buildversion.Version = was })
}

// checkRule holds the row under the header, rule, against the row under
// it: a rule from margin to margin whose every ┬ has a pane rule under it.
// Where a question's box clears its row from edge to edge, or stands on
// it, the row has no rule at all, never a piece of one beside the box.
func checkRule(t *testing.T, at string, w int, rule, under []string) {
	t.Helper()
	if len(rule) != w {
		t.Errorf("%s: the row under the header is %d cells, not %d", at, len(rule), w)
		return
	}
	if isRule(rule) {
		// A rule: whole, between the margins, joined only to pane rules.
		if rule[0] != " " || rule[w-1] != " " {
			t.Errorf("%s: the rule does not stand between the margins: %q", at, strings.Join(rule, ""))
		}
		for i := 1; i < w-1; i++ {
			switch {
			case rule[i] == "┬" && (i >= len(under) || under[i] != "│"):
				t.Errorf("%s: the rule's ┬ at column %d hangs over %q, not a pane rule", at, i, strings.Join(under, ""))
			case rule[i] != "─" && rule[i] != "┬":
				t.Errorf("%s: the rule has %q at column %d: %q", at, rule[i], i, strings.Join(rule, ""))
			}
		}
		return
	}
	// No rule: a box's row. The box's own border may run along it, but no
	// piece of the rule is left beside the box.
	first, last := -1, -1
	for i, c := range rule {
		if c != "" && strings.ContainsAny(c, "╭┌└╰╮┐┘╯│") {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	for i, c := range rule {
		if (c == "─" || c == "┬") && (first < 0 || i < first || i > last) {
			t.Errorf("%s: a piece of the rule is left beside the box at column %d: %q", at, i, strings.Join(rule, ""))
			return
		}
	}
}

// isRule reports whether a row is the header's rule: it starts a cell in.
func isRule(row []string) bool { return len(row) > 2 && (row[1] == "─" || row[1] == "┬") }

// cells is a row of a frame as its cells: a character two cells wide, as a
// Korean one, takes two, the second empty, so a column is an index.
func cells(row string) []string {
	var out []string
	for _, r := range row {
		out = append(out, string(r))
		// The second cell of a wide character holds nothing of its own.
		for range ansi.StringWidth(string(r)) - 1 {
			out = append(out, "")
		}
	}
	return out
}

// TestTheHeaderStandsOnARuleThatMeetsThePanes: on every screen, at every
// size and in both languages, the row under the header is a rule from
// margin to margin. Where it has a ┬, a pane rule starts right under it,
// and where the main screen starts its pane rule under it, the rule has its
// ┬ there: the panes hang from the header. A question's box may clear the
// rule's row or stand on it, never cut the rule short; the short heights
// are where a box comes closest. The header, the name and its version,
// stays above every box that fits under it.
func TestTheHeaderStandsOnARuleThatMeetsThePanes(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.English, i18n.Korean} {
		i18n.Set(lang)
		for name, keys := range keySets {
			box := name == "confirm" || name == "marked"
			for _, w := range []int{40, 45, 60, 80, 100, 110, 132, 160, 180, 200} {
				for _, h := range []int{12, 14, 16, 18, 24, 40} {
					r := screen(demo(), w, h, keys...)
					rule, under := cells(r[1]), cells(r[2])
					at := fmt.Sprintf("%s %s %dx%d", lang, name, w, h)
					if !strings.Contains(r[0], "wsl-shrinker") {
						t.Errorf("%s: the header lost the name: %q", at, r[0])
					}
					// Screens with no box have their rule whole.
					if !box && !isRule(rule) {
						t.Errorf("%s: no rule under the header: %q", at, r[1])
						continue
					}
					checkRule(t, at, w, rule, under)
					// The main screen has no other │ in its first row than the
					// rule between its panes.
					if name != "main" && name != "second" {
						continue
					}
					for i, c := range under {
						if c == "│" && i < w && rule[i] != "┬" {
							t.Errorf("%s %s %dx%d: the pane rule at column %d does not meet the header's rule: %q", lang, name, w, h, i, r[1])
						}
					}
				}
			}
		}
	}
	i18n.Set(i18n.English)
}

// TestTheHeaderSaysWhereItIs: a screen opened from the main one names
// itself after the version, in the words of the key that opens it, as the
// setup does; the main screen says WSL's version there.
func TestTheHeaderSaysWhereItIs(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.English, i18n.Korean} {
		i18n.Set(lang)
		for key, keys := range map[string][]string{"key.history": {"h"}, "key.help": {"?"}, "key.clean": {"c"}, "key.shrink": {"s", "enter"}} {
			head := screen(demo(), 132, 30, keys...)[0]
			if want := i18n.T(key); !strings.Contains(head, "wsl-shrinker ") || !strings.Contains(head, "·  "+want) {
				t.Errorf("%s after %q: the header does not name the screen %q after the version: %q", lang, keys, want, head)
			}
		}
		if head := screen(demo(), 132, 30)[0]; !strings.Contains(head, "·  WSL ") {
			t.Errorf("%s: the main screen's header does not say WSL's version: %q", lang, head)
		}
	}
	i18n.Set(i18n.English)
}

// TestTheHeaderKeepsItsTwoSidesApart: the right side, from the total's
// square on, stands at least six cells from the left, more than the five
// between two of its pieces, so the two read as two; closer only where the
// total alone stands beside the name alone, on a narrow screen. Every width
// from 40 to 160 columns, as a piece drops at a width of its own.
func TestTheHeaderKeepsItsTwoSidesApart(t *testing.T) {
	longVersion(t)
	for _, lang := range []i18n.Lang{i18n.English, i18n.Korean} {
		i18n.Set(lang)
		for _, keys := range [][]string{nil, {"h"}} {
			for w := 40; w <= 160; w++ {
				head := screen(demo(), w, 30, keys...)[0]
				// The main screen keeps the total wherever it fits in fewer
				// words beside the name, two cells apart, inside the margins.
				short := "▪ " + i18n.T("header.total.short", "87.0 GB")
				need := 1 + ansi.StringWidth("▪▪▪ wsl-shrinker "+buildversion.Short()) + 2 + ansi.StringWidth(short) + 1
				if keys == nil && w >= need && !strings.Contains(head, "can be given back now") && !strings.Contains(head, "돌려받을 수 있음") && !strings.Contains(head, short) {
					t.Errorf("%s at %d columns: the total left the main screen's header: %q", lang, w, head)
				}
				// The total's square is the first after the name: the chip
				// before the name is squares too.
				n := strings.Index(head, "wsl-shrinker")
				if n < 0 {
					t.Errorf("%s %q at %d columns: the header lost the name: %q", lang, keys, w, head)
					continue
				}
				i := strings.Index(head[n:], "▪")
				if i < 0 {
					continue
				}
				i += n
				left := strings.TrimRight(head[:i], " ")
				gap := ansi.StringWidth(head[:i]) - ansi.StringWidth(left)
				least := 2
				if strings.Contains(left, "·") || strings.Contains(head[i:], "·") {
					least = 6
				}
				if gap < least {
					t.Errorf("%s %q at %d columns: the header's sides are %d cells apart, not %d: %q", lang, keys, w, gap, least, head)
				}
			}
		}
	}
	i18n.Set(i18n.English)
}

// TestTheTotalOutlastsWSLsVersionInEnglishToo: on a narrow main screen what
// can come back stays in the header where WSL's version goes, as it does in
// Korean (TestHeaderKeepsTheTotalLongest).
func TestTheTotalOutlastsWSLsVersionInEnglishToo(t *testing.T) {
	if head := screen(demo(), 60, 16)[0]; !strings.Contains(head, "can be given back now") {
		t.Errorf("the total left the header before WSL's version: %q", head)
	}
}

// TestTheSetupHeaderNamesTheStep: the setup's header says the step's number
// and its name, as the rail has it and wsl-spawner's header does, and the
// bar of the steps is right under it.
func TestTheSetupHeaderNamesTheStep(t *testing.T) {
	r := strings.Split(frame(setupStart(true), 100, 21, enters(3)...), "\n")
	if !strings.Contains(r[0], "step 3 of 6 · Mode") {
		t.Errorf("the setup's header does not name the step: %q", r[0])
	}
	if strings.TrimSpace(r[1]) == "" {
		t.Errorf("the setup's bar is not right under its header: %q", r[1])
	}
	// Short of room the name goes first, then the number; "setup" stays.
	longVersion(t)
	r = strings.Split(frame(setupStart(true), 56, 21, enters(3)...), "\n")
	if !strings.Contains(r[0], "step 3 of 6") || strings.Contains(r[0], "Mode") {
		t.Errorf("56 columns: the setup's header does not keep the number alone: %q", r[0])
	}
	r = strings.Split(frame(setupStart(true), 44, 21, enters(3)...), "\n")
	if strings.Contains(r[0], "step") || !strings.Contains(r[0], "setup") || !strings.Contains(r[0], "v10.10.10") {
		t.Errorf("44 columns: the setup's header does not keep its name, version and \"setup\" alone: %q", r[0])
	}
}

// TestTheRuleIsASCIIWhereTheMarksAre: where the console's marks are ASCII,
// the rule is dashes and its join a plus, one cell each as the marks were.
func TestTheRuleIsASCIIWhereTheMarksAre(t *testing.T) {
	ui.SetMarks(ui.MarksASCII)
	defer ui.SetMarks(ui.MarksUnicode)
	r := screen(demo(), 132, 30)
	if strings.Trim(r[1], " -+") != "" || !strings.Contains(r[1], "+") || len(r[1]) != 132 {
		t.Errorf("the ASCII rule is not dashes and a plus: %q", r[1])
	}
}
