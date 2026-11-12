package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/kanggu12/wsl-shrinker/internal/app"
	"github.com/kanggu12/wsl-shrinker/internal/engine/disks"
	"github.com/kanggu12/wsl-shrinker/internal/engine/journal"
	"github.com/kanggu12/wsl-shrinker/internal/front/i18n"
	"github.com/kanggu12/wsl-shrinker/internal/front/ui"
	"github.com/kanggu12/wsl-shrinker/internal/machine/wsl"
)

// The way out of a run that stays cut off: R tries to finish it, and once
// a start has failed F forgets it, after a question that says what stays
// stopped.

// cutOffRun is the demo with a run of Ubuntu cut off after it had stopped
// the distribution and two containers.
func cutOffRun() *variant {
	return &variant{Demo: demo(), unfinishedState: journal.CutOff, unfinished: journal.Entry{
		Disk: "Ubuntu-24.04", Distro: "Ubuntu-24.04", Step: "compact", Stopped: true,
		Containers: []string{"0123456789ab", "ba9876543210"}}}
}

// stuckText is why a distribution does not start, in the engine's words:
// long enough to need three rows of the narrowest window.
const stuckText = "the root filesystem stays read-only; `wsl --shutdown` restarts WSL and clears it"

// stuck is a start that failed, as a recovery reports one: the run stays
// cut off.
func stuck() error { return app.StillCutOff{Err: errors.New(stuckText)} }

// loaded is the screen of b at w by h cells, its disks read.
func loaded(t *testing.T, b Backend, w, h int) model {
	t.Helper()
	m := playKeys(t, b, nil)
	m.width, m.height = w, h
	return m
}

// play presses a key and plays what the key started to its end: the
// answer to what it asked, then the reload that follows every answer. A
// look inside after the reload is taken as playKeys takes it.
func play(t *testing.T, m model, key string) model {
	t.Helper()
	next, cmd := m.Update(snapshotKey(key))
	m = next.(model)
	for i := 0; i < 2 && cmd != nil; i++ {
		next, cmd = m.Update(cmd())
		m = next.(model)
	}
	return m.surveyNow()
}

// shown is the screen as plain lines.
func shown(m model) []string { return rows(m.View().Content) }

// atRest is the screen once a passing message is gone: the status row
// then says what stays.
func atRest(m model) []string {
	m.toast = ""
	return shown(m)
}

// footerOf is the keys in the last row, their spaces run together.
func footerOf(lines []string) string { return joined(lines[len(lines)-1:]) }

// boxText is what the dialog on a screen says: the rows between its top
// and bottom border, without the border and the screen around them.
func boxText(t *testing.T, lines []string) []string {
	t.Helper()
	top, left, right := -1, 0, 0
	var inside []string
	for i, l := range lines {
		switch {
		case top < 0 && strings.Contains(l, "╭"):
			// The corners say which cells are the box's.
			top = i
			left = lipgloss.Width(l[:strings.Index(l, "╭")])
			right = lipgloss.Width(l[:strings.Index(l, "╮")])
		case top >= 0 && strings.Contains(l, "╰"):
			return inside
		case top >= 0:
			inside = append(inside, strings.TrimSpace(ansi.Cut(l, left+1, right)))
		}
	}
	t.Fatalf("no whole box on the screen:\n%s", strings.Join(lines, "\n"))
	return nil
}

// hasLine reports whether one of lines holds want whole.
func hasLine(lines []string, want string) bool {
	for _, l := range lines {
		if strings.Contains(l, want) {
			return true
		}
	}
	return false
}

func TestForgettingIsOfferedOnlyAfterRFailed(t *testing.T) {
	defer i18n.Set(i18n.English)
	for _, lang := range []i18n.Lang{i18n.English, i18n.Korean} {
		i18n.Set(lang)
		b := cutOffRun()
		m := loaded(t, b, 120, 30)
		// A run cut off: R finishes it, and nothing offers to forget it.
		recoverKey, forgetKey := "R "+i18n.T("key.recover"), "F "+i18n.T("key.forget")
		lines := atRest(m)
		if f := footerOf(lines); !strings.Contains(f, recoverKey) || strings.Contains(f, forgetKey) {
			t.Fatalf("%s: footer before R: %q", lang, f)
		}
		// F does nothing yet: no question, and nothing forgotten.
		if m = play(t, m, "F"); m.mode != modeMain || b.forgot != 0 {
			t.Fatalf("%s: F before a failed R: mode %v, forgot %d", lang, m.mode, b.forgot)
		}
		// R cannot start things again: its reason is said, and from then on
		// the status row and the footer name both ways on.
		b.recoverErr = stuck()
		m = play(t, m, "R")
		if b.recovered != 1 || !strings.Contains(m.toast, "read-only") {
			t.Fatalf("%s: recovered %d times, toast %q", lang, b.recovered, m.toast)
		}
		lines = atRest(m)
		if out := joined(lines); !strings.Contains(out, i18n.T("unfinished.cutoff.failed", "Ubuntu-24.04")) {
			t.Errorf("%s: the status row does not say the run is still cut off:\n%s", lang, strings.Join(lines, "\n"))
		}
		if f := footerOf(lines); !strings.Contains(f, recoverKey) || !strings.Contains(f, forgetKey) {
			t.Errorf("%s: footer after a failed R: %q", lang, f)
		}
		// Shrinking stays held back all the while.
		if f := footerOf(lines); strings.Contains(f, "s "+i18n.T("key.shrink")) {
			t.Errorf("%s: s is offered while a run is cut off: %q", lang, f)
		}
	}
}

