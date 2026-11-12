package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/kanggu12/wsl-shrinker/internal/engine/shrink"
	"github.com/kanggu12/wsl-shrinker/internal/engine/survey"
	"github.com/kanggu12/wsl-shrinker/internal/front/i18n"
)

// What the screen says in windows too small for all of it: nothing that
// matters is cut, and nothing is left unsaid that only one line says.

// surveyFails is the demo with every look inside failing.
type surveyFails struct{ *Demo }

func (surveyFails) Survey(context.Context, string) (survey.Result, error) {
	return survey.Result{}, errors.New("wsl.exe -d Ubuntu-24.04 timed out after 20s")
}

// failingCompact is a run WSL refused to compact, with its long reason,
// that started the distribution again.
var failingCompact = []shrink.Event{
	{Step: shrink.StepCheck, Status: shrink.Done}, {Step: shrink.StepStop, Status: shrink.Done},
	{Step: shrink.StepCompact, Status: shrink.Failed, Detail: "wsl --manage --compact: Wsl/Service/E_UNEXPECTED (0x8000ffff). " +
		"The operation could not be completed because the virtual disk is in use by another process."},
	{Step: shrink.StepRestart, Status: shrink.Done},
}

// rows is a frame's rows, colors taken out and trailing spaces trimmed.
func rows(frame string) []string {
	out := strings.Split(ansi.Strip(frame), "\n")
	for i, l := range out {
		out[i] = strings.TrimRight(l, " ")
	}
	return out
}

func TestALookInsideThatFailedIsSaidWhereItsListHasNoRoom(t *testing.T) {
	// 80x24 has no room for the inside list under the checks: its one line
	// says the look failed, and c says so too rather than "nothing found".
	defer i18n.Set(i18n.English)
	for _, lang := range []i18n.Lang{i18n.English, i18n.Korean} {
		i18n.Set(lang)
		out := ansi.Strip(Snapshot(surveyFails{demo()}, 80, 24, true, nil, ""))
		if !strings.Contains(out, i18n.T("inside.failed.short")) {
			t.Errorf("%s: no word of the failed look:\n%s", lang, out)
		}
		c := ansi.Strip(Snapshot(surveyFails{demo()}, 80, 24, true, []string{"c"}, ""))
		if strings.Contains(c, i18n.T("toast.clean.nothing", "Ubuntu-24.04")) || !strings.Contains(c, "timed out after 20s") {
			t.Errorf("%s: c after a failed look:\n%s", lang, c)
		}
	}
}

func TestAMessageTooLongForItsRowTakesTheRowAbove(t *testing.T) {
	// Docker Desktop's disk at 60 columns: the message ends with what to
	// do, and that end is on screen, over two rows, not cut.
	out := rows(Snapshot(demo(), 60, 16, true, []string{"down", "s"}, ""))
	last := strings.Join(strings.Fields(strings.Join(out[len(out)-3:len(out)-1], " ")), " ")
	if !strings.Contains(last, "quit Docker Desktop to shrink it.") || strings.Contains(last, "…") {
		t.Fatalf("%q", out[len(out)-3:])
	}
}

func TestAFailedRunSaysWhereThingsStandInASmallWindow(t *testing.T) {
	// A long reason from WSL is cut to the rows left; where the disk and
	// the distribution stand is said whole.
	defer i18n.Set(i18n.English)
	for _, lang := range []i18n.Lang{i18n.English, i18n.Korean} {
		i18n.Set(lang)
		b := &scripted{Demo: demo(), events: failingCompact}
		out := ansi.Strip(Snapshot(b, 60, 16, true, []string{"s", "enter"}, ""))
		want := map[i18n.Lang]string{i18n.English: "was started", i18n.Korean: "원래대로"}[lang]
		if !strings.Contains(out, want) || strings.Contains(out, "……") {
			t.Errorf("%s:\n%s", lang, out)
		}
	}
}

func TestTheNoteUnderARunFitsASmallWindow(t *testing.T) {
	// At 60x16 the note on WSL stopping the distribution is said, whole.
	out := ansi.Strip(Snapshot(demo(), 60, 16, true, []string{"s", "enter"}, ""))
	if !strings.Contains(out, "WSL stops it") || !strings.Contains(out, "running.") {
		t.Fatalf("%s", out)
	}
}

func TestNothingBehindTheQuestionTouchesIt(t *testing.T) {
	// At 80x24 the box leaves a few cells beside it: they are blank, not
	// pieces of the words behind it.
	for _, l := range rows(Snapshot(demo(), 80, 24, true, []string{"s"}, "")) {
		if i := strings.IndexAny(l, "│╭╰"); i >= 0 && strings.TrimSpace(l[:i]) != "" {
			t.Fatalf("%q", l)
		}
	}
}

func TestTheHistoryNamesItsColumnsAndKeepsTheTime(t *testing.T) {
	defer i18n.Set(i18n.English)
	for _, lang := range []i18n.Lang{i18n.English, i18n.Korean} {
		i18n.Set(lang)
		out := rows(Snapshot(demo(), 80, 16, true, []string{"h"}, ""))
		page := strings.Join(out, "\n")
		if !strings.Contains(page, i18n.T("history.col.freed")) || !strings.Contains(page, i18n.T("history.col.sizes")) {
			t.Errorf("%s: no heads:\n%s", lang, page)
		}
		for _, l := range out[3 : len(out)-2] {
			if strings.HasSuffix(l, "…") {
				t.Errorf("%s: cut: %q", lang, l)
			}
		}
	}
}

func TestTheKeysAt80ColumnsKeepHistory(t *testing.T) {
	out := rows(Snapshot(demo(), 80, 24, true, nil, ""))
	if keys := out[len(out)-1]; !strings.Contains(keys, "h  history") {
		t.Fatalf("%q", keys)
	}
}

func TestSetupMarksTheDisksItCounts(t *testing.T) {
	// "1 GB: 1 disk worth shrinking" and the disk it counts first, with a
	// check; the one under the size after it, without.
	out := frame(setupStart(true), 100, 26, "enter", "enter", "enter", "enter")
	dev, work := strings.Index(out, "✓ Dev"), strings.Index(out, "Work")
	if dev < 0 || work < dev {
		t.Fatalf("%s", out)
	}
}

func TestTheDetailsKeepEveryNameWhole(t *testing.T) {
	// The README's size: a finding's name too long for its column loses
	// its note in brackets, not half a word; the disk's path loses whole
	// folders, never half a name.
	f := ansi.Strip(Snapshot(demo(), 132, 30, true, nil, ""))
	var journal, path string
	for _, l := range strings.Split(f, "\n") {
		switch {
		case strings.Contains(l, "System journal"):
			journal = l
		case strings.Contains(l, "ext4.vhdx"):
			path = l
		}
	}
	if journal == "" || strings.Contains(journal, "…") {
		t.Errorf("the journal's row: %q\n%s", journal, f)
	}
	if i := strings.Index(path, "…"); path == "" || i < 0 || !strings.HasSuffix(path[:i], `\`) || !strings.HasPrefix(path[i+len("…"):], `\`) {
		t.Errorf("the path: %q", path)
	}
}
