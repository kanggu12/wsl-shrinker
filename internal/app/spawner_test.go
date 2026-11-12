package app

import (
	"context"
	"strings"
	"testing"

	"github.com/kanggu12/wsl-shrinker/internal/engine/journal"
	"github.com/kanggu12/wsl-shrinker/internal/engine/shrink"
	"github.com/kanggu12/wsl-shrinker/internal/machine/spawner"
	"github.com/kanggu12/wsl-shrinker/internal/machine/wsl"
)

// stubSpawner is a wsl-spawner that stops the fake's distribution cleanly,
// as the real one does at its end, and counts its stops.
type stubSpawner struct {
	fake  *wsl.Fake
	stops int
	// during is the journal as its stop runs: what a run cut off then
	// would leave to start again.
	read   func() journal.Entry
	during journal.Entry
}

func (s *stubSpawner) Stop(ctx context.Context, distro string, on func(spawner.Step)) (spawner.Result, error) {
	s.stops++
	s.during = s.read()
	on(spawner.Step{Name: "docker", Count: 2})
	_ = s.fake.Terminate(ctx, distro)
	return spawner.Result{End: spawner.EndClean}, nil
}

func TestARunStopsThroughTheWslSpawnerFoundForIt(t *testing.T) {
	a, fake := testApp(t, func(int, string) bool { return false })
	stub := &stubSpawner{fake: fake, read: func() journal.Entry { e, _, _ := a.Journal.Read(); return e }}
	found := 0
	a.FindSpawner = func() (spawner.Stopper, bool) { found++; return stub, true }
	res := a.Shrink(context.Background(), dev(t, a), shrink.Options{}, nil)
	if res.Err != "" || stub.stops != 1 || found == 0 {
		t.Fatalf("%s: %d stops, looked %d times", res.Err, stub.stops, found)
	}
	// The journal named the containers before wsl-spawner stopped them: a
	// run cut off during its stop starts them again.
	if !stub.during.Stopped || strings.Join(stub.during.Containers, " ") != "0123456789ab fedcba987654" {
		t.Fatalf("while wsl-spawner stopped it, the journal said %+v", stub.during)
	}
	var stop shrink.Event
	for _, e := range res.Events {
		if e.Step == shrink.StepStop {
			stop = e
		}
	}
	if stop.By != shrink.BySpawner || stop.Status != shrink.Done {
		t.Fatalf("%+v", stop)
	}
	// Found again for the next run, as it may have been removed since.
	a.FindSpawner = func() (spawner.Stopper, bool) { return nil, false }
	fake.List[0].Running = true
	res = a.Shrink(context.Background(), dev(t, a), shrink.Options{}, nil)
	for _, e := range res.Events {
		if e.Step == shrink.StepStop && e.By != "" {
			t.Fatalf("stopped by %q with none found", e.By)
		}
	}
	if stub.stops != 1 {
		t.Fatalf("%d stops", stub.stops)
	}
}

func TestAnAppOfATestFindsNoWslSpawner(t *testing.T) {
	// An App made by hand, as every test makes one, looks for none: the
	// real one on a PC would stop a real distribution of a test's name.
	a, _ := testApp(t, func(int, string) bool { return false })
	if a.FindSpawner != nil || a.shrinkEnv(context.Background()).Spawner != nil {
		t.Fatal("a test's App has a wsl-spawner")
	}
	// And the program's own finder finds none in a test either.
	if s, ok := findSpawner(); ok {
		t.Fatalf("found %v", s)
	}
}