func TestAFailureThatIsNotOfTheStartOffersNoForgetting(t *testing.T) {
	// Another wsl-shrinker at work on the run is a reason to wait, not a
	// run to give up: R says it, and F stays away.
	b := cutOffRun()
	b.recoverErr = errors.New("Ubuntu-24.04 is being shrunk by another wsl-shrinker; let it finish")
	m := play(t, loaded(t, b, 120, 30), "R")
	if m.recoverFailed || !strings.Contains(m.toast, "another wsl-shrinker") {
		t.Fatalf("recoverFailed %v, toast %q", m.recoverFailed, m.toast)
	}
	lines := atRest(m)
	if out := joined(lines); !strings.Contains(out, "The run of Ubuntu-24.04 was cut off at Compact · R finishes it") {
		t.Errorf("status:\n%s", strings.Join(lines, "\n"))
	}
	if f := footerOf(lines); strings.Contains(f, "F forget") {
		t.Errorf("footer %q", f)
	}
	if m = play(t, m, "F"); m.mode != modeMain {
		t.Errorf("F opened the question: mode %v", m.mode)
	}
}

func TestForgetBoxSaysWhatStaysStoppedAndHowToStartIt(t *testing.T) {
	defer i18n.Set(i18n.English)
	for _, lang := range []i18n.Lang{i18n.English, i18n.Korean} {
		i18n.Set(lang)
		for _, size := range [][2]int{{120, 30}, {80, 24}, {60, 16}, {40, 12}} {
			w, h := size[0], size[1]
			b := cutOffRun()
			b.recoverErr = stuck()
			m := play(t, play(t, loaded(t, b, w, h), "R"), "F")
			if m.mode != modeForget {
				t.Fatalf("%s %dx%d: F after a failed R: mode %v", lang, w, h, m.mode)
			}
			lines := shown(m)
			all := strings.Join(lines, "\n")
			// The screen keeps its size, and no row is wider than it.
			if len(lines) != h {
				t.Fatalf("%s %dx%d: %d rows:\n%s", lang, w, h, len(lines), all)
			}
			for _, l := range lines {
				if lipgloss.Width(l) > w {
					t.Errorf("%s %dx%d: a row %d cells wide: %q", lang, w, h, lipgloss.Width(l), l)
				}
			}
			box := boxText(t, lines)
			said := joined(box)
			// The title and what stays stopped are said whole on every size.
			for _, want := range []string{
				i18n.T("forget.title", "Ubuntu-24.04"),
				i18n.TN("forget.stopped", 2, "Ubuntu-24.04", 2),
				"⏎ " + i18n.T("key.forget"), "esc " + i18n.T("key.cancel"),
			} {
				if !strings.Contains(said, want) {
					t.Errorf("%s %dx%d: the box does not say %q:\n%s", lang, w, h, want, all)
				}
			}
			// The name stays whole wherever it is said: no line ends in
			// "Ubuntu-" with "24.04" on the next.
			for _, l := range box {
				if strings.HasSuffix(l, "Ubuntu-") {
					t.Errorf("%s %dx%d: the name is broken at its hyphen:\n%s", lang, w, h, all)
				}
			}
			// Where there is room, the commands to type, each on one line.
			if h > 12 {
				for _, cmd := range []string{"wsl -d Ubuntu-24.04", "docker start 0123456789ab ba9876543210"} {
					if !hasLine(box, cmd) {
						t.Errorf("%s %dx%d: %q is not on one line:\n%s", lang, w, h, cmd, all)
					}
				}
			} else if strings.Contains(said, "docker start") {
				// On the smallest screen they go whole rather than half.
				t.Errorf("%s %dx%d: the commands were expected to give way:\n%s", lang, w, h, all)
			}
			// Under the box the status row is empty: the box says the run,
			// and a piece of the row showing under it reads as noise.
			if lines[h-2] != "" {
				t.Errorf("%s %dx%d: the status row under the box says %q", lang, w, h, lines[h-2])
			}
			// The footer names the box's keys, and none of the screen's.
			if f := footerOf(lines); !strings.Contains(f, "⏎ "+i18n.T("key.forget")) || !strings.Contains(f, "esc "+i18n.T("key.cancel")) || strings.Contains(f, "R "+i18n.T("key.recover")) {
				t.Errorf("%s %dx%d: footer %q", lang, w, h, f)
			}
		}
	}
}

