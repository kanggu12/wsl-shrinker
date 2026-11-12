package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/kanggu12/wsl-shrinker/internal/engine/shrink"
	"github.com/kanggu12/wsl-shrinker/internal/front/i18n"
	"github.com/kanggu12/wsl-shrinker/internal/machine/spawner"
)

// spawnerStub is wsl-spawner behind the real engine of an engineWorld: it
// says one step, ends the distribution as the real one does after its
// stop (unless it stopped nothing), and answers res and err.
type spawnerStub struct {
	w   *engineWorld
	res spawner.Result
	err error
	// keepRunning leaves the distribution running, as a stop left half
	// done may.
	keepRunning bool
}

func (s spawnerStub) Stop(ctx context.Context, distro string, on func(spawner.Step)) (spawner.Result, error) {
	if s.err != nil {
		return s.res, s.err
	}
	on(spawner.Step{Name: "docker", Count: 2})
	if !s.keepRunning && s.res.End != spawner.EndBusy {
		_ = s.w.fake.Terminate(ctx, distro)
	}
	return s.res, nil
}

func TestTheStopRowSaysWhoStoppedTheDistribution(t *testing.T) {
	defer i18n.Set(i18n.English)
	for _, lang := range []i18n.Lang{i18n.English, i18n.Korean} {
		i18n.Set(lang)
		for _, c := range []struct {
			name string
			res  spawner.Result
			err  error
			// mark is the stop row's; words what the row must say.
			mark  string
			words []string
			// not are words the row must not say.
			not []string
		}{
			// Every step in its time: by wsl-spawner, with the containers and
			// the person's own script, and nothing failed.
			{name: "clean", res: spawner.Result{End: spawner.EndClean}, mark: "✓",
				words: []string{i18n.TN("detail.stop.spawner", 2, 2), i18n.TN("detail.hooks", 1, 1)},
				not:   []string{i18n.T("detail.hooks.failed"), i18n.T("detail.spawner.limit")}},
			// A step out of time: marked for a look, and said; the scripts did
			// not fail, so that is not said.
			{name: "limit", res: spawner.Result{End: spawner.EndLimit}, mark: "!",
				words: []string{i18n.TN("detail.stop.spawner", 2, 2), i18n.T("detail.spawner.limit")},
				not:   []string{i18n.T("detail.hooks.failed")}},
			// It stopped nothing: this program did, and says so.
			{name: "not stopped", err: fmt.Errorf("%w: Dev is left to WSL in the settings", spawner.ErrNotStopped), mark: "✓",
				words: []string{i18n.TN("detail.stop", 2, 2), i18n.T("detail.spawner.notstopped")},
				not:   []string{i18n.TN("detail.stop.spawner", 2, 2)}},
		} {
			t.Run(string(lang)+"/"+c.name, func(t *testing.T) {
				w := newEngineWorld(t)
				w.env.Spawner = spawnerStub{w: w, res: c.res, err: c.err}
				b := &engineBackend{Demo: demo(), w: w}
				lines := screen(b, 120, 34, "s", "enter")
				if m := stepMarks(lines)[shrink.StepStop]; m != c.mark {
					t.Errorf("stop is marked %q, want %q:\n%s", m, c.mark, strings.Join(lines, "\n"))
				}
				row := stepRow(lines, shrink.StepStop)
				for _, want := range c.words {
					if !strings.Contains(row, want) {
						t.Errorf("stop row %q, want %q", row, want)
					}
				}
				for _, bad := range c.not {
					if strings.Contains(row, bad) {
						t.Errorf("stop row %q says %q", row, bad)
					}
				}
			})
		}
	}
}

func TestAStopOfThisProgramsOwnThatWarnsStillMeansItsScripts(t *testing.T) {
	// A run's events from before wsl-spawner was known (a warned stop said
	// "some failed" of the scripts), and one now with the count: both say it.
	for _, e := range []shrink.Event{
		{Step: shrink.StepStop, Status: shrink.Warned, Count: 2, Hooks: 2},
		{Step: shrink.StepStop, Status: shrink.Warned, Count: 2, Hooks: 2, HooksFailed: 1},
	} {
		if s := stepDetail(e); !strings.Contains(s, i18n.T("detail.hooks.failed")) {
			t.Errorf("%+v: %q", e, s)
		}
	}
	// wsl-spawner's step that failed is its own, not the scripts'.
	e := shrink.Event{Step: shrink.StepStop, Status: shrink.Warned, Count: 2, By: shrink.BySpawner, Spawner: spawner.EndFailed}
	if s := stepDetail(e); strings.Contains(s, i18n.T("detail.hooks.failed")) || !strings.Contains(s, i18n.T("detail.spawner.failed")) {
		t.Errorf("%q", s)
	}
}

