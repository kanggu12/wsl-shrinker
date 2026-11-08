package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/kanggu12/wsl-shrinker/internal/engine/disks"
	"github.com/kanggu12/wsl-shrinker/internal/engine/shrink"

	"github.com/kanggu12/wsl-shrinker/internal/engine/survey"
	"github.com/kanggu12/wsl-shrinker/internal/machine/wsl"
)

// A survey still in flight when 'r' is pressed, with the cursor moved off
// that distribution before the reload lands: its answer finds no state.
func TestReviewSurveyAnswerAfterReloadAndMove(t *testing.T) {
	d := demo()
	m := newModel(context.Background(), d)
	l, _ := d.Disks(m.ctx)
	next, _ := m.Update(disksMsg{list: l})
	m = next.(model) // survey of Ubuntu-24.04 now in flight (loading)
	if s := m.surveys["Ubuntu-24.04"]; s == nil || !s.loading {
		t.Fatalf("expected survey in flight: %+v", s)
	}
	next, _ = m.Update(snapshotKey("r")) // deletes the in-flight state
	m = next.(model)
	next, _ = m.Update(snapshotKey("down")) // to the Docker disk: no survey
	m = next.(model)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic when the old survey answers: %v", r)
		}
	}()
	r, _ := d.Survey(m.ctx, "Ubuntu-24.04")
	m.Update(surveyMsg{distro: "Ubuntu-24.04", r: r})
}

// A survey that lands while the clean list is open (a reload of a stale
// survey) and finds less: the cursor is left past the end.
func TestReviewCleanListShrinksUnderCursor(t *testing.T) {
	m := playKeys(t, demo(), []string{"c", "G"})
	if m.mode != modeClean {
		t.Fatalf("mode %v", m.mode)
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic on space after the list shrank: %v", r)
		}
	}()
	// The survey comes back with one item (or with an error: none).
	next, _ := m.Update(surveyMsg{distro: "Ubuntu-24.04", r: survey.Result{Items: []survey.Item{
		{ID: "apt", What: "apt", Label: "/var/cache/apt/archives", Bytes: 1 << 20, Kind: survey.KindCache, Command: "apt-get clean"}}}})
	m = next.(model)
	m.Update(snapshotKey("space"))
}

func TestReviewCleanListEmptiedByFailedSurvey(t *testing.T) {
	m := playKeys(t, demo(), []string{"c"})
	next, _ := m.Update(surveyMsg{distro: "Ubuntu-24.04", err: context.DeadlineExceeded})
	m = next.(model)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic on space with an empty list: %v", r)
		}
	}()
	m.Update(snapshotKey("space"))
}

// How the clean list gets a survey in flight: open it on a survey just
// under a minute old, then a reload lands.
func TestReviewReloadStartsSurveyInCleanMode(t *testing.T) {
	m := playKeys(t, demo(), []string{"c"})
	m.surveys["Ubuntu-24.04"].at = time.Now().Add(-2 * time.Minute)
	l, _ := m.b.Disks(m.ctx)
	next, cmd := m.Update(disksMsg{list: l})
	m = next.(model)
	if m.mode == modeClean && cmd != nil && m.surveys["Ubuntu-24.04"].loading {
		t.Fatalf("a reload started a new survey under the open clean list")
	}
}

// The window a run moved to: while prepareRun looks inside again, keys that
// would start another run do nothing; then exactly the asked-for run starts.
func TestReviewAutoRunOverAManualRun(t *testing.T) {
	d := demo()
	m := newModel(context.Background(), d)
	m.width, m.height = 120, 34
	m.pending = &Options{Run: []string{"Ubuntu-24.04"}, Clean: map[string][]string{"Ubuntu-24.04": {"apt"}}}
	l, _ := d.Disks(m.ctx)
	next, prep := m.Update(disksMsg{list: l})
	m = next.(model).surveyNow()
	for _, k := range []string{"s", "enter"} {
		next, _ = m.Update(snapshotKey(k))
		m = next.(model)
	}
	if m.mode == modeRun || m.run != nil {
		t.Fatalf("keys started a run while the moved run was getting ready")
	}
	next, _ = m.Update(prep())
	m = next.(model)
	if m.mode != modeRun || m.run == nil || len(m.run.queue) != 1 {
		t.Fatalf("the asked-for run did not start: mode %v", m.mode)
	}
}

// The confirm box is open; WSL's version arrives (or a reload lands) and
// makes the disk blocked; enter then starts a run of nothing.
func TestReviewConfirmThenBlockedEmptyQueue(t *testing.T) {
	m := playKeys(t, demo(), []string{"s"})
	if m.mode != modeConfirm {
		t.Fatalf("mode %v", m.mode)
	}
	next, _ := m.Update(infoMsg(wsl.Info{Version: "2.0.14.0", CanCompact: false}))
	m = next.(model)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic on enter with an empty queue: %v", r)
		}
	}()
	m.Update(snapshotKey("enter"))
}

// scripted plays a fixed list of events, then a result with an error.
type scripted struct {
	*Demo
	events []shrink.Event
}

func (s *scripted) Shrink(_ context.Context, d disks.Disk, _ shrink.Options, emit func(shrink.Event)) shrink.Result {
	for _, e := range s.events {
		emit(e)
	}
	return settledLike(d, shrink.Result{Disk: d.Name, Err: "failed", Events: s.events})
}

func TestReviewCompactAndRestartBothFailed(t *testing.T) {
	b := &scripted{Demo: demo(), events: []shrink.Event{
		{Step: shrink.StepCheck, Status: shrink.Done}, {Step: shrink.StepStop, Status: shrink.Done},
		{Step: shrink.StepCompact, Status: shrink.Failed}, {Step: shrink.StepRestart, Status: shrink.Failed},
	}}
	out := ansi.Strip(Snapshot(b, 120, 30, true, []string{"s", "enter"}, ""))
	// Not shrunk, not started: the screen says both, and no format verb leaks.
	if strings.Contains(out, "was shrunk") || strings.Contains(out, "%[") || !strings.Contains(out, "did not start again; to start it: wsl -d Ubuntu-24.04") {
		t.Fatalf("\n%s", out)
	}
}

func TestReviewVerifyFailedAfterCompaction(t *testing.T) {
	b := &scripted{Demo: demo(), events: []shrink.Event{
		{Step: shrink.StepCheck, Status: shrink.Done}, {Step: shrink.StepCompact, Status: shrink.Done},
		{Step: shrink.StepVerify, Status: shrink.Failed},
		// The disk did not read as before: the engine leaves it stopped.
		{Step: shrink.StepRestart, Status: shrink.Skipped, Detail: "left stopped: check the disk first"},
	}}
	m := playKeys(t, b, []string{"space", "s", "enter"})
	for !m.run.done {
		next, _ := m.Update(<-m.run.events)
		m = next.(model)
	}
	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "Ubuntu-24.04 was shrunk, with a problem") || !strings.Contains(out, "does not read as before") {
		t.Fatalf("\n%s", out)
	}
	// Compacted: not marked to be shrunk again.
	if m.marked["Ubuntu-24.04"] {
		t.Fatal("a compacted disk stayed marked")
	}
}