func TestForgetBoxCountsOneContainerAndNone(t *testing.T) {
	// One container, no container, and nothing stopped at all: each said
	// as it is, with only the commands that apply.
	for _, c := range []struct {
		name       string
		run        journal.Entry
		want, hand string
		not        string
	}{
		{"one container", journal.Entry{Disk: "Dev", Distro: "Dev", Step: "start", Stopped: true, Containers: []string{"0123456789ab"}},
			"It had stopped Dev and 1 container. Forgetting it starts nothing again.",
			"To start them by hand afterwards: wsl -d Dev, then docker start 0123456789ab inside it", "containers"},
		{"no container", journal.Entry{Disk: "Dev", Distro: "Dev", Step: "compact", Stopped: true},
			"It had stopped Dev. Forgetting it starts nothing again.", "To start it by hand afterwards: wsl -d Dev", "docker"},
		{"nothing stopped", journal.Entry{Disk: "Dev", Distro: "Dev", Step: "check"},
			"It had stopped nothing.", "", "by hand"},
		// An id that is not one docker start is given never reaches a
		// command a person is told to type.
		{"not an id", journal.Entry{Disk: "Dev", Distro: "Dev", Step: "compact", Stopped: true, Containers: []string{"$(reboot)"}},
			"It had stopped Dev. Forgetting it starts nothing again.", "To start it by hand afterwards: wsl -d Dev", "reboot"},
	} {
		b := &variant{Demo: demo(), unfinishedState: journal.CutOff, unfinished: c.run, recoverErr: stuck()}
		m := play(t, play(t, loaded(t, b, 120, 30), "R"), "F")
		said := joined(boxText(t, shown(m)))
		if !strings.Contains(said, c.want) || !strings.Contains(said, c.hand) || strings.Contains(said, c.not) {
			t.Errorf("%s:\n%s", c.name, said)
		}
	}
}

func TestARunWhoseJournalCannotBeReadIsNotSaidToHaveStoppedNothing(t *testing.T) {
	defer i18n.Set(i18n.English)
	for _, lang := range []i18n.Lang{i18n.English, i18n.Korean} {
		i18n.Set(lang)
		// A journal that did not parse: a run was cut off, and that is all
		// that is known of it.
		b := &variant{Demo: demo(), unfinishedState: journal.CutOff, recoverErr: stuck()}
		m := loaded(t, b, 120, 30)
		if out := joined(atRest(m)); !strings.Contains(out, i18n.T("unfinished.unknown")) {
			t.Errorf("%s: status before R:\n%s", lang, out)
		}
		m = play(t, m, "R")
		if out := joined(atRest(m)); !strings.Contains(out, i18n.T("unfinished.unknown.failed")) {
			t.Errorf("%s: status after a failed R:\n%s", lang, out)
		}
		m = play(t, m, "F")
		said := joined(boxText(t, shown(m)))
		for _, want := range []string{i18n.T("forget.title.unknown"), i18n.T("forget.unknown")} {
			if !strings.Contains(said, want) {
				t.Errorf("%s: the box does not say %q:\n%s", lang, want, said)
			}
		}
		// Nothing is guessed: neither "nothing", nor a command to type.
		if strings.Contains(said, i18n.T("forget.nothing")) || strings.Contains(said, "wsl -d") {
			t.Errorf("%s: the box guesses:\n%s", lang, said)
		}
		if m = play(t, m, "enter"); m.toast != i18n.T("toast.forgot.unknown") || b.forgot != 1 {
			t.Errorf("%s: toast %q, forgot %d", lang, m.toast, b.forgot)
		}
	}
}

func TestForgetBoxIsLeftWithoutForgetting(t *testing.T) {
	for _, key := range []string{"esc", "n", "q"} {
		b := cutOffRun()
		b.recoverErr = stuck()
		m := play(t, play(t, loaded(t, b, 120, 30), "R"), "F")
		// The key closes the question and nothing else: q does not end the
		// program from inside it.
		next, cmd := m.Update(snapshotKey(key))
		m = next.(model)
		if cmd != nil || m.mode != modeMain || b.forgot != 0 || m.unfinishedState != journal.CutOff {
			t.Fatalf("%s: command %v, mode %v, forgot %d, state %v", key, cmd != nil, m.mode, b.forgot, m.unfinishedState)
		}
		// The run is as it was: both keys still lead the footer.
		if f := footerOf(atRest(m)); !strings.Contains(f, "R finish run") || !strings.Contains(f, "F forget") {
			t.Errorf("%s: footer %q", key, f)
		}
	}
}

