package app

import (
	"context"
	"errors"
	"testing"

	"github.com/kanggu12/wsl-shrinker/internal/engine/journal"
	"github.com/kanggu12/wsl-shrinker/internal/engine/shrink"
)

// A run cut off while Dev was down; then Dev is unregistered. Recover must
// not leave the journal (the lock) behind forever.
func TestRecoverAfterUnregister(t *testing.T) {
	a, fake := testApp(t, func(int, string) bool { return false })
	d := dev(t, a)
	if err := a.Journal.Begin(journal.Entry{PID: 1, Exe: "x", Disk: "Dev", Distro: "Dev", Step: "compact", Stopped: true}); err != nil {
		t.Fatal(err)
	}
	// wsl --unregister Dev
	fake.List = nil
	fake.Fail = map[string]error{"start:Dev": errors.New("wsl --distribution Dev --exec true: exit status 1: There is no distribution with the supplied name.")}
	for i := 0; i < 2; i++ {
		said, err := a.Recover(context.Background())
		t.Logf("recover #%d: %q %v", i, said, err)
	}
	_, st, _ := a.Journal.Read()
	t.Logf("journal state after recover: %v", st)
	res := a.Shrink(context.Background(), d, shrink.Options{}, nil)
	t.Logf("another run: err=%q", res.Err)
	if st != journal.None {
		t.Errorf("journal stays %v; every run is refused: %s", st, res.Err)
	}
}
