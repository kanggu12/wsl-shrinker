package shrink

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kanggu12/wsl-shrinker/internal/machine/spawner"
	"github.com/kanggu12/wsl-shrinker/internal/testutil/shtest"
)

// fakeSpawner plays wsl-spawner for a run: it answers with res and err,
// says steps first, and ends the distribution as the real one does after
// its stop (all but busy, a background stop and one that found nothing
// running), unless keepRunning, as a stop that broke off may leave it.
type fakeSpawner struct {
	w           *world
	res         spawner.Result
	err         error
	steps       []spawner.Step
	keepRunning bool
	// after runs as its stop returns: what changes then (WSL that stops
	// answering, say).
	after func()
	// asked are the distributions it was asked to stop; log is the world's
	// order of scripts and calls, "spawner" for this one.
	asked []string
	log   *[]string
}

func (f *fakeSpawner) Stop(ctx context.Context, distro string, on func(spawner.Step)) (spawner.Result, error) {
	f.asked = append(f.asked, distro)
	*f.log = append(*f.log, "spawner")
	for _, s := range f.steps {
		on(s)
	}
	// One that stopped nothing ends nothing either.
	ends := f.res.End != spawner.EndBusy && !f.res.NotRunning && !f.res.Detached && !errors.Is(f.err, spawner.ErrNotStopped)
	if ends && !f.keepRunning {
		// WSL ends what is left, as wsl-spawner's last step.
		_ = f.w.fake.Terminate(ctx, distro)
	}
	if f.after != nil {
		f.after()
	}
	return f.res, f.err
}

// withSpawner gives the world a fake wsl-spawner and a log of what ran in
// the distribution, in order: "list" (the containers named), "hooks" (the
// person's scripts alone), "stop" (this program's stop: containers, hooks,
// sync), "containers" (containers and sync, no hooks), "start", and
// "spawner".
func withSpawner(w *world, f *fakeSpawner) *fakeSpawner {
	log := []string{}
	f.w, f.log = w, &log
	inner := w.fake.Script
	w.fake.Script = func(d, u, s string) (string, error) {
		hooks, stops := strings.Contains(s, "pre-stop.d"), strings.Contains(s, "docker stop")
		switch {
		case strings.Contains(s, "docker ps -q"):
			log = append(log, "list")
		case hooks && stops:
			log = append(log, "stop")
		case hooks:
			log = append(log, "hooks")
		case stops:
			log = append(log, "containers")
		case strings.Contains(s, "docker start"):
			log = append(log, "start")
		}
		return inner(d, u, s)
	}
	w.env.Spawner = f
	return f
}

// stopEvents are the run's events of the stop step.
func stopEvents(r Result) []Event {
	var out []Event
	for _, e := range r.Events {
		if e.Step == StepStop {
			out = append(out, e)
		}
	}
	return out
}

// lastStop is the stop step's last event.
func lastStop(t *testing.T, r Result) Event {
	t.Helper()
	es := stopEvents(r)
	if len(es) == 0 {
		t.Fatalf("no stop step: %+v", r.Events)
	}
	return es[len(es)-1]
}

func TestWithWslSpawnerTheRunStopsThroughIt(t *testing.T) {
	w := newWorld(t)
	f := withSpawner(w, &fakeSpawner{res: spawner.Result{End: spawner.EndClean},
		steps: []spawner.Step{{Name: "docker", Count: 2}, {Name: "sync"}, {Name: spawner.StepTerminate}}})
	r := Run(context.Background(), w.env, w.disk(), Options{}, nil)
	if r.Err != "" || r.Left != LeftRunning {
		t.Fatalf("%s, left %s: %+v", r.Err, r.Left, r.Events)
	}
	// Asked once, for the distribution; the containers were named first,
	// the person's scripts ran before it, and this program stopped none of
	// them itself.
	if !slices.Equal(f.asked, []string{"Dev"}) {
		t.Fatalf("asked %q", f.asked)
	}
	if want := []string{"list", "hooks", "spawner", "start"}; !slices.Equal(*f.log, want) {
		t.Fatalf("ran %q, want %q", *f.log, want)
	}
	// Each of its steps showed as it began, then the step's end says who
	// stopped it and how, with the containers and the hooks.
	var phases []string
	for _, e := range stopEvents(r) {
		if e.Status == Running && e.Phase != "" {
			phases = append(phases, e.Phase)
		}
	}
	if !slices.Equal(phases, []string{"docker", "sync", spawner.StepTerminate}) {
		t.Fatalf("phases %q", phases)
	}
	e := lastStop(t, r)
	if e.Status != Done || e.By != BySpawner || e.Spawner != spawner.EndClean || e.Count != 2 || e.Hooks != 1 || !strings.Contains(e.Detail, "by wsl-spawner") {
		t.Fatalf("%+v", e)
	}
	// The compaction ran, and the containers came back.
	if statuses(r)[StepCompact] != Done || r.ContainersStarted != 2 {
		t.Fatalf("%v, %d started", statuses(r), r.ContainersStarted)
	}
}