func TestForgettingClosesTheRunAndFreesShrinking(t *testing.T) {
	defer i18n.Set(i18n.English)
	for _, lang := range []i18n.Lang{i18n.English, i18n.Korean} {
		i18n.Set(lang)
		for _, yes := range []string{"enter", "y"} {
			b := cutOffRun()
			b.recoverErr = stuck()
			m := play(t, play(t, loaded(t, b, 120, 30), "R"), "F")
			// Forget's answer, before the disks are read again: the run is
			// closed from that moment, not from the reload, so R and F are
			// gone at once.
			next, cmd := m.Update(snapshotKey(yes))
			next, reload := next.(model).Update(cmd())
			m = next.(model)
			if m.unfinishedState != journal.None || m.recoverFailed || reload == nil {
				t.Fatalf("%s %s: before the reload: state %v, recoverFailed %v", lang, yes, m.unfinishedState, m.recoverFailed)
			}
			if f := footerOf(atRest(m)); strings.Contains(f, "R "+i18n.T("key.recover")) || strings.Contains(f, "F "+i18n.T("key.forget")) {
				t.Fatalf("%s %s: footer before the reload: %q", lang, yes, f)
			}
			next, _ = m.Update(reload())
			m = next.(model).surveyNow()
			// Asked once, and said in a line that names the run.
			if b.forgot != 1 || m.toast != i18n.T("toast.forgot", "Ubuntu-24.04") {
				t.Fatalf("%s %s: forgot %d, toast %q", lang, yes, b.forgot, m.toast)
			}
			if m.mode != modeMain || m.unfinishedState != journal.None || m.recoverFailed || m.settling {
				t.Fatalf("%s %s: mode %v, state %v, recoverFailed %v, settling %v", lang, yes, m.mode, m.unfinishedState, m.recoverFailed, m.settling)
			}
			// Nothing was started again by it: forgetting is not recovering.
			if b.recovered != 1 {
				t.Errorf("%s %s: Recover was asked %d times", lang, yes, b.recovered)
			}
			// s is back, and the keys of the run are gone.
			f := footerOf(atRest(m))
			if !strings.Contains(f, "s "+i18n.T("key.shrink")) || strings.Contains(f, "R "+i18n.T("key.recover")) || strings.Contains(f, "F "+i18n.T("key.forget")) {
				t.Errorf("%s %s: footer %q", lang, yes, f)
			}
			// And s opens the question before a run again.
			if m = play(t, m, "s"); m.mode != modeConfirm {
				t.Errorf("%s %s: s after forgetting: mode %v, toast %q", lang, yes, m.mode, m.toast)
			}
		}
	}
}

func TestForgettingThatFailsKeepsTheRun(t *testing.T) {
	b := cutOffRun()
	b.recoverErr = stuck()
	b.forgetErr = errors.New("the journal could not be closed: access is denied")
	m := play(t, play(t, loaded(t, b, 120, 30), "R"), "F")
	m = play(t, m, "enter")
	// Why it failed is said, and the run stays cut off with both keys.
	if b.forgot != 1 || !strings.Contains(m.toast, "access is denied") || m.unfinishedState != journal.CutOff || m.settling {
		t.Fatalf("forgot %d, toast %q, state %v, settling %v", b.forgot, m.toast, m.unfinishedState, m.settling)
	}
	if f := footerOf(atRest(m)); !strings.Contains(f, "R finish run") || !strings.Contains(f, "F forget") {
		t.Errorf("footer %q", f)
	}
	// F asks again.
	if m = play(t, m, "F"); m.mode != modeForget {
		t.Errorf("F after a failed forgetting: mode %v", m.mode)
	}
}

func TestRAndFWorkOneAtATime(t *testing.T) {
	b := cutOffRun()
	b.recoverErr = stuck()
	m := play(t, loaded(t, b, 120, 30), "R")
	// R at work: the command is out, its answer not yet in.
	next, first := m.Update(snapshotKey("R"))
	m = next.(model)
	if first == nil || !m.settling {
		t.Fatalf("R started nothing: settling %v", m.settling)
	}
	// Meanwhile a second R starts nothing, and F asks nothing: the same
	// things would be started twice, or forgotten while they start.
	for _, key := range []string{"R", "F"} {
		next, cmd := m.Update(snapshotKey(key))
		m = next.(model)
		if cmd != nil || m.mode != modeMain {
			t.Fatalf("%s while R is at work: command %v, mode %v", key, cmd != nil, m.mode)
		}
	}
	// Its answer frees both keys again.
	next, _ = m.Update(first())
	m = next.(model)
	if m.settling || b.recovered != 2 {
		t.Fatalf("settling %v, recovered %d", m.settling, b.recovered)
	}
	// The same while a forgetting is at work.
	m = play(t, m, "F")
	next, forgetting := m.Update(snapshotKey("enter"))
	m = next.(model)
	if forgetting == nil || !m.settling || m.mode != modeMain {
		t.Fatalf("enter started nothing: settling %v, mode %v", m.settling, m.mode)
	}
	for _, key := range []string{"R", "F"} {
		next, cmd := m.Update(snapshotKey(key))
		m = next.(model)
		if cmd != nil || m.mode != modeMain {
			t.Fatalf("%s while F is at work: command %v, mode %v", key, cmd != nil, m.mode)
		}
	}
	// Its answer: forgotten once, and nothing started again by it.
	m.Update(forgetting())
	if b.forgot != 1 || b.recovered != 2 {
		t.Errorf("forgot %d, recovered %d", b.forgot, b.recovered)
	}
}

func TestForgetBoxClosesWhenTheRunEndedElsewhere(t *testing.T) {
	b := cutOffRun()
	b.recoverErr = stuck()
	m := play(t, play(t, loaded(t, b, 120, 30), "R"), "F")
	// Another wsl-shrinker finished the run while the question stood: a
	// reload finds no run, and the question about it goes.
	b.unfinishedState = journal.None
	next, _ := m.Update(m.loadDisks()())
	m = next.(model)
	if m.mode != modeMain || m.recoverFailed {
		t.Fatalf("mode %v, recoverFailed %v", m.mode, m.recoverFailed)
	}
	// And an answer given to a question that is gone forgets nothing.
	if m = play(t, m, "enter"); b.forgot != 0 {
		t.Errorf("forgot %d", b.forgot)
	}
}

