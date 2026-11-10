package app

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kanggu12/wsl-shrinker/internal/engine/journal"
	"github.com/kanggu12/wsl-shrinker/internal/engine/shrink"
	"github.com/kanggu12/wsl-shrinker/internal/machine/winapi"
)

// The way out of a run that stays cut off: Recover says when it is the
// start that failed, and Forget closes the run as it is.

// stoppedDev is a run of Dev cut off after it stopped the distribution and
// two containers.
var stoppedDev = journal.Entry{PID: 1, Exe: "x", Disk: "Dev", Distro: "Dev", Step: "compact", Stopped: true,
	Containers: []string{"0123456789ab", "fedcba987654"}}

// started reports whether WSL was asked to start Dev, or anything in it.
func started(fake interface{ CallList() []string }) bool {
	return strings.Contains(strings.Join(fake.CallList(), " "), "start:Dev")
}

func TestAStartThatFailsLeavesTheRunCutOffAndSaysItIsTheStart(t *testing.T) {
	a, fake := testApp(t, func(int, string) bool { return false })
	fake.List[0].Running = false
	// The waits between the tries go through the App's Sleep, which is how
	// tests do without them.
	var waits []time.Duration
	a.Sleep = func(d time.Duration) { waits = append(waits, d) }
	if err := a.Journal.Begin(stoppedDev); err != nil {
		t.Fatal(err)
	}
	// Dev does not start: the reason comes back as StillCutOff, which the
	// fronts take as the moment to offer forgetting.
	fake.Fail = map[string]error{"start:Dev": errors.New("wsl --distribution Dev --exec true: exit status 1")}
	said, err := a.Recover(context.Background())
	var still StillCutOff
	if err == nil || !errors.As(err, &still) || said != "" {
		t.Fatalf("recover: %q, %v", said, err)
	}
	// The reason is the start's own, unwrapped for errors.Is and the like.
	if still.Err == nil || err.Error() != still.Err.Error() || errors.Unwrap(err) != still.Err {
		t.Fatalf("the reason %q wraps %v", err, still.Err)
	}
	// Tried more than once, with a wait before each try after the first.
	if len(waits) < 2 || waits[0] <= 0 {
		t.Fatalf("waits %v", waits)
	}
	// The journal stays, for another try: the run is as cut off as before.
	if e, st, _ := a.Journal.Read(); st != journal.CutOff || e.Disk != "Dev" {
		t.Fatalf("journal %v %+v, want it kept", st, e)
	}
	// And the try works once Dev starts.
	fake.Fail = nil
	if said, err = a.Recover(context.Background()); err != nil || !strings.HasPrefix(said, "Dev: running") {
		t.Fatalf("recover once Dev starts: %q, %v", said, err)
	}
	if _, st, _ := a.Journal.Read(); st != journal.None {
		t.Fatalf("journal %v after a recovery", st)
	}
}

func TestAHeldDiskLeavesTheRunCutOffAndARunGoingOnDoesNot(t *testing.T) {
	// The disk still held, by WSL compacting or by another program for as
	// long as it likes: the run stays cut off, and Forget is a way out of
	// it, as after a start that failed.
	a, fake := testApp(t, func(int, string) bool { return false })
	fake.List[0].Running = false
	a.StatFile = func(p string) (winapi.FileInfo, error) {
		fi, err := winapi.Stat(p)
		fi.Held = true
		return fi, err
	}
	if err := a.Journal.Begin(stoppedDev); err != nil {
		t.Fatal(err)
	}
	_, err := a.Recover(context.Background())
	var still StillCutOff
	if err == nil || !errors.As(err, &still) || !strings.Contains(err.Error(), "still compacting Dev, or another program holds its disk") {
		t.Fatalf("recover: %v", err)
	}
	if _, st, _ := a.Journal.Read(); st != journal.CutOff {
		t.Fatalf("journal %v", st)
	}
	// A run going on elsewhere is not cut off: no StillCutOff.
	b, _ := testApp(t, func(int, string) bool { return true })
	if err := b.Journal.Begin(stoppedDev); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Recover(context.Background()); err == nil || errors.As(err, &still) {
		t.Fatalf("recover of a run going on: %v", err)
	}
}

func TestForgetClosesACutOffRunAndStartsNothing(t *testing.T) {
	a, fake := testApp(t, func(int, string) bool { return false })
	fake.List[0].Running = false
	if err := a.Journal.Begin(stoppedDev); err != nil {
		t.Fatal(err)
	}
	// The run it closed comes back, for the fronts to say what stays
	// stopped, with the state it was found in.
	e, st, err := a.Forget(nil)
	if err != nil || st != journal.CutOff {
		t.Fatalf("forget: %v, state %v", err, st)
	}
	if e.Disk != "Dev" || !e.Stopped || strings.Join(e.Containers, " ") != "0123456789ab fedcba987654" {
		t.Fatalf("the run forgotten: %+v", e)
	}
	// Nothing was started, and nothing run inside: forgetting is not a
	// recovery by another name.
	if started(fake) || len(fake.CallList()) != 0 {
		t.Fatalf("forgetting asked WSL for %q", fake.CallList())
	}
	// The journal is gone, so nothing holds a later run back.
	if _, st, _ := a.Journal.Read(); st != journal.None {
		t.Fatalf("journal %v after forgetting", st)
	}
	fake.List[0].Running = true
	if res := a.Shrink(context.Background(), dev(t, a), shrink.Options{}, nil); res.Err != "" {
		t.Fatalf("a run after forgetting: %s", res.Err)
	}
	// Forgetting again finds nothing, and says so by the state alone.
	if e, st, err = a.Forget(nil); err != nil || st != journal.None || e.Disk != "" {
		t.Fatalf("a second forget: %+v, %v, %v", e, st, err)
	}
}

