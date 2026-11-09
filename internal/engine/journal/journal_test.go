package journal

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// alive makes a process check that answers yes for the ids given.
func alive(ids ...int) func(int, string) bool {
	return func(pid int, _ string) bool { return slices.Contains(ids, pid) }
}

func TestOneRunAtATime(t *testing.T) {
	s := Open(t.TempDir(), alive(100))
	if err := s.Begin(Entry{PID: 100, Exe: "wsl-shrinker.exe", Disk: "Dev", Step: "check"}); err != nil {
		t.Fatal(err)
	}
	// A second run, while the first one's process lives, is refused.
	err := s.Begin(Entry{PID: 200, Disk: "Debian"})
	if !errors.Is(err, ErrBusy) || !strings.Contains(err.Error(), "Dev is being shrunk") {
		t.Fatalf("got %v", err)
	}
	if _, st, _ := s.Read(); st != Going {
		t.Fatalf("state %v", st)
	}
	// Once it ended and removed its journal, the next one goes.
	if err := s.End(); err != nil {
		t.Fatal(err)
	}
	if err := s.Begin(Entry{PID: 200, Disk: "Debian"}); err != nil {
		t.Fatal(err)
	}
}

func TestARunCutOffKeepsWhatToStartAgain(t *testing.T) {
	// The process died after the stop step: its journal says what it
	// stopped, and no new run starts until that is finished.
	s := Open(t.TempDir(), alive())
	e := Entry{PID: 100, Exe: "wsl-shrinker.exe", Disk: "Dev", Distro: "Dev", Step: "check"}
	if err := s.Begin(e); err != nil {
		t.Fatal(err)
	}
	e.Step, e.Stopped, e.Containers = "compact", true, []string{"0123456789ab"}
	if err := s.Update(e); err != nil {
		t.Fatal(err)
	}
	got, st, err := s.Read()
	if err != nil || st != CutOff || !got.Stopped || len(got.Containers) != 1 || got.Step != "compact" {
		t.Fatalf("%+v %v %v", got, st, err)
	}
	err = s.Begin(Entry{PID: 300, Disk: "Debian"})
	if !errors.Is(err, ErrBusy) || !strings.Contains(err.Error(), "cut off at compact") || !strings.Contains(err.Error(), "recover") {
		t.Fatalf("got %v", err)
	}
}

func TestDamagedJournalIsACutOffRun(t *testing.T) {
	// Cut off as it was being written: nothing is known, but it is not
	// taken for "no run".
	s := Open(t.TempDir(), alive())
	if err := os.WriteFile(s.Path, []byte(`{"pid": 1`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Just made, it is a run starting, whose entry is on its way: nobody
	// may take it for a run cut off and remove it.
	if _, st, err := s.Read(); st != Going || err != nil {
		t.Fatalf("young: %v %v", st, err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(s.Path, old, old); err != nil {
		t.Fatal(err)
	}
	if _, st, err := s.Read(); st != CutOff || err != nil {
		t.Fatalf("%v %v", st, err)
	}
	if err := s.Begin(Entry{PID: 2}); !errors.Is(err, ErrBusy) {
		t.Fatalf("got %v", err)
	}
	// Ending it twice is no error.
	for range 2 {
		if err := s.End(); err != nil {
			t.Fatal(err)
		}
	}
	if _, st, _ := s.Read(); st != None {
		t.Fatalf("state %v", st)
	}
}

func TestBeginWritesTheEntryWhole(t *testing.T) {
	// The journal appears with its entry in it, never empty, and nothing
	// of the way it was written is left beside it.
	dir := t.TempDir()
	s := Open(dir, alive())
	if err := s.Begin(Entry{PID: 7, Disk: "Dev", Step: "check"}); err != nil {
		t.Fatal(err)
	}
	if e, st, err := s.Read(); err != nil || st != CutOff || e.Disk != "Dev" || e.PID != 7 {
		t.Fatalf("%+v %v %v", e, st, err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "run-*.tmp")); len(left) != 0 {
		t.Fatalf("left %v", left)
	}
	// A second run is refused, its entry not written anywhere.
	if err := s.Begin(Entry{PID: 8, Disk: "Other"}); !errors.Is(err, ErrBusy) {
		t.Fatalf("got %v", err)
	}
	if e, _, _ := s.Read(); e.Disk != "Dev" {
		t.Fatalf("%+v", e)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "run-*.tmp")); len(left) != 0 {
		t.Fatalf("left %v", left)
	}
}

func TestAReusedProcessIDIsNotTheRunGoingOn(t *testing.T) {
	// After a restart of Windows another wsl-shrinker, this one or another,
	// may get the id the journal names: it started after the run began, so
	// it is not that run, which was cut off.
	began := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name    string
		started time.Time
		known   bool
		want    State
	}{
		{"the process that began it", began.Add(-time.Minute), true, Going},
		{"a process given its id later", began.Add(time.Hour), true, CutOff},
		{"rounding at the second", began.Add(500 * time.Millisecond), true, Going},
		{"the system does not say", began.Add(time.Hour), false, Going},
	} {
		s := Store{Path: filepath.Join(t.TempDir(), "run.json"), Alive: func(int, string) bool { return true },
			Since: func(int) (time.Time, bool) { return c.started, c.known }}
		if err := s.Begin(Entry{PID: 100, Exe: "wsl-shrinker.exe", Started: began, Disk: "Dev", Step: "compact"}); err != nil {
			t.Fatal(err)
		}
		if _, st, err := s.Read(); err != nil || st != c.want {
			t.Errorf("%s: %v %v, want %v", c.name, st, err, c.want)
		}
	}
}
