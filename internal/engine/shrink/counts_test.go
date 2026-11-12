package shrink

import (
	"context"
	"strings"
	"testing"

	"github.com/kanggu12/wsl-shrinker/internal/engine/survey"
	"github.com/kanggu12/wsl-shrinker/internal/machine/spawner"
)

func TestOneOfAThingIsCountedAsOne(t *testing.T) {
	// The steps' details, as the command line prints them: one container,
	// one script, one item, never "1 containers"; and the person's scripts
	// by the name everything else gives them, not "hooks".
	one := twoIDs[:1]
	for _, c := range []struct{ got, want string }{
		{stoppedItself(one, 1, 0).Detail, "1 container, 1 pre-stop script"},
		{stoppedItself(twoIDs, 2, 1).Detail, "2 containers, 2 pre-stop scripts, 1 failed"},
		{stoppedBySpawner(one, 1, 0, spawner.EndClean, "").Detail, "1 container, by wsl-spawner, 1 pre-stop script"},
		{stoppedItselfInstead(one, 0, 0, "Dev is left to WSL").Detail, "1 container; wsl-spawner stopped nothing (Dev is left to WSL), so wsl-shrinker did"},
		{lostWords(1), "1 block the filesystem uses is no longer stored after compacting: data went missing"},
		{lostWords(3), "3 blocks the filesystem uses are no longer stored after compacting: data went missing"},
	} {
		if c.got != c.want {
			t.Errorf("%q, want %q", c.got, c.want)
		}
	}
	// A run with one item chosen and one container: its clean step, its stop
	// and its start again.
	w := newWorld(t)
	inner := w.fake.Script
	w.fake.Script = func(d, u, s string) (string, error) {
		switch {
		case strings.Contains(s, "docker ps -q"):
			return "running|0123456789ab\n", nil
		case strings.Contains(s, "docker start"):
			return "started|0123456789ab\n", nil
		}
		return inner(d, u, s)
	}
	item := survey.Item{ID: "apt", What: "apt", Bytes: 1 << 20, Command: "apt-get clean"}
	r := Run(context.Background(), w.env, w.disk(), Options{Clean: []survey.Item{item}}, nil)
	details := map[Step]string{}
	for _, e := range r.Events {
		if e.Status != Running {
			details[e.Step] = e.Detail
		}
	}
	if r.Err != "" || details[StepClean] != "1 item, about 1.0 MB" || details[StepStop] != "1 container, 1 pre-stop script" || details[StepRestart] != "running, 1 container started again" {
		t.Fatalf("%s: %q", r.Err, details)
	}
	// One of one that did not start.
	w = newWorld(t)
	inner = w.fake.Script
	w.fake.Script = func(d, u, s string) (string, error) {
		switch {
		case strings.Contains(s, "docker ps -q"):
			return "running|0123456789ab\n", nil
		case strings.Contains(s, "docker start"):
			return "", nil
		}
		return inner(d, u, s)
	}
	r = Run(context.Background(), w.env, w.disk(), Options{}, nil)
	if last := r.Events[len(r.Events)-1]; last.Detail != "running, 1 of 1 container not started again" {
		t.Fatalf("%q", last.Detail)
	}
}