func TestNarrowestScreenKeepsTheKeysOfACutOffRunWhole(t *testing.T) {
	defer i18n.Set(i18n.English)
	for _, lang := range []i18n.Lang{i18n.English, i18n.Korean} {
		i18n.Set(lang)
		b := cutOffRun()
		b.recoverErr = stuck()
		m := play(t, loaded(t, b, 40, 12), "R")
		// Why the start failed, whole: three rows of forty cells.
		lines := shown(m)
		if got := joined(lines[8:11]); got != stuckText {
			t.Errorf("%s: the reason is not whole:\n%s", lang, strings.Join(lines, "\n"))
		}
		lines = atRest(m)
		if len(lines) != 12 {
			t.Fatalf("%s: %d rows:\n%s", lang, len(lines), strings.Join(lines, "\n"))
		}
		// The footer names R, F and q with their words, none cut short.
		f := lines[11]
		if lipgloss.Width(f) > 40 {
			t.Errorf("%s: footer %d cells wide: %q", lang, lipgloss.Width(f), f)
		}
		for _, want := range []string{"R " + i18n.T("key.recover"), "F " + i18n.T("key.forget"), "q " + i18n.T("key.quit")} {
			if !strings.Contains(joined([]string{f}), want) {
				t.Errorf("%s: footer %q lacks %q", lang, f, want)
			}
		}
		if strings.Contains(f, "…") {
			t.Errorf("%s: the footer is cut: %q", lang, f)
		}
		// The status says both ways on, whole, over the footer.
		if out := joined(lines); !strings.Contains(out, i18n.T("unfinished.cutoff.failed", "Ubuntu-24.04")) {
			t.Errorf("%s: status:\n%s", lang, strings.Join(lines, "\n"))
		}
	}
}

// noList is a PC whose disks cannot be listed, with whatever its journal
// says.
type noList struct{ *variant }

func (noList) Disks(context.Context) ([]disks.Disk, error) {
	return nil, errors.New("wsl.exe --list --verbose: exit status 1")
}

func TestCutOffRunIsSaidWhereNoDiskIsListed(t *testing.T) {
	defer i18n.Set(i18n.English)
	// The journal is read apart from WSL: a run cut off still waits for R
	// when its disk is gone, and when the disks cannot be listed at all.
	gone := journal.Entry{Disk: "Gone", Distro: "Gone", Step: "stop", Stopped: true}
	for _, lang := range []i18n.Lang{i18n.English, i18n.Korean} {
		i18n.Set(lang)
		empty := &variant{Demo: demo(), disks: func([]disks.Disk) []disks.Disk { return nil }, unfinishedState: journal.CutOff, unfinished: gone}
		failed := noList{&variant{Demo: demo(), unfinishedState: journal.CutOff, unfinished: gone}}
		for name, b := range map[string]Backend{"no disk": empty, "no list": failed} {
			m := loaded(t, b, 80, 24)
			lines := atRest(m)
			if out := joined(lines); !strings.Contains(out, i18n.T("unfinished.cutoff", "Gone", i18n.T("step.stop"))) {
				t.Errorf("%s, %s: the run is not said:\n%s", lang, name, strings.Join(lines, "\n"))
			}
			if f := footerOf(lines); !strings.HasPrefix(f, "R "+i18n.T("key.recover")) {
				t.Errorf("%s, %s: R does not lead the footer: %q", lang, name, f)
			}
		}
		// R works from there, and a failed one offers F as anywhere.
		empty.recoverErr = stuck()
		m := play(t, loaded(t, empty, 80, 24), "R")
		if empty.recovered != 1 {
			t.Fatalf("%s: recovered %d times", lang, empty.recovered)
		}
		if f := footerOf(atRest(m)); !strings.Contains(f, "F "+i18n.T("key.forget")) {
			t.Errorf("%s: footer after a failed R with no disk: %q", lang, f)
		}
		if m = play(t, m, "F"); m.mode != modeForget {
			t.Errorf("%s: F with no disk listed: mode %v", lang, m.mode)
		}
	}
}

func TestHelpNamesTheKeyThatForgets(t *testing.T) {
	defer i18n.Set(i18n.English)
	for _, lang := range []i18n.Lang{i18n.English, i18n.Korean} {
		i18n.Set(lang)
		// Every page of the help, as it is paged through.
		var seen []string
		keys := []string{"?"}
		for range 30 {
			seen = append(seen, screen(demo(), 80, 24, keys...)...)
			keys = append(keys, "down")
		}
		if all := joined(seen); !strings.Contains(all, i18n.T("help.forget")) || !strings.Contains(all, i18n.T("help.recover")) {
			t.Errorf("%s: the help does not say what R and F do", lang)
		}
	}
}