func TestWslSpawnerThatStopsNothingLeavesTheStopToTheRun(t *testing.T) {
	// It leaves the distribution to WSL (exit 2): the run stops the
	// containers itself, its hooks run once, before.
	w := newWorld(t)
	f := withSpawner(w, &fakeSpawner{err: fmt.Errorf("%w: Dev is left to WSL in the settings; wsl-spawner set Dev --skip=false makes it wsl-spawner's to stop", spawner.ErrNotStopped)})
	r := Run(context.Background(), w.env, w.disk(), Options{}, nil)
	if r.Err != "" || r.Left != LeftRunning || r.ContainersStarted != 2 {
		t.Fatalf("%s, left %s, %d started", r.Err, r.Left, r.ContainersStarted)
	}
	if want := []string{"list", "hooks", "spawner", "containers", "start"}; !slices.Equal(*f.log, want) {
		t.Fatalf("ran %q, want %q", *f.log, want)
	}
	e := lastStop(t, r)
	// Said as its own words give it, up to what to change in its settings.
	if e.By != "" || e.Spawner != SpawnerNotStopped || !strings.Contains(e.Detail, "Dev is left to WSL in the settings") || strings.Contains(e.Detail, "--skip") {
		t.Fatalf("%+v", e)
	}
}

func TestTheContainersStopThatFollowsStopsThemAndSyncsAlone(t *testing.T) {
	// The script of the stop a wsl-spawner that stopped nothing leaves:
	// the containers in one call and the sync, and no hooks a second time.
	s := containersStopScript(twoIDs, true)
	if !strings.Contains(s, "docker stop 0123456789ab fedcba987654") || !strings.HasSuffix(s, "sync\n") || strings.Contains(s, "pre-stop.d") {
		t.Fatalf("%s", s)
	}
	// This program's own stop is the same script as before: the
	// containers, the hooks, the sync.
	if got := stopScript(twoIDs); !strings.HasPrefix(got, "docker stop 0123456789ab fedcba987654 >/dev/null\n") || !strings.Contains(got, hooksScript) || !strings.HasSuffix(got, "fi\nsync\n") {
		t.Fatalf("%s", got)
	}
}

func TestAStopThatRanOutOfTimeOrFailedAStepIsMarkedAndTheRunGoesOn(t *testing.T) {
	for end, words := range map[string]string{spawner.EndLimit: "a step ran out of time", spawner.EndFailed: "a step failed"} {
		w := newWorld(t)
		withSpawner(w, &fakeSpawner{res: spawner.Result{End: end}})
		r := Run(context.Background(), w.env, w.disk(), Options{}, nil)
		e := lastStop(t, r)
		if r.Err != "" || e.Status != Warned || e.Spawner != end || !strings.Contains(e.Detail, words) || statuses(r)[StepCompact] != Done {
			t.Fatalf("%s: %s %+v %v", end, r.Err, e, statuses(r))
		}
	}
}

