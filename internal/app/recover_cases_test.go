package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kanggu12/wsl-shrinker/internal/engine/journal"
	"github.com/kanggu12/wsl-shrinker/internal/engine/shrink"
	"github.com/kanggu12/wsl-shrinker/internal/machine/winapi"
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

// A run cut off during the compaction of a distribution it found stopped:
// WSL goes on compacting on its own and holds the disk until it is done.
// Recover waits, the journal kept, and closes the run once WSL lets go.
func TestRecoverWaitsWhileWSLStillCompacts(t *testing.T) {
	a, fake := testApp(t, func(int, string) bool { return false })
	fake.List[0].Running = false
	a.StatFile = func(p string) (winapi.FileInfo, error) {
		fi, err := winapi.Stat(p)
		fi.Held = true
		return fi, err
	}
	if err := a.Journal.Begin(journal.Entry{PID: 1, Exe: "x", Disk: "Dev", Distro: "Dev", Step: "compact"}); err != nil {
		t.Fatal(err)
	}
	if msg, err := a.Recover(context.Background()); err == nil || !strings.Contains(err.Error(), "still compacting Dev") {
		t.Fatalf("recover: %q %v", msg, err)
	}
	if _, st, _ := a.Journal.Read(); st != journal.CutOff {
		t.Fatalf("journal %v, want it kept", st)
	}
	// WSL is done: the disk reads cleanly, nothing was stopped to start.
	a.StatFile = nil
	if msg, err := a.Recover(context.Background()); err != nil || msg != "nothing was stopped" {
		t.Fatalf("recover: %q %v", msg, err)
	}
	if _, st, _ := a.Journal.Read(); st != journal.None {
		t.Fatalf("journal %v, want none", st)
	}
	// The distribution was never started by recover.
	if calls := strings.Join(fake.CallList(), " "); strings.Contains(calls, "start:Dev") {
		t.Fatalf("calls %s", calls)
	}
}

// An update of the journal the file refuses (another program has run.json
// open a moment longer than Update waits) is tried again with the next
// event: the stop is on record before the compaction, where a run cut off
// would need it.
func TestAJournalUpdateRefusedIsTriedAgain(t *testing.T) {
	var written []journal.Entry
	refuse := 1
	f := &follower{j: journal.Entry{Disk: "Dev", Distro: "Dev", Step: "check"}, update: func(e journal.Entry) error {
		if refuse > 0 {
			refuse--
			return errors.New("the file is in use")
		}
		written = append(written, e)
		return nil
	}}
	f.see(shrink.Event{Step: shrink.StepStop, Status: shrink.Running})
	if len(written) != 0 {
		t.Fatalf("written %v", written)
	}
	// The next event changes nothing, yet the stop goes on record now.
	f.see(shrink.Event{Step: shrink.StepStop, Status: shrink.Done})
	if len(written) != 1 || !written[0].Stopped || written[0].Step != "stop" {
		t.Fatalf("written %+v", written)
	}
	// Up to date: an event that changes nothing writes nothing.
	f.see(shrink.Event{Step: shrink.StepStop, Status: shrink.Done})
	if len(written) != 1 {
		t.Fatalf("written again: %+v", written)
	}
}