func TestForgetLeavesARunGoingOnElsewhere(t *testing.T) {
	// The process that wrote the journal still runs: its run is not this
	// program's to close, and the journal stays as it is.
	a, _ := testApp(t, func(int, string) bool { return true })
	if err := a.Journal.Begin(stoppedDev); err != nil {
		t.Fatal(err)
	}
	e, st, err := a.Forget(nil)
	if err == nil || st != journal.Going || err.Error() != "Dev is being shrunk by another wsl-shrinker; let it finish" {
		t.Fatalf("forget: %+v, %v, %v", e, st, err)
	}
	if got, st, _ := a.Journal.Read(); st != journal.Going || got.Disk != "Dev" {
		t.Fatalf("journal %v %+v, want it untouched", st, got)
	}
}

// unreadableJournal leaves a journal that does not parse and is old enough
// not to be a run just starting: a run cut off of which nothing is known.
func unreadableJournal(t *testing.T, a *App) {
	t.Helper()
	if err := os.WriteFile(a.Journal.Path, []byte("{\"pid\":12,\"disk\":\"De"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(a.Journal.Path, old, old); err != nil {
		t.Fatal(err)
	}
}

func TestAJournalThatCannotBeReadIsNotSaidToHaveStoppedNothing(t *testing.T) {
	a, fake := testApp(t, func(int, string) bool { return false })
	unreadableJournal(t, a)
	if e, st := a.Pending(); st != journal.CutOff || e.Disk != "" {
		t.Fatalf("pending: %v %+v", st, e)
	}
	// Recover cannot know what to start: it closes the run, and says what
	// it does not know rather than that nothing was stopped.
	said, err := a.Recover(context.Background())
	if err != nil || !strings.Contains(said, "cannot be read") || !strings.Contains(said, "not known") || !strings.Contains(said, "the run is closed") || strings.Contains(said, "nothing was stopped") {
		t.Fatalf("recover: %q, %v", said, err)
	}
	if started(fake) {
		t.Fatalf("recover started something on a guess: %q", fake.CallList())
	}
	if _, st, _ := a.Journal.Read(); st != journal.None {
		t.Fatalf("journal %v after recover", st)
	}
	// Forget closes one as well, and gives back the entry with nothing in
	// it, which the fronts read as not known.
	unreadableJournal(t, a)
	e, st, err := a.Forget(nil)
	if err != nil || st != journal.CutOff || e.Disk != "" || e.Distro != "" || e.Stopped {
		t.Fatalf("forget: %+v, %v, %v", e, st, err)
	}
	if _, st, _ := a.Journal.Read(); st != journal.None {
		t.Fatalf("journal %v after forgetting", st)
	}
	// A readable run that stopped nothing still says so.
	if err := a.Journal.Begin(journal.Entry{PID: 1, Exe: "x", Disk: "Dev", Distro: "Dev", Step: "check"}); err != nil {
		t.Fatal(err)
	}
	if said, err = a.Recover(context.Background()); err != nil || said != "nothing was stopped" {
		t.Fatalf("recover of a run that stopped nothing: %q, %v", said, err)
	}
}

func TestForgetWithNoRunClosesNothing(t *testing.T) {
	a, fake := testApp(t, func(int, string) bool { return false })
	if e, st, err := a.Forget(nil); err != nil || st != journal.None || e.Disk != "" {
		t.Fatalf("forget: %+v, %v, %v", e, st, err)
	}
	if len(fake.CallList()) != 0 {
		t.Fatalf("calls %q", fake.CallList())
	}
}

func TestForgetLeavesARunOtherThanTheOneAskedAbout(t *testing.T) {
	a, _ := testApp(t, func(int, string) bool { return false })
	asked := stoppedDev
	asked.Started = time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	// The run asked about ended meanwhile, and another was cut off since:
	// the same disk, another process, another moment.
	now := asked
	now.PID, now.Started = 2, asked.Started.Add(time.Minute)
	if err := a.Journal.Begin(now); err != nil {
		t.Fatal(err)
	}
	if _, st, err := a.Forget(&asked); !errors.Is(err, ErrRunChanged) || st != journal.CutOff {
		t.Fatalf("forget of another run: %v, %v", err, st)
	}
	if e, st, _ := a.Journal.Read(); st != journal.CutOff || e.PID != 2 {
		t.Fatalf("journal %v %+v, want the other run kept", st, e)
	}
	// The same process id, given again after a restart, at another moment:
	// another run all the same.
	again := now
	again.PID = asked.PID
	if err := a.Journal.End(); err != nil {
		t.Fatal(err)
	}
	if err := a.Journal.Begin(again); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Forget(&asked); !errors.Is(err, ErrRunChanged) {
		t.Fatalf("forget of a run with the same id: %v", err)
	}
	now = again
	// Asked about this one, it goes.
	if _, _, err := a.Forget(&now); err != nil {
		t.Fatal(err)
	}
	if _, st, _ := a.Journal.Read(); st != journal.None {
		t.Fatalf("journal %v", st)
	}
	// A journal that does not parse is the same run as its empty entry.
	unreadableJournal(t, a)
	if _, _, err := a.Forget(&journal.Entry{}); err != nil {
		t.Fatalf("forget of an unreadable journal: %v", err)
	}
	// Names: a journal that says its disk, and one that does not.
	if (journal.Entry{Disk: "Dev"}).Name() != "Dev" || (journal.Entry{}).Name() != "a disk" {
		t.Fatal("Name")
	}
}
