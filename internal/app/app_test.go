package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kanggu12/wsl-shrinker/internal/engine/disks"
	"github.com/kanggu12/wsl-shrinker/internal/engine/history"
	"github.com/kanggu12/wsl-shrinker/internal/engine/journal"
	"github.com/kanggu12/wsl-shrinker/internal/engine/shrink"
	"github.com/kanggu12/wsl-shrinker/internal/engine/survey"
	"github.com/kanggu12/wsl-shrinker/internal/machine/wsl"
	"github.com/kanggu12/wsl-shrinker/internal/settings/config"
	"github.com/kanggu12/wsl-shrinker/internal/testutil/vhdxtest"
)

// testApp is an App on a fake WSL with one running distribution, Dev,
// whose disk is a small VHDX file in the test's folder, and whose history
// and journal live there too. alive says which process ids still run.
func testApp(t *testing.T, alive func(int, string) bool) (*App, *wsl.Fake) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "ext4.vhdx")
	vhdxtest.Write(t, path, vhdxtest.Spec{VirtualSize: 16 << 20, Blocks: map[int64][]byte{0: {1}, 2: {1}}})
	fake := &wsl.Fake{List: []wsl.Distro{{Name: "Dev", VHD: path, Version: 2, Running: true}}}
	fake.Script = func(_, _, s string) (string, error) {
		switch {
		case strings.Contains(s, "docker ps -q"):
			// Two containers running, as the stop step names them.
			return "running|0123456789ab fedcba987654 \n", nil
		case strings.Contains(s, "/proc/mounts"):
			return "rw,relatime\n", nil
		case strings.Contains(s, "docker start"):
			return "started|0123456789ab\nstarted|fedcba987654\n", nil
		case strings.Contains(s, "docker info") && strings.Contains(s, "echo up"):
			// Docker answers at once, so the restart does not wait for it.
			return "up\n", nil
		}
		return "", nil
	}
	// A start that fails is tried again at once: the waits between tries
	// are the engine's to test.
	return &App{Config: config.Default(), WSL: fake, History: history.Open(dir), Journal: journal.Open(dir, alive),
		Sleep: func(time.Duration) {}}, fake
}

// dev finds the distribution's disk as the screen would.
func dev(t *testing.T, a *App) disks.Disk {
	t.Helper()
	ds, err := disks.Find(context.Background(), a.DisksEnv())
	if err != nil || len(ds) != 1 {
		t.Fatalf("%v %v", ds, err)
	}
	return ds[0]
}

func TestAStopCutOffStartsItsContainersAgain(t *testing.T) {
	// The program ends while the containers stop (a closed window, the
	// power): the journal names them already, so recover starts them.
	a, fake := testApp(t, func(int, string) bool { return false })
	inner := fake.Script
	var during journal.Entry
	var scripts []string
	fake.Script = func(x, y, s string) (string, error) {
		scripts = append(scripts, s)
		// The stop script runs: what would a run cut off now leave?
		if strings.Contains(s, "pre-stop.d") {
			during, _, _ = a.Journal.Read()
		}
		return inner(x, y, s)
	}
	a.Shrink(context.Background(), dev(t, a), shrink.Options{}, nil)
	if !during.Stopped || strings.Join(during.Containers, " ") != "0123456789ab fedcba987654" {
		t.Fatalf("while the containers stopped, the journal said %+v", during)
	}
	// Left behind as it was then, recover starts both again.
	if err := a.Journal.Begin(during); err != nil {
		t.Fatal(err)
	}
	scripts = nil
	said, err := a.Recover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	started := false
	for _, s := range scripts {
		started = started || strings.Contains(s, "docker start") && strings.Contains(s, "fedcba987654")
	}
	if !started {
		t.Fatalf("containers left stopped: %s; scripts %q", said, scripts)
	}
	// It still ran when recover started its containers: one line. Stopped,
	// as a run cut off in its compaction leaves it, recover starts it, and a
	// second line says how long the containers then run.
	if strings.Contains(said, "\n") {
		t.Fatalf("a distribution that kept running gets the note: %q", said)
	}
	fake.List[0].Running = false
	if err := a.Journal.Begin(during); err != nil {
		t.Fatal(err)
	}
	if said, err = a.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if first, note, _ := strings.Cut(said, "\n"); !strings.HasPrefix(first, "Dev: running") || !strings.HasPrefix(note, "note: with no terminal open in Dev, WSL stops it") {
		t.Fatalf("recover of a stopped distribution said %q", said)
	}
}

func TestJournalFollowsTheRunAndGoesWithIt(t *testing.T) {
	a, _ := testApp(t, func(int, string) bool { return false })
	// While the distribution is down for its compaction, the journal says
	// what a cut-off run would have to start again.
	var during journal.Entry
	var state journal.State
	res := a.Shrink(context.Background(), dev(t, a), shrink.Options{}, func(e shrink.Event) {
		if e.Step == shrink.StepCompact && e.Status == shrink.Running {
			during, _, _ = a.Journal.Read()
			_, state, _ = a.Journal.Read()
		}
	})
	if res.Err != "" {
		t.Fatalf("%s", res.Err)
	}
	if state != journal.CutOff || !during.Stopped || during.Step != "compact" || strings.Join(during.Containers, " ") != "0123456789ab fedcba987654" {
		// CutOff only because the test's process check says no one runs.
		t.Fatalf("during the run: %+v (%v)", during, state)
	}
	// Run returned, so it settled: nothing is left to recover, and the run
	// is in the history.
	if _, st, _ := a.Journal.Read(); st != journal.None {
		t.Fatalf("the journal stayed: %v", st)
	}
	if all, _ := a.History.All(); len(all) != 1 {
		t.Fatalf("history %d entries", len(all))
	}
}