func TestAStopThatDidNotRunToItsEndGoesOnOnlyWhenTheDistributionIsDown(t *testing.T) {
	for _, c := range []struct {
		name        string
		res         spawner.Result
		err         error
		keepRunning bool
		// failed: the stop step fails; otherwise it is marked and the
		// compaction runs.
		failed bool
	}{
		{name: "error, ended", res: spawner.Result{End: spawner.EndError, Err: "the stop inside Dev did not run to its end"}},
		{name: "error, still running", res: spawner.Result{End: spawner.EndError, Err: "the stop inside Dev did not run to its end"}, keepRunning: true, failed: true},
		{name: "no result, ended", err: fmt.Errorf("%w: killed", spawner.ErrNoResult)},
		{name: "no result, still running", err: fmt.Errorf("%w: killed", spawner.ErrNoResult), keepRunning: true, failed: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t)
			f := withSpawner(w, &fakeSpawner{res: c.res, err: c.err, keepRunning: c.keepRunning})
			// A stop of ErrNoResult ends it too, as the real one may have.
			r := Run(context.Background(), w.env, w.disk(), Options{}, nil)
			e := lastStop(t, r)
			if c.failed {
				// The distribution still runs: the step fails, nothing is
				// compacted, and its containers start again.
				if e.Status != Failed || r.FailedAt != StepStop || slices.Contains(w.fake.CallList(), "compact:Dev") || r.Left != LeftRunning || !slices.Contains(*f.log, "start") {
					t.Fatalf("%+v, failed at %q, left %q, calls %v, ran %q", e, r.FailedAt, r.Left, w.fake.CallList(), *f.log)
				}
				return
			}
			if r.Err != "" || e.Status != Warned || e.By != BySpawner || statuses(r)[StepCompact] != Done || r.Left != LeftRunning {
				t.Fatalf("%s %+v %v left %q", r.Err, e, statuses(r), r.Left)
			}
		})
	}
}

func TestAnotherWslSpawnerStopIsLeftToEndIt(t *testing.T) {
	// The sign-out hook's stop runs: this run stops nothing more, starts
	// nothing again, and says so.
	w := newWorld(t)
	f := withSpawner(w, &fakeSpawner{res: spawner.Result{End: spawner.EndBusy, Err: "another stop of Dev is running"}})
	r := Run(context.Background(), w.env, w.disk(), Options{}, nil)
	// Left as that stop leaves it: not "running as it was", which it is
	// not for long.
	if r.FailedAt != StepStop || !strings.Contains(r.Err, "another wsl-spawner stop is running for Dev") || r.Left != LeftOtherStop {
		t.Fatalf("failed at %q: %q, left %q", r.FailedAt, r.Err, r.Left)
	}
	calls := strings.Join(w.fake.CallList(), " ")
	if strings.Contains(calls, "start:") || strings.Contains(calls, "compact:") || slices.Contains(*f.log, "start") {
		t.Fatalf("calls %s, ran %q", calls, *f.log)
	}
}

func TestAStopThatWentOnInTheBackgroundIsWaitedFor(t *testing.T) {
	// It took the distribution for the terminal's own and went on alone:
	// the run waits until the distribution is down, then compacts.
	w := newWorld(t)
	withSpawner(w, &fakeSpawner{res: spawner.Result{Detached: true}})
	// The waits of the look, until the third ends it; the restart's own
	// waits after are not counted.
	polls, ended := 0, false
	w.env.Sleep = func(d time.Duration) {
		if d == backgroundPoll && !ended {
			polls++
			if polls == 3 {
				w.fake.List[0].Running, ended = false, true
			}
		}
	}
	r := Run(context.Background(), w.env, w.disk(), Options{}, nil)
	e := lastStop(t, r)
	if r.Err != "" || e.By != BySpawner || !strings.Contains(e.Detail, "in the background") || polls != 3 || statuses(r)[StepCompact] != Done {
		t.Fatalf("%s %+v, %d polls, %v", r.Err, e, polls, statuses(r))
	}
	// One that never ends it fails the step, once the waits are over, and
	// is ended so that it starts afresh, as that stop would have ended it.
	w = newWorld(t)
	withSpawner(w, &fakeSpawner{res: spawner.Result{Detached: true}})
	r = Run(context.Background(), w.env, w.disk(), Options{}, nil)
	if r.FailedAt != StepStop || !strings.Contains(r.Err, "in the background, and it still ran") || !callsBefore(w.fake.CallList(), "terminate:Dev", "start:Dev") {
		t.Fatalf("failed at %q: %s; calls %v", r.FailedAt, r.Err, w.fake.CallList())
	}
}

func TestADistributionFoundStoppedByWslSpawnerFailsTheStep(t *testing.T) {
	// It stopped on its own between the check and the stop.
	w := newWorld(t)
	withSpawner(w, &fakeSpawner{res: spawner.Result{NotRunning: true}})
	r := Run(context.Background(), w.env, w.disk(), Options{}, nil)
	if r.FailedAt != StepStop || !strings.Contains(r.Err, "not running") {
		t.Fatalf("failed at %q: %s", r.FailedAt, r.Err)
	}
}

