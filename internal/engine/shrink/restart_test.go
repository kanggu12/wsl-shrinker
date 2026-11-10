package shrink

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kanggu12/wsl-shrinker/internal/machine/wsl"
)

// starts counts the starts of Dev in the fake's calls.
func starts(w *world) int {
	return strings.Count(strings.Join(w.fake.CallList(), " "), "start:Dev")
}

func TestAStartWSLRefusesAtFirstIsTriedAgain(t *testing.T) {
	// WSL can refuse a start for a moment right after a compaction: the
	// run tries again, seconds apart, and the distribution comes back with
	// its containers.
	w := newWorld(t)
	var waits []time.Duration
	w.env.Sleep = func(d time.Duration) { waits = append(waits, d) }
	w.fake.Fail = map[string]error{"start:Dev": errors.New("Wsl/Service/CreateInstance/E_UNEXPECTED")}
	w.fake.Flaky = map[string]int{"start:Dev": 2}
	r := Run(context.Background(), w.env, w.disk(), Options{}, nil)
	if r.Err != "" || r.Left != LeftRunning || statuses(r)[StepRestart] != Done || r.ContainersStarted != 2 {
		t.Fatalf("%s, left %s, %v, %d started", r.Err, r.Left, statuses(r), r.ContainersStarted)
	}
	if n := starts(w); n != 3 {
		t.Fatalf("%d starts: %v", n, w.fake.CallList())
	}
	// The tries are seconds apart, the first at once.
	if len(waits) < 2 || !slices.Equal(waits[:2], []time.Duration{3 * time.Second, 10 * time.Second}) {
		t.Fatalf("waits %v", waits)
	}
	if last := r.Events[len(r.Events)-1]; !strings.Contains(last.Detail, "try 3") {
		t.Fatalf("restart says %q", last.Detail)
	}
}

func TestADistributionThatWillNotStartIsLeftDownAfterThreeTries(t *testing.T) {
	w := newWorld(t)
	w.fake.Fail = map[string]error{"start:Dev": errors.New("WSL_E_VM_MODE_INVALID_STATE")}
	r := Run(context.Background(), w.env, w.disk(), Options{}, nil)
	if r.Left != LeftDown || statuses(r)[StepRestart] != Failed || starts(w) != len(startTries) {
		t.Fatalf("left %s, %v, %d starts", r.Left, statuses(r), starts(w))
	}
	// A WSL that did not answer in time is not asked again: minutes more
	// would not make it answer.
	w = newWorld(t)
	w.fake.Fail = map[string]error{"start:Dev": fmt.Errorf("%w: wsl --distribution Dev --exec true ran for 2m0s", wsl.ErrNoAnswer)}
	r = Run(context.Background(), w.env, w.disk(), Options{}, nil)
	if r.Left != LeftDown || starts(w) != 1 {
		t.Fatalf("left %s, %d starts", r.Left, starts(w))
	}
}

func TestADistributionThatStopsBeforeItsRootIsReadIsStartedAgain(t *testing.T) {
	// With a short instanceIdleTimeout, WSL stops the distribution between
	// the start and the look at its root: the next try starts it again,
	// rather than leave it down.
	w := newWorld(t)
	inner := w.fake.Script
	gone := false
	w.fake.Script = func(d, u, s string) (string, error) {
		if strings.Contains(s, "/proc/mounts") && !gone {
			gone = true
			return "", fmt.Errorf("Dev is %w", wsl.ErrNotRunning)
		}
		return inner(d, u, s)
	}
	r := Run(context.Background(), w.env, w.disk(), Options{}, nil)
	if r.Err != "" || r.Left != LeftRunning || starts(w) != 2 {
		t.Fatalf("%s, left %s, %d starts", r.Err, r.Left, starts(w))
	}
}

func TestAContainerThatWaitsOnAnotherStartsInASecondRound(t *testing.T) {
	// The second container runs on the first one's network: started before
	// it, it fails, and a second round a little later starts it. Only the
	// ones that failed go round again.
	w := newWorld(t)
	inner := w.fake.Script
	var rounds []string
	w.fake.Script = func(d, u, s string) (string, error) {
		if !strings.Contains(s, "docker start") {
			return inner(d, u, s)
		}
		rounds = append(rounds, s)
		if len(rounds) == 1 {
			return "failed|fedcba987654\nstarted|0123456789ab\n", nil
		}
		return "started|fedcba987654\n", nil
	}
	r := Run(context.Background(), w.env, w.disk(), Options{}, nil)
	if r.Err != "" || r.ContainersStarted != 2 || r.ContainersNotStarted != 0 || statuses(r)[StepRestart] != Done {
		t.Fatalf("%s: %d started, %d not, %v", r.Err, r.ContainersStarted, r.ContainersNotStarted, statuses(r))
	}
	if len(rounds) != 2 || strings.Contains(rounds[1], "0123456789ab") || !strings.Contains(rounds[1], "fedcba987654") {
		t.Fatalf("rounds %q", rounds)
	}
	// One that fails both rounds is counted once, and said.
	w = newWorld(t)
	inner = w.fake.Script
	w.fake.Script = func(d, u, s string) (string, error) {
		if strings.Contains(s, "docker start") {
			return "failed|fedcba987654\nstarted|0123456789ab\n", nil
		}
		return inner(d, u, s)
	}
	r = Run(context.Background(), w.env, w.disk(), Options{}, nil)
	if r.ContainersStarted != 1 || r.ContainersNotStarted != 1 || statuses(r)[StepRestart] != Warned {
		t.Fatalf("%d started, %d not, %v", r.ContainersStarted, r.ContainersNotStarted, statuses(r))
	}
}