func TestSecondRunWaitsForTheFirst(t *testing.T) {
	// Another process holds the journal: the run is refused at its check,
	// before anything changes, and that refusal is not history.
	a, fake := testApp(t, func(pid int, _ string) bool { return pid == 4242 })
	if err := a.Journal.Begin(journal.Entry{PID: 4242, Exe: "wsl-shrinker.exe", Disk: "Dev", Step: "compact"}); err != nil {
		t.Fatal(err)
	}
	res := a.Shrink(context.Background(), dev(t, a), shrink.Options{}, nil)
	if res.FailedAt != shrink.StepCheck || !strings.Contains(res.Err, "being shrunk by another") || res.Left != shrink.LeftRunning {
		t.Fatalf("%+v", res)
	}
	for _, c := range fake.CallList() {
		if !strings.HasPrefix(c, "run:") {
			t.Fatalf("changed something: %v", fake.CallList())
		}
	}
	if all, _ := a.History.All(); len(all) != 0 {
		t.Fatalf("a refused run went into the history: %+v", all)
	}
}

func TestHandoffWaitsForTheNewWindow(t *testing.T) {
	// The receipt goes next to the history; this test's own folder.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("LOCALAPPDATA", os.Getenv("XDG_CACHE_HOME"))
	defer func(open func([]string) error, wait time.Duration) { openWindow, handoffWait = open, wait }(openWindow, handoffWait)
	handoffWait = 2 * time.Second
	// The new window starts and leaves its receipt a moment later.
	var got []string
	openWindow = func(args []string) error {
		got = args
		go func() {
			time.Sleep(300 * time.Millisecond)
			_ = Received(args[len(args)-1])
		}()
		return nil
	}
	if err := Reopen([]string{"--reopened", "--run", "Dev"}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 || got[3] != "--handoff" {
		t.Fatalf("args %v", got)
	}
	// It never says a word: that is said here, after the wait.
	handoffWait = 300 * time.Millisecond
	openWindow = func([]string) error { return nil }
	if err := Reopen([]string{"--reopened"}); err == nil || !strings.Contains(err.Error(), "did not start") {
		t.Fatalf("got %v", err)
	}
	// A window that starts after the wait gave up leaves its receipt; the
	// next move takes it away, and leaves a fresh one of another alone.
	late, fresh := receiptPath("late"), receiptPath("fresh")
	if err := Received("late"); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(late, old, old)
	if err := Received("fresh"); err != nil {
		t.Fatal(err)
	}
	_ = Reopen([]string{"--reopened"})
	if _, err := os.Stat(late); !os.IsNotExist(err) {
		t.Fatalf("a stale receipt stayed: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("a fresh one went: %v", err)
	}
}

func TestHandoffKeepsEveryItemWhole(t *testing.T) {
	// One --clean per item, an id with a comma in it whole, disks in order.
	queue := []disks.Disk{{Name: "Ubuntu"}, {Name: "Debian"}}
	chosen := map[string][]survey.Item{"Ubuntu": {{ID: "apt"}, {ID: "pip:/home/a,b/.cache/pip"}}}
	want := "--reopened --run Ubuntu --clean Ubuntu=apt --clean Ubuntu=pip:/home/a,b/.cache/pip --run Debian"
	if got := strings.Join(HandoffArgs("", queue, chosen), " "); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestRecoverAfterACompactionReadsTheDiskFirst(t *testing.T) {
	// Cut off once the compaction began: a disk that does not read cleanly
	// stays stopped, as a run would leave it, and the journal goes.
	a, fake := testApp(t, func(int, string) bool { return false })
	fake.List[0].Running = false
	if err := os.Truncate(fake.List[0].VHD, 512); err != nil {
		t.Fatal(err)
	}
	if err := a.Journal.Begin(journal.Entry{PID: 99, Disk: "Dev", Distro: "Dev", Step: "verify", Stopped: true}); err != nil {
		t.Fatal(err)
	}
	_, err := a.Recover(context.Background())
	if err == nil || !strings.Contains(err.Error(), "does not read cleanly; it stays stopped") {
		t.Fatalf("got %v", err)
	}
	for _, c := range fake.CallList() {
		if strings.HasPrefix(c, "start:") {
			t.Fatalf("started it: %v", fake.CallList())
		}
	}
	if _, st, _ := a.Journal.Read(); st != journal.None {
		t.Fatalf("journal %v", st)
	}
}

func TestAWSLThatDidNotAnswerIsAskedAgainAndNotTakenForAnOldOne(t *testing.T) {
	a, fake := testApp(t, nil)
	ctx := context.Background()
	fake.InfoErr = fmt.Errorf("%w: wsl --version ran for 1m0s", wsl.ErrNoAnswer)
	if in := a.Info(ctx); !in.NoAnswer || in.CanCompact {
		t.Fatalf("%+v", in)
	}
	// What it can do is not known: a run is not refused for an old WSL,
	// and its own calls to WSL say what is wrong.
	if !a.shrinkEnv(ctx).CanCompact {
		t.Fatal("a WSL that did not answer was taken for one that cannot compact")
	}
	// Answered the next time, the answer is the one kept.
	fake.InfoErr = nil
	if in := a.Info(ctx); in.NoAnswer || !in.CanCompact || in.Version == "" {
		t.Fatalf("%+v", in)
	}
	fake.InfoErr = fmt.Errorf("%w: later", wsl.ErrNoAnswer)
	if in := a.Info(ctx); in.NoAnswer || !in.CanCompact {
		t.Fatalf("the answer was not kept: %+v", in)
	}
}