func TestWithoutWslSpawnerTheStopIsThisProgramsOwn(t *testing.T) {
	// Not installed: the containers, the hooks and the sync in one script,
	// as before wsl-spawner was known.
	w := newWorld(t)
	f := withSpawner(w, &fakeSpawner{})
	w.env.Spawner = nil
	r := Run(context.Background(), w.env, w.disk(), Options{}, nil)
	e := lastStop(t, r)
	if r.Err != "" || len(f.asked) != 0 || e.By != "" || e.Spawner != "" || !slices.Equal(*f.log, []string{"list", "stop", "start"}) {
		t.Fatalf("%s %+v asked %q ran %q", r.Err, e, f.asked, *f.log)
	}
}

func TestAContainerRunningAgainIsNotStartedASecondTime(t *testing.T) {
	// One came back with the distribution (wsl-spawner's start, or its
	// restart policy): it is left as it is, the other is started.
	dir := shtest.Tools(t)
	asked := shtest.Docker{OS: its, Running: twoIDs[:1]}.Put(t, dir)
	out, err := shtest.Run(t, dir, startScript(twoIDs))
	if err != nil {
		t.Fatalf("%v %s", err, out)
	}
	if want := "running|" + twoIDs[0] + "\nstarted|" + twoIDs[1] + "\n"; out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
	if got := shtest.Asked(t, asked); !slices.Equal(got, []string{"start " + twoIDs[1]}) {
		t.Fatalf("docker was asked for %q", got)
	}
	// Both run again: the run counts both, and says neither failed.
	w := newWorld(t)
	inner := w.fake.Script
	w.fake.Script = func(d, u, s string) (string, error) {
		if strings.Contains(s, "docker start") {
			return "running|0123456789ab\nstarted|fedcba987654\n", nil
		}
		return inner(d, u, s)
	}
	r := Run(context.Background(), w.env, w.disk(), Options{}, nil)
	if r.ContainersStarted != 2 || r.ContainersNotStarted != 0 || statuses(r)[StepRestart] != Done {
		t.Fatalf("%d started, %d not, %v", r.ContainersStarted, r.ContainersNotStarted, statuses(r))
	}
}