func TestTheRunningStopRowNamesWslSpawnersStep(t *testing.T) {
	// The stop step runs through wsl-spawner, at its Docker step: the row
	// says that step, and the sentence under the steps says how it stops.
	at := time.Now()
	b := &scripted{Demo: demo(), events: []shrink.Event{
		{Step: shrink.StepCheck, Status: shrink.Done, At: at},
		{Step: shrink.StepClean, Status: shrink.Skipped, At: at},
		{Step: shrink.StepStop, Status: shrink.Running, By: shrink.BySpawner, Phase: "docker", At: at},
	}}
	out := ansi.Strip(Snapshot(b, 120, 34, true, []string{"s", "enter"}, string(shrink.StepStop)))
	lines := strings.Split(out, "\n")
	if row := stepRow(lines, shrink.StepStop); !strings.Contains(row, i18n.T("run.spawner.docker")) {
		t.Errorf("stop row %q", row)
	}
	about := strings.Join(strings.Fields(i18n.T("run.about.stop.spawner")), " ")
	if !strings.Contains(strings.Join(strings.Fields(out), " "), about) {
		t.Errorf("no %q:\n%s", about, out)
	}
	// A step this program has no words for is named as wsl-spawner names it.
	if got := phaseWords("defrost"); got != "defrost" {
		t.Errorf("%q", got)
	}
	// Every step wsl-spawner names has words of its own: in Korean they
	// differ from its name, but for the two programs' names.
	i18n.Set(i18n.Korean)
	defer i18n.Set(i18n.English)
	for p, want := range map[string]string{"hook": "", "units": "", "sync": "", spawner.StepTerminate: "", "docker": "Docker", "podman": "Podman"} {
		got := phaseWords(p)
		if want != "" && got != want || want == "" && (got == p || got == "") {
			t.Errorf("%s says %q", p, got)
		}
	}
}

func TestAStepThatSaysMoreKeepsItsTime(t *testing.T) {
	// The containers named, then wsl-spawner's steps: the stop's time counts
	// from its first event, not from each.
	m := newModel(context.Background(), demo())
	m.run = &runState{steps: map[shrink.Step]shrink.Event{}}
	t0 := time.Now()
	for i, e := range []shrink.Event{
		{Step: shrink.StepStop, Status: shrink.Running, At: t0},
		{Step: shrink.StepStop, Status: shrink.Running, Containers: []string{"0123456789ab"}, At: t0.Add(time.Second)},
		{Step: shrink.StepStop, Status: shrink.Running, By: shrink.BySpawner, Phase: "docker", At: t0.Add(5 * time.Second)},
	} {
		next, _ := m.Update(eventMsg(e))
		m = next.(model)
		if !m.run.stepStart.Equal(t0) {
			t.Fatalf("event %d moved the step's start to %v", i, m.run.stepStart)
		}
	}
	// The next step starts its own time.
	next, _ := m.Update(eventMsg(shrink.Event{Step: shrink.StepCompact, Status: shrink.Running, At: t0.Add(9 * time.Second)}))
	if m = next.(model); !m.run.stepStart.Equal(t0.Add(9 * time.Second)) {
		t.Fatalf("compaction starts at %v", m.run.stepStart)
	}
}

func TestARunLeftToAnotherStopSaysSo(t *testing.T) {
	// Another wsl-spawner stop ends the distribution: the outcome says so
	// in both languages, never that it runs as it was.
	defer i18n.Set(i18n.English)
	for _, lang := range []i18n.Lang{i18n.English, i18n.Korean} {
		i18n.Set(lang)
		w := newEngineWorld(t)
		w.env.Spawner = spawnerStub{w: w, res: spawner.Result{End: spawner.EndBusy, Err: "another stop of Dev is running"}}
		b := &engineBackend{Demo: demo(), w: w}
		out := joined(screen(b, 120, 34, "s", "enter"))
		if !strings.Contains(out, said("run.state.stop.otherstop")) || strings.Contains(out, said("run.state.stop.running")) {
			t.Errorf("%s:\n%s", lang, out)
		}
	}
}

func TestTheStopRowSaysWhatWslSpawnerSaid(t *testing.T) {
	// Its own words, after the screen's: why it stopped nothing, and what
	// went wrong after its steps.
	for _, c := range []struct {
		name  string
		stub  spawnerStub
		words string
	}{
		{name: "not stopped", stub: spawnerStub{err: fmt.Errorf("%w: Dev is left to WSL in the settings", spawner.ErrNotStopped)}, words: "Dev is left to WSL in the settings"},
		{name: "not ended", stub: spawnerStub{res: spawner.Result{End: spawner.EndClean, Err: "WSL did not end Dev: timed out"}}, words: "WSL did not end Dev: timed out"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newEngineWorld(t)
			c.stub.w = w
			w.env.Spawner = c.stub
			lines := screen(&engineBackend{Demo: demo(), w: w}, 180, 34, "s", "enter")
			if row := stepRow(lines, shrink.StepStop); !strings.Contains(row, c.words) {
				t.Errorf("stop row %q, want %q", row, c.words)
			}
		})
	}
}

func TestAStopThatFailedThroughWslSpawnerSaysWhyInTheScreensLanguage(t *testing.T) {
	// The failed stop row says why in Korean too, not in the engine's
	// English.
	defer i18n.Set(i18n.English)
	i18n.Set(i18n.Korean)
	for _, c := range []struct {
		name string
		stub spawnerStub
		key  string
	}{
		{name: "busy", stub: spawnerStub{res: spawner.Result{End: spawner.EndBusy, Err: "another stop of Dev is running"}}, key: "stop.fail.otherstop"},
		{name: "still runs", stub: spawnerStub{res: spawner.Result{End: spawner.EndError, Err: "the stop inside Dev did not run to its end"}, keepRunning: true}, key: "stop.fail.stillruns"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newEngineWorld(t)
			c.stub.w = w
			w.env.Spawner = c.stub
			lines := screen(&engineBackend{Demo: demo(), w: w}, 180, 34, "s", "enter")
			out := strings.Join(strings.Fields(strings.Join(lines, " ")), " ")
			want := strings.Join(strings.Fields(i18n.T(c.key, "Dev")), " ")
			if !strings.Contains(out, want) || strings.Contains(out, "wsl-shrinker left it to that stop") || strings.Contains(out, "and it still ran") {
				t.Errorf("want %q:\n%s", want, strings.Join(lines, "\n"))
			}
		})
	}
}