func TestALongMessageTakesThreeRowsAndSaysWhereItIsCut(t *testing.T) {
	m := loaded(t, demo(), 40, 12)
	// Two sentences: what is wrong, then what clears it. The second is
	// the one to act on, so the row gives room for both.
	m.say(stuckText, ui.CatBad)
	lines := shown(m)
	if len(lines) != 12 || joined(lines[8:11]) != stuckText {
		t.Fatalf("the message is not whole on three rows:\n%s", strings.Join(lines, "\n"))
	}
	// The table gives its rows up without losing the selected disk, and
	// the footer stays.
	if _, ok := tableRow(lines, "Ubuntu-24.04"); !ok || !strings.Contains(lines[11], "q") {
		t.Errorf("the table or the footer went:\n%s", strings.Join(lines, "\n"))
	}
	// Longer than three rows: cut inside the width, and marked as cut.
	long := strings.TrimSpace(strings.Repeat("the disk is held by another program and cannot be compacted now; ", 4))
	m.say(long, ui.CatBad)
	lines = shown(m)
	if len(lines) != 12 {
		t.Fatalf("%d rows:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	for _, l := range lines {
		if lipgloss.Width(l) > 40 {
			t.Errorf("a row %d cells wide: %q", lipgloss.Width(l), l)
		}
	}
	if !strings.HasSuffix(lines[10], "…") {
		t.Errorf("a message cut short is not marked: %q", lines[10])
	}
	// What shows is the message from its start, in order.
	if shown := strings.TrimSuffix(joined(lines[8:11]), "…"); !strings.HasPrefix(long, shown) || len(shown) < 80 {
		t.Errorf("shown %q", shown)
	}
	// A message that fits takes its one row, and no mark.
	m.say("Nothing to shrink.", ui.CatBad)
	lines = shown(m)
	if lines[10] != " Nothing to shrink." || strings.Contains(lines[9], "Nothing") {
		t.Errorf("a short message:\n%s", strings.Join(lines, "\n"))
	}
}

func TestHoldKeepsNamesAmountsAndCommandsWhole(t *testing.T) {
	m := newModel(context.Background(), demo())
	text := "Stops Ubuntu-24.04 (67.4 GB): run wsl -d Ubuntu-24.04, then docker start 0123456789ab ba9876543210 inside it; `wsl --shutdown` clears a read-only root."
	// From 15 cells every word of the text fits the column (the longest,
	// "Ubuntu-24.04," is 13 wide and wrapTo keeps one cell free): narrower,
	// a word has to break, which the test after this one is about.
	for w := 15; w <= 90; w++ {
		lines := m.wrapTo(text, w, "", lipgloss.NewStyle())
		// Nothing is lost or changed by the marks: the words come back as
		// they went in.
		if got := strings.Join(lines, " "); got != text {
			t.Fatalf("width %d: %q", w, got)
		}
		// wrapTo keeps one cell free at the right.
		col := w - 1
		for i, l := range lines {
			if lipgloss.Width(l) > col {
				t.Errorf("width %d: line %d is %d cells wide: %q", w, i, lipgloss.Width(l), l)
			}
			// A word that fits the column is never broken at a hyphen...
			for _, end := range []string{"Ubuntu-", "read-", "wsl --", "wsl -", " -"} {
				if strings.HasSuffix(l, end) {
					t.Errorf("width %d: line %d ends in %q: %q", w, i, end, lines)
				}
			}
			// ...an amount stays with its unit...
			if strings.HasSuffix(l, "67.4") || strings.HasSuffix(l, "(67.4") {
				t.Errorf("width %d: the amount lost its unit: %q", w, lines)
			}
			// ...and a command with what it acts on, where both fit.
			if col >= len("wsl -d Ubuntu-24.04,") && (strings.HasSuffix(l, " wsl") || strings.HasSuffix(l, "wsl -d")) {
				t.Errorf("width %d: wsl -d lost its name: %q", w, lines)
			}
			if col >= len("docker start 0123456789ab") && (strings.HasSuffix(l, "docker") || strings.HasSuffix(l, "docker start")) {
				t.Errorf("width %d: docker start lost its id: %q", w, lines)
			}
		}
		// Wide enough for all of docker start, it is on one line.
		if col >= len("docker start 0123456789ab ba9876543210") && !hasLine(lines, "docker start 0123456789ab ba9876543210") {
			t.Errorf("width %d: docker start is parted from its ids: %q", w, lines)
		}
	}
}

func TestHoldLetsAWordWiderThanTheColumnBreak(t *testing.T) {
	m := newModel(context.Background(), demo())
	// A name longer than the column has to break somewhere: it is left its
	// hyphens to break at, and no line runs past the column.
	text := "the disk of a-very-long-hyphenated-distribution-name-that-fits-no-column is held"
	for _, w := range []int{12, 20, 30} {
		lines := m.wrapTo(text, w, "", lipgloss.NewStyle())
		for i, l := range lines {
			if lipgloss.Width(l) > w-1 {
				t.Errorf("width %d: line %d is %d cells wide: %q", w, i, lipgloss.Width(l), l)
			}
		}
		// Nothing of it is lost: taken together the lines are the text,
		// but for the spaces a break stands for.
		if got := strings.ReplaceAll(strings.Join(lines, ""), " ", ""); got != strings.ReplaceAll(text, " ", "") {
			t.Errorf("width %d: %q", w, lines)
		}
		// Where each piece between its hyphens fits the column, the name
		// breaks at them: a line ends at a hyphen or at a word's end, never
		// inside a piece.
		if w >= 20 {
			for _, l := range lines[:len(lines)-1] {
				if !strings.HasSuffix(l, "-") && !strings.Contains(text+" ", l+" ") {
					t.Errorf("width %d: a line ends inside a word: %q", w, lines)
				}
			}
		}
	}
	// And the marks leave nothing behind in what is shown.
	for _, s := range []string{text, "67.4 GB of Ubuntu-24.04", "wsl -d Ubuntu-24.04", "docker start 0123456789ab ba9876543210"} {
		for _, w := range []int{5, 20, 80} {
			held := hold(s, w)
			if got := unhold(held); got != s {
				t.Errorf("hold(%q, %d) comes back as %q", s, w, got)
			}
			if lipgloss.Width(held) != lipgloss.Width(s) {
				t.Errorf("hold(%q, %d) is %d cells wide, not %d", s, w, lipgloss.Width(held), lipgloss.Width(s))
			}
		}
	}
}

func TestADiskStillHeldOffersForgettingToo(t *testing.T) {
	// WSL compacting, or another program holding the disk for as long as it
	// likes: R says so each time, and F is the way out as after a start
	// that failed.
	b := cutOffRun()
	b.recoverErr = app.StillCutOff{Err: errors.New("WSL is still compacting Ubuntu-24.04, or another program holds its disk; try again when it is done")}
	m := play(t, loaded(t, b, 120, 30), "R")
	if !m.recoverFailed || !strings.Contains(m.toast, "another program holds its disk") {
		t.Fatalf("recoverFailed %v, toast %q", m.recoverFailed, m.toast)
	}
	if m = play(t, m, "F"); m.mode != modeForget {
		t.Fatalf("F after a held disk: mode %v", m.mode)
	}
}

func TestForgettingNamesTheRunTheBoxShowed(t *testing.T) {
	b := cutOffRun()
	b.recoverErr = stuck()
	shown := b.unfinished
	m := play(t, play(t, play(t, loaded(t, b, 120, 30), "R"), "F"), "enter")
	// The run the question was about goes with the answer, for the
	// program to refuse any other.
	if b.forgot != 1 || !b.forgotRun.Same(shown) || b.forgotRun.Disk != "Ubuntu-24.04" {
		t.Fatalf("forgot %d, asked about %+v", b.forgot, b.forgotRun)
	}
	_ = m
}

func TestAnotherRunCutOffStartsOverFromR(t *testing.T) {
	// R failed on one run; a reload finds another cut off in its place
	// (the first finished elsewhere, the second cut off since). F waits
	// for R to fail on this one, and a question about the first closes.
	b := cutOffRun()
	b.recoverErr = stuck()
	m := play(t, play(t, loaded(t, b, 120, 30), "R"), "F")
	if m.mode != modeForget {
		t.Fatalf("mode %v", m.mode)
	}
	b.unfinished = journal.Entry{PID: 4242, Disk: "Debian", Distro: "Debian", Step: "stop", Stopped: true}
	next, _ := m.Update(m.loadDisks()())
	m = next.(model)
	if m.mode != modeMain || m.recoverFailed {
		t.Fatalf("mode %v, recoverFailed %v", m.mode, m.recoverFailed)
	}
	lines := atRest(m)
	if f := footerOf(lines); strings.Contains(f, "F forget") {
		t.Errorf("F offered for a run R never tried: %q", f)
	}
	if out := joined(lines); !strings.Contains(out, "The run of Debian was cut off at Stop · R finishes it") {
		t.Errorf("status:\n%s", strings.Join(lines, "\n"))
	}
}

func TestARunStartingElsewhereSaysNoEmptyName(t *testing.T) {
	defer i18n.Set(i18n.English)
	for _, lang := range []i18n.Lang{i18n.English, i18n.Korean} {
		i18n.Set(lang)
		// A journal that does not parse yet: a run starting, its disk not
		// said. The status row says a disk, not a blank.
		b := &variant{Demo: demo(), unfinishedState: journal.Going}
		lines := atRest(loaded(t, b, 120, 30))
		if out := joined(lines); !strings.Contains(out, i18n.T("unfinished.going.unknown")) {
			t.Errorf("%s: status:\n%s", lang, strings.Join(lines, "\n"))
		}
	}
}

func TestShutdownStaysWholeInMessagesAndTheErrorBox(t *testing.T) {
	defer i18n.Set(i18n.English)
	// The reason a start fails, in the status rows, at every width.
	for w := 40; w <= 100; w++ {
		m := loaded(t, demo(), w, 24)
		m.say(stuckText, ui.CatBad)
		for _, l := range shown(m) {
			if t2 := strings.TrimRight(l, " "); strings.HasSuffix(t2, "`wsl") || strings.HasSuffix(t2, "wsl --") {
				t.Errorf("width %d: a row ends in %q", w, t2)
			}
		}
	}
	// WSL not answering: the box says what to do, wsl --shutdown whole,
	// in both languages and at every width.
	for _, lang := range []i18n.Lang{i18n.English, i18n.Korean} {
		i18n.Set(lang)
		for w := 40; w <= 110; w++ {
			b := &variant{Demo: demo()}
			m := loaded(t, noList{b}, w, 30)
			m.err = fmt.Errorf("wsl.exe --list --verbose: %w", wsl.ErrNoAnswer)
			for _, l := range shown(m) {
				if t2 := strings.TrimSpace(l); strings.HasSuffix(t2, "wsl") || strings.HasSuffix(t2, "wsl --") || strings.HasPrefix(t2, "--shutdown") {
					t.Errorf("%s width %d: a line ends or starts inside wsl --shutdown: %q", lang, w, t2)
				}
			}
		}
	}
}

func TestDockerStartStaysOneHoweverNarrow(t *testing.T) {
	m := newModel(context.Background(), demo())
	text := "then docker start 0123456789ab ba9876543210 inside it"
	for w := 14; w <= 40; w++ {
		for _, l := range m.wrapTo(text, w, "", lipgloss.NewStyle()) {
			if strings.HasSuffix(l, "docker") {
				t.Errorf("width %d: a line ends in docker: %q", w, l)
			}
		}
	}
}

func TestARunEndedWhileTheQuestionStoodIsSaidSo(t *testing.T) {
	defer i18n.Set(i18n.English)
	for _, lang := range []i18n.Lang{i18n.English, i18n.Korean} {
		i18n.Set(lang)
		b := cutOffRun()
		b.recoverErr = stuck()
		b.forgetErr = app.ErrNothingToForget
		m := play(t, play(t, loaded(t, b, 120, 30), "R"), "F")
		next, cmd := m.Update(snapshotKey("enter"))
		next, _ = next.(model).Update(cmd())
		m = next.(model)
		// Said as what happened, and the run no longer holds anything back.
		if m.toast != i18n.T("toast.forgot.gone") || m.unfinishedState != journal.None || m.recoverFailed {
			t.Fatalf("%s: toast %q, state %v", lang, m.toast, m.unfinishedState)
		}
	}
}

func TestTheClockSlowsDownWhenNothingMoves(t *testing.T) {
	// Idle, the screen is drawn twice a second, not eight times: often
	// enough for a passing message to go on time.
	m := loaded(t, demo(), 120, 30)
	if m.moving() || frameDelay(m.moving()) != 500*time.Millisecond {
		t.Fatalf("idle: moving %v", m.moving())
	}
	// Anything with a spinner or a clock on the screen keeps the quick
	// frames.
	for name, f := range map[string]func(*model){
		"disks loading":     func(m *model) { m.loading = true },
		"a look inside":     func(m *model) { m.surveys["Ubuntu-24.04"] = &surveyState{loading: true} },
		"a run going":       func(m *model) { m.run = &runState{} },
		"R or F at work":    func(m *model) { m.settling = true },
		"a run's preparing": func(m *model) { m.preparing = true },
		"a new window":      func(m *model) { m.reopening = true },
	} {
		c := m
		c.surveys = map[string]*surveyState{}
		f(&c)
		if !c.moving() || frameDelay(c.moving()) != 120*time.Millisecond {
			t.Errorf("%s: moving %v", name, c.moving())
		}
	}
	// A run that has ended has nothing moving.
	c := m
	c.run = &runState{done: true}
	if c.moving() {
		t.Error("an ended run keeps the quick frames")
	}
}

func TestKoreanClosesACutOffRunAndNamesThePastRunsOnce(t *testing.T) {
	// "Forget" in Korean reads as closing the cut-off run, never as 잊다:
	// the question, its keys, the footer, the status row and help say 닫다.
	// The past runs go by one word, in the footer as in help.
	defer i18n.Set(i18n.English)
	i18n.Set(i18n.Korean)
	b := cutOffRun()
	b.recoverErr = stuck()
	m := play(t, loaded(t, b, 120, 30), "R")
	main := atRest(m)
	box := shown(play(t, m, "F"))
	var helps []string
	keys := []string{"?"}
	for range 30 {
		helps = append(helps, screen(demo(), 80, 24, keys...)...)
		keys = append(keys, "down")
	}
	for _, want := range []string{"끊긴 Ubuntu-24.04 줄이기를 닫을까요?", "⏎ 닫기"} {
		if !strings.Contains(joined(box), want) {
			t.Errorf("no %q in the question:\n%s", want, strings.Join(box, "\n"))
		}
	}
	if f := footerOf(main); !strings.Contains(f, "F 닫기") {
		t.Errorf("footer %q", f)
	}
	if out := joined(helps); !strings.Contains(out, "R로 마무리하지 못한 끊긴 줄이기 닫기") {
		t.Errorf("help:\n%s", out)
	}
	for _, lines := range [][]string{main, box, helps} {
		if out := joined(lines); strings.Contains(out, "잊") {
			t.Errorf("says 잊:\n%s", out)
		}
	}
	// No Korean words of the program's say it either.
	for k := range catalogKeys(t)[i18n.Korean] {
		if strings.Contains(i18n.T(k), "잊") {
			t.Errorf("%s: %q", k, i18n.T(k))
		}
	}
	if i18n.T("help.history") != i18n.T("key.history") {
		t.Errorf("help names the past runs %q, the footer %q", i18n.T("help.history"), i18n.T("key.history"))
	}
}