func TestTheStopsNewWordsGoIntoTheJSONOnlyWhenSaid(t *testing.T) {
	e := Event{Step: StepStop, Status: Warned, By: BySpawner, Phase: "docker", Spawner: spawner.EndLimit, HooksFailed: 1, At: time.Unix(0, 0).UTC()}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"by":"wsl-spawner"`, `"phase":"docker"`, `"spawner":"limit"`, `"hooks_failed":1`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("no %s in %s", want, b)
		}
	}
	var back Event
	if err := json.Unmarshal(b, &back); err != nil || back.By != e.By || back.Phase != e.Phase || back.Spawner != e.Spawner || back.HooksFailed != 1 {
		t.Fatalf("%+v %v", back, err)
	}
	// An event of this program's own stop has none of them.
	b, _ = json.Marshal(Event{Step: StepStop, Status: Done})
	for _, key := range []string{"by", "phase", "spawner", "hooks_failed"} {
		if strings.Contains(string(b), `"`+key+`"`) {
			t.Errorf("%q in %s", key, b)
		}
	}
}

func TestAWslSpawnerThatGaveNoReasonStillReadsAsOneThatStoppedNothing(t *testing.T) {
	// It stopped nothing and said nothing: the stop is this program's, and
	// the step says wsl-spawner stopped nothing, with a reason all the same.
	w := newWorld(t)
	withSpawner(w, &fakeSpawner{err: fmt.Errorf("%w:", spawner.ErrNotStopped)})
	r := Run(context.Background(), w.env, w.disk(), Options{}, nil)
	e := lastStop(t, r)
	if r.Err != "" || e.Spawner != SpawnerNotStopped || !strings.Contains(e.Detail, "wsl-spawner stopped nothing (") || strings.Contains(e.Detail, "nothing ()") {
		t.Fatalf("%s %+v", r.Err, e)
	}
}

// callsBefore reports whether call a comes before call b in the fake's
// calls, both there.
func callsBefore(calls []string, a, b string) bool {
	i, j := slices.Index(calls, a), slices.Index(calls, b)
	return i >= 0 && j >= 0 && i < j
}

func TestAStopLeftHalfDoneIsEndedSoItStartsAfresh(t *testing.T) {
	// wsl-spawner's stop did not run to its end, or said no result, and
	// the distribution still runs: wsl-spawner may have stopped Docker's
	// daemon, its units and Podman, which only a fresh start brings back.
	// The run ends it before it starts it again, and compacts nothing.
	for name, f := range map[string]*fakeSpawner{
		"error":     {res: spawner.Result{End: spawner.EndError, Err: "the stop inside Dev did not run to its end"}, keepRunning: true},
		"no result": {err: fmt.Errorf("%w: killed", spawner.ErrNoResult), keepRunning: true},
		// Every step in its time, then WSL did not end it: the same.
		"not ended": {res: spawner.Result{End: spawner.EndClean, Err: "WSL did not end Dev: the operation timed out"}, keepRunning: true},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			withSpawner(w, f)
			r := Run(context.Background(), w.env, w.disk(), Options{}, nil)
			calls := w.fake.CallList()
			if r.FailedAt != StepStop || slices.Contains(calls, "compact:Dev") || r.Left != LeftRunning || r.ContainersStarted != 2 {
				t.Fatalf("failed at %q, left %q, %d started, calls %v", r.FailedAt, r.Left, r.ContainersStarted, calls)
			}
			if !callsBefore(calls, "terminate:Dev", "start:Dev") {
				t.Fatalf("not ended before its start: %v", calls)
			}
		})
	}
}

func TestAStopWhoseEndWSLDoesNotSayFailsTheStep(t *testing.T) {
	// After wsl-spawner's stop, WSL does not answer which distributions
	// run: whether this one still runs is not known, and the run does not
	// compact it on a guess.
	w := newWorld(t)
	withSpawner(w, &fakeSpawner{res: spawner.Result{End: spawner.EndError, Err: "the distribution did not answer"}, keepRunning: true,
		after: func() { w.fake.DistrosErr = errors.New("WSL did not answer in time") }})
	r := Run(context.Background(), w.env, w.disk(), Options{}, nil)
	if r.FailedAt != StepStop || slices.Contains(w.fake.CallList(), "compact:Dev") {
		t.Fatalf("failed at %q: %s; calls %v", r.FailedAt, r.Err, w.fake.CallList())
	}
}

func TestAStopWSLDidNotEndThatEndedAnywayIsMarkedWithWhy(t *testing.T) {
	// Every step in its time, wsl-spawner says WSL did not end it, yet it
	// is down: the run goes on, the step marked for a look with what
	// wsl-spawner said, not done.
	w := newWorld(t)
	withSpawner(w, &fakeSpawner{res: spawner.Result{End: spawner.EndClean, Err: "WSL did not end Dev: the operation timed out"}})
	r := Run(context.Background(), w.env, w.disk(), Options{}, nil)
	e := lastStop(t, r)
	if r.Err != "" || e.Status != Warned || !strings.Contains(e.Detail, "WSL did not end Dev") || statuses(r)[StepCompact] != Done {
		t.Fatalf("%s %+v %v", r.Err, e, statuses(r))
	}
}

func TestTheHistoryIsNamedOnlyForWslSpawnersOwnSteps(t *testing.T) {
	// Its steps did not all go in their time: its history has them.
	e := stoppedBySpawner(twoIDs, 1, 0, spawner.EndLimit, "")
	if !strings.Contains(e.Detail, "wsl-spawner history has the steps") {
		t.Errorf("%q", e.Detail)
	}
	// Only a script of the person's own failed: nothing of wsl-spawner's
	// to look up there.
	e = stoppedBySpawner(twoIDs, 1, 1, spawner.EndClean, "")
	if e.Status != Warned || strings.Contains(e.Detail, "wsl-spawner history") {
		t.Errorf("%s %q", e.Status, e.Detail)
	}
}

func TestWhatWslSpawnerSaidGoesWithTheStepAndItsFailure(t *testing.T) {
	// Said on the stop step's event, for the screen to show in its row.
	e := stoppedItselfInstead(twoIDs, 0, 0, "Dev is left to WSL in the settings", heard{said: "Dev is left to WSL in the settings"})
	if e.SpawnerSaid != "Dev is left to WSL in the settings" {
		t.Errorf("%+v", e)
	}
	e = stoppedBySpawner(twoIDs, 0, 0, spawner.EndClean, "WSL did not end Dev")
	if e.SpawnerSaid != "WSL did not end Dev" {
		t.Errorf("%+v", e)
	}
	// And on a failed step, with a code the screen has words for.
	w := newWorld(t)
	withSpawner(w, &fakeSpawner{res: spawner.Result{End: spawner.EndError, Err: "the stop inside Dev did not run to its end"}, keepRunning: true})
	r := Run(context.Background(), w.env, w.disk(), Options{}, nil)
	f := lastStop(t, r)
	if f.Status != Failed || f.Code != CodeStillRuns || f.SpawnerSaid != "the stop inside Dev did not run to its end" {
		t.Errorf("%+v", f)
	}
	w = newWorld(t)
	withSpawner(w, &fakeSpawner{res: spawner.Result{End: spawner.EndBusy}})
	if f := lastStop(t, Run(context.Background(), w.env, w.disk(), Options{}, nil)); f.Code != CodeOtherStop {
		t.Errorf("%+v", f)
	}
}

func TestOnlyWslSpawnersOwnWordsGoAsWhatItSaid(t *testing.T) {
	// What this program says of a call that said nothing (it printed
	// nothing, it ran out of time, Windows did not run it) is a code the
	// screen has words for; what wsl-spawner said is its own words alone.
	// The English detail and error are the command line's, as before.
	for _, c := range []struct {
		name string
		f    *fakeSpawner
		// status, said, quiet and exit are the stop step's last event's.
		status Status
		said   string
		quiet  string
		exit   int
		// english is a part of the step's detail, or of the run's error.
		english string
	}{
		{name: "stopped nothing, silent", f: &fakeSpawner{err: spawner.CallError{Kind: spawner.ErrNotStopped, Quiet: spawner.QuietSilent, Exit: 2}},
			status: Done, quiet: spawner.QuietSilent, exit: 2, english: "wsl-spawner stopped nothing (it printed nothing and exited 2), so wsl-shrinker did"},
		{name: "stopped nothing, in its words", f: &fakeSpawner{err: spawner.CallError{Kind: spawner.ErrNotStopped, Said: "Dev is left to WSL; wsl-spawner set Dev --skip=false"}},
			status: Done, said: "Dev is left to WSL", english: "wsl-spawner stopped nothing (Dev is left to WSL), so wsl-shrinker did"},
		{name: "no result, out of time, still running", f: &fakeSpawner{err: spawner.CallError{Kind: spawner.ErrNoResult, Quiet: spawner.QuietTimeout, System: "context deadline exceeded"}, keepRunning: true},
			status: Failed, quiet: spawner.QuietTimeout, english: "(wsl-spawner said no result: wsl-spawner ran out of the time a run gives it (context deadline exceeded))"},
		{name: "no result, in its words, ended", f: &fakeSpawner{err: spawner.CallError{Kind: spawner.ErrNoResult, Said: "panic: out of memory"}},
			status: Warned, said: "panic: out of memory", english: "(wsl-spawner said no result: panic: out of memory)"},
		{name: "no result, system, ended", f: &fakeSpawner{err: spawner.CallError{Kind: spawner.ErrNoResult, Quiet: spawner.QuietSystem, System: "exec: WaitDelay expired before I/O complete"}},
			status: Warned, quiet: spawner.QuietSystem, english: "(wsl-spawner said no result: exec: WaitDelay expired before I/O complete)"},
		// An error of another stopper's, not one of the call's: what follows
		// the words of its kind is taken for wsl-spawner's.
		{name: "no result, as another stopper says it", f: &fakeSpawner{err: fmt.Errorf("%w: killed", spawner.ErrNoResult)},
			status: Warned, said: "killed", english: "(wsl-spawner said no result: killed)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t)
			withSpawner(w, c.f)
			r := Run(context.Background(), w.env, w.disk(), Options{}, nil)
			e := lastStop(t, r)
			if e.Status != c.status || e.SpawnerSaid != c.said || e.SpawnerQuiet != c.quiet || e.SpawnerExit != c.exit {
				t.Fatalf("%s, said %q, quiet %q, exit %d: %+v", e.Status, e.SpawnerSaid, e.SpawnerQuiet, e.SpawnerExit, e)
			}
			if !strings.Contains(e.Detail+" "+r.Err, c.english) {
				t.Fatalf("no %q in %q / %q", c.english, e.Detail, r.Err)
			}
		})
	}
	// Into the JSON when there is something to say, and back.
	e := Event{Step: StepStop, Status: Done, Spawner: SpawnerNotStopped, SpawnerQuiet: spawner.QuietSilent, SpawnerExit: 2, At: time.Unix(0, 0).UTC()}
	b, err := json.Marshal(e)
	if err != nil || !strings.Contains(string(b), `"spawner_quiet":"silent"`) || !strings.Contains(string(b), `"spawner_exit":2`) {
		t.Fatalf("%v %s", err, b)
	}
	var back Event
	if err := json.Unmarshal(b, &back); err != nil || back.SpawnerQuiet != spawner.QuietSilent || back.SpawnerExit != 2 {
		t.Fatalf("%+v %v", back, err)
	}
	b, _ = json.Marshal(Event{Step: StepStop, Status: Done})
	if strings.Contains(string(b), "spawner_quiet") || strings.Contains(string(b), "spawner_exit") {
		t.Fatalf("%s", b)
	}
}

func TestTheLookAtWslSpawnersLockSeesAStopAtWork(t *testing.T) {
	root := t.TempDir()
	locks := filepath.Join(root, "run", "wsl-spawner")
	if err := os.MkdirAll(locks, 0o755); err != nil {
		t.Fatal(err)
	}
	look := func(tools string) string {
		t.Helper()
		out, err := shtest.Run(t, tools, spawnerLockScript(root))
		if err != nil {
			t.Fatalf("%v %s", err, out)
		}
		return out
	}
	// With flock, as wsl-spawner holds its stop.lock: held while a stop
	// runs, free after, and no lock file is no stop.
	withFlock := shtest.Tools(t, "flock", "sleep")
	if out := look(withFlock); out != "" {
		t.Fatalf("no lock file: %q", out)
	}
	lock := filepath.Join(locks, "stop.lock")
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if out := look(withFlock); out != "" {
		t.Fatalf("a lock no one holds: %q", out)
	}
	holder := exec.Command("flock", lock, "sleep", "30")
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Process.Kill(); _ = holder.Wait() }()
	// flock takes it at once in the holder; wait until it is held.
	held := false
	for range 50 {
		if exec.Command("flock", "-n", lock, "true").Run() != nil {
			held = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !held {
		t.Fatal("the holder never held the lock")
	}
	if out := look(withFlock); out != "spawner|stopping\n" {
		t.Fatalf("a held lock: %q", out)
	}
	// Without flock, wsl-spawner keeps a folder with its stop's pid: at
	// work while that process runs.
	noFlock := shtest.Tools(t)
	dir := filepath.Join(locks, "stop")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pid"), []byte(fmt.Sprintf("%d\n", holder.Process.Pid)), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := look(noFlock); out != "spawner|stopping\n" {
		t.Fatalf("a running stop's folder: %q", out)
	}
	_ = holder.Process.Kill()
	_ = holder.Wait()
	if out := look(noFlock); out != "" {
		t.Fatalf("a stop that ended: %q", out)
	}
	// It needs no tool that picks text apart.
	if tool := shtest.TextTool(spawnerLockScript("")); tool != "" {
		t.Fatalf("needs %s", tool)
	}
}

func TestAStopAtWorkIsSeenOnlyWhereWslSpawnerIs(t *testing.T) {
	w := newWorld(t)
	inner := w.fake.Script
	w.fake.Script = func(d, u, s string) (string, error) {
		if strings.Contains(s, "/run/wsl-spawner") {
			return "spawner|stopping\n", nil
		}
		return inner(d, u, s)
	}
	// Not installed: nothing is looked at.
	if busy, err := SpawnerStopping(context.Background(), w.env, "Dev"); busy || err != nil {
		t.Fatalf("%v %v", busy, err)
	}
	withSpawner(w, &fakeSpawner{})
	if busy, err := SpawnerStopping(context.Background(), w.env, "Dev"); !busy || err != nil {
		t.Fatalf("%v %v", busy, err)
	}
	// A distribution that does not run has no stop at work in it.
	w.fake.List[0].Running = false
	if busy, err := SpawnerStopping(context.Background(), w.env, "Dev"); busy || err != nil {
		t.Fatalf("%v %v", busy, err)
	}
}
