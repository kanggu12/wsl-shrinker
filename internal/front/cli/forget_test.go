package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kanggu12/wsl-shrinker/internal/app"
	"github.com/kanggu12/wsl-shrinker/internal/app/remote"
	"github.com/kanggu12/wsl-shrinker/internal/engine/history"
	"github.com/kanggu12/wsl-shrinker/internal/engine/journal"
	"github.com/kanggu12/wsl-shrinker/internal/version"
)

// The ways out and the clearing on the command line: recover --forget,
// recover's hint when a start fails, history --clear, and the backend
// commands the Linux-side screen asks for the same.

// answering makes the test's command line look started in a terminal,
// with text typed in answer to its question. Everything is put back after
// the test.
func answering(t *testing.T, text string) {
	t.Helper()
	// A file stands in for the keyboard: the question reads its one line.
	f, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	in, std := stdinIsTerminal, os.Stdin
	stdinIsTerminal, os.Stdin = func() bool { return true }, f
	t.Cleanup(func() {
		stdinIsTerminal, os.Stdin = in, std
		f.Close()
	})
}

// noTerminal makes the test's command line look started with no person to
// answer: from a script, a pipe, a scheduled task.
func noTerminal(t *testing.T) {
	t.Helper()
	in, relay := stdinIsTerminal, relayed
	stdinIsTerminal, relayed = func() bool { return false }, func(int) bool { return false }
	t.Cleanup(func() { stdinIsTerminal, relayed = in, relay })
}

// cutOffDev leaves a run of Dev on a cut off after it stopped Dev and two
// containers, its process gone.
func cutOffDev(t *testing.T, a *app.App) {
	t.Helper()
	if err := a.Journal.Begin(journal.Entry{PID: 999999999, Exe: "wsl-shrinker.exe", Disk: "Dev", Distro: "Dev", Step: "compact",
		Stopped: true, Containers: []string{"0123456789ab", "ba9876543210"}}); err != nil {
		t.Fatal(err)
	}
}

// state is what the journal of a says now.
func state(t *testing.T, a *app.App) journal.State {
	t.Helper()
	_, st, err := a.Journal.Read()
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestRecoverSaysBothWaysOnWhenTheStartFails(t *testing.T) {
	a, fake := flowApp(t)
	fake.List[0].Running = false
	cutOffDev(t, a)
	fake.Fail = map[string]error{"start:Dev": errors.New("the root filesystem stays read-only")}
	code, out, errOut := shrinkWith(a, "recover")
	// The reason, then what to do: try again, or close the run as it is.
	if code != exitFail || out != "" || !strings.Contains(errOut, "the root filesystem stays read-only") ||
		!strings.Contains(errOut, "wsl-shrinker recover tries again; wsl-shrinker recover --forget closes the run as it is") {
		t.Fatalf("exit %d: %q %q", code, out, errOut)
	}
	// The run stays cut off, for either.
	if st := state(t, a); st != journal.CutOff {
		t.Fatalf("journal %v", st)
	}
	// A failure that is not the start's says no such thing: there is
	// nothing to forget while WSL is at the disk.
	fake.Fail = nil
	fake.List[0].Running = true
	if err := a.Journal.End(); err != nil {
		t.Fatal(err)
	}
	b, _ := flowApp(t)
	b.Journal = journal.Open(filepath.Dir(b.Journal.Path), func(int, string) bool { return true })
	cutOffDev(t, b)
	if code, _, errOut := shrinkWith(b, "recover"); code == exitOK || strings.Contains(errOut, "--forget") {
		t.Fatalf("a run going on: exit %d, %q", code, errOut)
	}
}

func TestRecoverForgetAsksWhatStaysStoppedFirst(t *testing.T) {
	a, fake := flowApp(t)
	cutOffDev(t, a)
	// No: nothing is closed, and the command says so in its exit code.
	answering(t, "n\n")
	code, out, errOut := shrinkWith(a, "recover", "--forget")
	for _, want := range []string{
		"the run of Dev was cut off at compact\n",
		"it had stopped Dev and 2 containers; forgetting it starts nothing again\n",
		"Forget it? [y/N] ",
		"not forgotten",
	} {
		if !strings.Contains(errOut, want) {
			t.Errorf("no %q in:\n%s", want, errOut)
		}
	}
	if code != exitFail || out != "" || state(t, a) != journal.CutOff {
		t.Fatalf("no: exit %d, %q, journal %v", code, out, state(t, a))
	}
	// Yes: closed, nothing started, and the way to start it by hand said.
	answering(t, "y\n")
	code, out, errOut = shrinkWith(a, "recover", "--forget")
	if code != exitOK || state(t, a) != journal.None {
		t.Fatalf("yes: exit %d, %q, journal %v", code, errOut, state(t, a))
	}
	want := "forgot the run of Dev that was cut off at compact; nothing was started again\n" +
		"note: to start by hand what it had stopped: wsl -d Dev, then docker start 0123456789ab ba9876543210 inside it\n"
	if out != want {
		t.Fatalf("said:\n%s\nwant:\n%s", out, want)
	}
	if len(fake.CallList()) != 0 {
		t.Fatalf("forgetting asked WSL for %q", fake.CallList())
	}
}

func TestRecoverForgetWithoutATerminalWantsYes(t *testing.T) {
	a, fake := flowApp(t)
	cutOffDev(t, a)
	noTerminal(t)
	// No one to ask: refused before anything is read, with the flag that
	// does without the question.
	code, _, errOut := shrinkWith(a, "recover", "--forget")
	if code != exitUsage || !strings.Contains(errOut, "no terminal to ask in") || !strings.Contains(errOut, "add --yes to forget it without the question") {
		t.Fatalf("exit %d: %q", code, errOut)
	}
	if state(t, a) != journal.CutOff {
		t.Fatal("the run was closed without --yes")
	}
	// --yes, or -y: closed without a question. The run the refusal left
	// is there for the first; the second gets one of its own.
	for _, yes := range []string{"--yes", "-y"} {
		if state(t, a) == journal.None {
			cutOffDev(t, a)
		}
		code, out, errOut := shrinkWith(a, "recover", "--forget", yes)
		if code != exitOK || strings.Contains(errOut, "Forget it?") || !strings.HasPrefix(out, "forgot the run of Dev") || state(t, a) != journal.None {
			t.Fatalf("%s: exit %d, %q %q", yes, code, out, errOut)
		}
	}
	if len(fake.CallList()) != 0 {
		t.Fatalf("calls %q", fake.CallList())
	}
	// --yes alone, without --forget, still finishes the run as before.
	cutOffDev(t, a)
	if code, out, errOut := shrinkWith(a, "recover", "--yes"); code != exitOK || !strings.HasPrefix(out, "Dev: ") || state(t, a) != journal.None {
		t.Fatalf("recover --yes: exit %d, %q %q", code, out, errOut)
	}
}

func TestRecoverForgetWithNothingToForget(t *testing.T) {
	a, _ := flowApp(t)
	noTerminal(t)
	// No run cut off: nothing to forget is not a mistake, for a script
	// that asks to be sure.
	if code, out, errOut := shrinkWith(a, "recover", "--forget", "--yes"); code != exitOK || out != "no run was cut off\n" {
		t.Fatalf("exit %d: %q %q", code, out, errOut)
	}
	// A run going on in another process is not this one's to close.
	a.Journal = journal.Open(filepath.Dir(a.Journal.Path), func(int, string) bool { return true })
	cutOffDev(t, a)
	code, _, errOut := shrinkWith(a, "recover", "--forget", "--yes")
	if code != exitFail || !strings.Contains(errOut, "Dev is being shrunk by another wsl-shrinker (let it finish)") {
		t.Fatalf("a run going on: exit %d, %q", code, errOut)
	}
	if state(t, a) != journal.Going {
		t.Fatal("a run going on was closed")
	}
}

func TestRecoverForgetOfAJournalThatCannotBeRead(t *testing.T) {
	a, _ := flowApp(t)
	// A journal that does not parse, old enough not to be a run starting.
	if err := os.WriteFile(a.Journal.Path, []byte(`{"pid":12,"disk":"De`), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(a.Journal.Path, old, old); err != nil {
		t.Fatal(err)
	}
	answering(t, "y\n")
	code, out, errOut := shrinkWith(a, "recover", "--forget")
	// What it had stopped is not known, and is not guessed: no "nothing",
	// and no command to type.
	if !strings.Contains(errOut, "a run was cut off\n") || !strings.Contains(errOut, "its journal cannot be read: what it had stopped is not known\n") || strings.Contains(errOut, "stopped nothing") {
		t.Errorf("the question:\n%s", errOut)
	}
	if code != exitOK || out != "forgot the run that was cut off\n" || state(t, a) != journal.None {
		t.Fatalf("exit %d: %q", code, out)
	}
}

func TestHistoryClearAsksHowManyFirst(t *testing.T) {
	a, _ := flowApp(t)
	for _, d := range []string{"Dev", "Other"} {
		if err := a.History.Add(history.Entry{At: time.Now(), Disk: d}); err != nil {
			t.Fatal(err)
		}
	}
	// No: the runs stay.
	answering(t, "n\n")
	code, _, errOut := shrinkWith(a, "history", "--clear")
	if code != exitFail || !strings.Contains(errOut, "Clear the history of 2 runs? [y/N] ") || !strings.Contains(errOut, "not cleared") {
		t.Fatalf("no: exit %d, %q", code, errOut)
	}
	if all, _ := a.History.All(); len(all) != 2 {
		t.Fatalf("no: %d runs left", len(all))
	}
	// Yes: they go, and what went is said.
	answering(t, "y\n")
	if code, out, errOut := shrinkWith(a, "history", "--clear"); code != exitOK || out != "cleared the history of 2 runs\n" {
		t.Fatalf("yes: exit %d, %q %q", code, out, errOut)
	}
	if all, _ := a.History.All(); len(all) != 0 {
		t.Fatalf("yes: %d runs left", len(all))
	}
	// Empty already: nothing to ask about.
	if code, out, errOut := shrinkWith(a, "history", "--clear"); code != exitOK || out != "the history is empty already\n" || strings.Contains(errOut, "[y/N]") {
		t.Fatalf("empty: exit %d, %q %q", code, out, errOut)
	}
}

func TestHistoryClearWithoutATerminalWantsYes(t *testing.T) {
	a, _ := flowApp(t)
	if err := a.History.Add(history.Entry{At: time.Now(), Disk: "Dev"}); err != nil {
		t.Fatal(err)
	}
	noTerminal(t)
	code, _, errOut := shrinkWith(a, "history", "--clear")
	if code != exitUsage || !strings.Contains(errOut, "no terminal to ask in") || !strings.Contains(errOut, "add --yes to clear it without the question") {
		t.Fatalf("exit %d: %q", code, errOut)
	}
	// --clear prints nothing a script would read: --json with it is a
	// mistake to say, before anything goes.
	if code, _, errOut := shrinkWith(a, "history", "--clear", "--yes", "--json"); code != exitUsage || !strings.Contains(errOut, "--clear prints no JSON; leave --json out") {
		t.Fatalf("--json: exit %d, %q", code, errOut)
	}
	if all, _ := a.History.All(); len(all) != 1 {
		t.Fatalf("%d runs left after the refusals", len(all))
	}
	// A run going on writes its line as it ends: the history is not cleared
	// under it.
	b, _ := flowApp(t)
	b.History = a.History
	b.Journal = journal.Open(filepath.Dir(b.Journal.Path), func(int, string) bool { return true })
	cutOffDev(t, b)
	if code, _, errOut := shrinkWith(b, "history", "--clear", "--yes"); code != exitFail || !strings.Contains(errOut, "being shrunk by another wsl-shrinker") {
		t.Fatalf("a run going on: exit %d, %q", code, errOut)
	}
	if all, _ := a.History.All(); len(all) != 1 {
		t.Fatalf("%d runs left with a run going on", len(all))
	}
	// --yes: cleared without a question, and the settings and the journal
	// beside the history stay.
	cutOffDev(t, a)
	if code, out, errOut := shrinkWith(a, "history", "--clear", "--yes"); code != exitOK || out != "cleared the history of 1 run\n" {
		t.Fatalf("--yes: exit %d, %q %q", code, out, errOut)
	}
	if state(t, a) != journal.CutOff {
		t.Fatal("clearing the history closed a run that was cut off")
	}
}

func TestTheBackendSaysAStartThatFailedApart(t *testing.T) {
	a, fake := flowApp(t)
	fake.List[0].Running = false
	cutOffDev(t, a)
	fake.Fail = map[string]error{"start:Dev": errors.New("the root filesystem stays read-only")}
	// The failure is in the answer, and the answer is given: exit 0.
	code, out, errOut := shrinkWith(a, "backend", "recover")
	var got struct {
		Schema      int    `json:"schema"`
		Did         string `json:"did"`
		Error       string `json:"error"`
		StillCutOff bool   `json:"still_cut_off"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || code != exitOK {
		t.Fatalf("exit %d: %v\n%s%s", code, err, out, errOut)
	}
	if got.Schema != 1 || got.Did != "" || got.Error != "the root filesystem stays read-only" || !got.StillCutOff {
		t.Fatalf("answer %+v", got)
	}
	// Forgetting from the screen: nothing printed, the journal gone.
	if code, out, errOut := shrinkWith(a, "backend", "forget"); code != exitOK || out != "" || errOut != "" {
		t.Fatalf("forget: exit %d, %q %q", code, out, errOut)
	}
	if state(t, a) != journal.None {
		t.Fatal("the run stayed")
	}
	// A recovery that works says what it did, with no failure in it.
	fake.Fail = nil
	cutOffDev(t, a)
	got.Did, got.Error, got.StillCutOff = "", "", false
	_, out, _ = shrinkWith(a, "backend", "recover")
	if err := json.Unmarshal([]byte(out), &got); err != nil || !strings.HasPrefix(got.Did, "Dev: running") || got.Error != "" || got.StillCutOff {
		t.Fatalf("a recovery: %v %+v", err, got)
	}
	// Neither a failure of another kind, nor a run going on, is a start
	// that failed.
	b, _ := flowApp(t)
	b.Journal = journal.Open(filepath.Dir(b.Journal.Path), func(int, string) bool { return true })
	cutOffDev(t, b)
	got.Did, got.Error, got.StillCutOff = "", "", false
	_, out, _ = shrinkWith(b, "backend", "recover")
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.Error == "" || got.StillCutOff || strings.Contains(out, "still_cut_off") {
		t.Fatalf("a run going on: %v %+v\n%s", err, got, out)
	}
	// And forgetting a run going on fails as the command does.
	if code, _, errOut := shrinkWith(b, "backend", "forget"); code != exitFail || !strings.Contains(errOut, "let it finish") {
		t.Fatalf("forget of a run going on: exit %d, %q", code, errOut)
	}
}

func TestTheLinuxScreenForgetsThroughTheProgram(t *testing.T) {
	// Both ends in this test, as in TestTheLinuxScreenTalksToTheProgram:
	// the start fails on the Windows side, the screen learns it is the
	// start, and forgetting closes the run there.
	a, fake := flowApp(t)
	fake.List[0].Running = false
	cutOffDev(t, a)
	fake.Fail = map[string]error{"start:Dev": errors.New("the root filesystem stays read-only")}
	run := func(ctx context.Context, args []string, out io.Writer) error {
		var e bytes.Buffer
		if code := runWith(args, out, &e, func() (*app.App, error) { return a, nil }); code != exitOK {
			return errors.New(strings.TrimSpace(strings.TrimPrefix(e.String(), "wsl-shrinker:")))
		}
		return nil
	}
	ctx := context.Background()
	b, err := remote.Open(ctx, run, "Other", version.String())
	if err != nil {
		t.Fatal(err)
	}
	if e, st := b.Pending(); st != journal.CutOff || e.Disk != "Dev" || len(e.Containers) != 2 {
		t.Fatalf("pending %+v %v", e, st)
	}
	_, err = b.Recover(ctx)
	var still app.StillCutOff
	if !errors.As(err, &still) || err.Error() != "the root filesystem stays read-only" {
		t.Fatalf("recover: %v", err)
	}
	// The recovery tried the start; the forgetting asks WSL for nothing.
	tried := len(fake.CallList())
	shownRun, _ := b.Pending()
	if err := b.Forget(ctx, shownRun); err != nil {
		t.Fatal(err)
	}
	if _, st := b.Pending(); st != journal.None {
		t.Fatalf("pending %v after forgetting", st)
	}
	if calls := fake.CallList(); len(calls) != tried {
		t.Fatalf("forgetting asked WSL for %q", calls[tried:])
	}
}

func TestTheBackendForgetsOnlyTheRunAskedAbout(t *testing.T) {
	a, _ := flowApp(t)
	cutOffDev(t, a)
	e, _, _ := a.Journal.Read()
	other := []string{"backend", "forget", "--pid=1", "--started=" + e.Started.Format(time.RFC3339Nano), "--disk=Dev"}
	// Another run than the journal's: refused, the run kept.
	if code, _, errOut := shrinkWith(a, other...); code != exitFail || !strings.Contains(errOut, "not the one shown any more") {
		t.Fatalf("exit %d: %q", code, errOut)
	}
	if state(t, a) != journal.CutOff {
		t.Fatal("the run went")
	}
	// A time that is no time is a mistake in the command line.
	if code, _, _ := shrinkWith(a, "backend", "forget", "--pid=1", "--started=yesterday"); code != exitUsage {
		t.Fatalf("exit %d", code)
	}
	// The journal's own run: forgotten.
	this := []string{"backend", "forget", "--pid=" + strconv.Itoa(e.PID), "--started=" + e.Started.Format(time.RFC3339Nano), "--disk=" + e.Disk}
	if code, _, errOut := shrinkWith(a, this...); code != exitOK || state(t, a) != journal.None {
		t.Fatalf("exit %d: %q", code, errOut)
	}
}

func TestARunStartingElsewhereIsSaidWithoutAnEmptyName(t *testing.T) {
	// A journal that does not parse yet is a run starting: its disk is
	// not known, and the words say a disk rather than a blank.
	a, _ := flowApp(t)
	if err := os.WriteFile(a.Journal.Path, []byte(`{"pid":12,"disk":"De`), 0o644); err != nil {
		t.Fatal(err)
	}
	noTerminal(t)
	for _, args := range [][]string{{"recover", "--forget", "--yes"}, {"history", "--clear", "--yes"}, {"recover"}} {
		if code, _, errOut := shrinkWith(a, args...); code != exitFail || !strings.Contains(errOut, "a disk is being shrunk by another wsl-shrinker") {
			t.Errorf("%v: exit %d, %q", args, code, errOut)
		}
	}
}

// syncBuf is a buffer two goroutines may write and read at once.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestRecoverForgetLeavesARunCutOffWhileItAsked(t *testing.T) {
	a, _ := flowApp(t)
	cutOffDev(t, a)
	// A keyboard that answers once the test says.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	in, std := stdinIsTerminal, os.Stdin
	stdinIsTerminal, os.Stdin = func() bool { return true }, r
	t.Cleanup(func() {
		stdinIsTerminal, os.Stdin = in, std
		r.Close()
		w.Close()
	})
	var o, e syncBuf
	done := make(chan int)
	go func() {
		done <- runWith([]string{"recover", "--forget"}, &o, &e, func() (*app.App, error) { return a, nil })
	}()
	for i := 0; i < 500 && !strings.Contains(e.String(), "Forget it?"); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	// While it asked, that run was finished elsewhere, and another was cut
	// off: the answer is not about that one.
	if err := a.Journal.End(); err != nil {
		t.Fatal(err)
	}
	other := journal.Entry{PID: 999999998, Exe: "wsl-shrinker.exe", Started: time.Now(), Disk: "Debian", Distro: "Debian", Step: "stop", Stopped: true}
	if err := a.Journal.Begin(other); err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString("y\n"); err != nil {
		t.Fatal(err)
	}
	code := <-done
	if code != exitFail || !strings.Contains(e.String(), "not the one shown any more") || !strings.Contains(e.String(), "wsl-shrinker recover --forget asks about the run cut off now") {
		t.Fatalf("exit %d: %q", code, e.String())
	}
	if got, st, _ := a.Journal.Read(); st != journal.CutOff || got.Disk != "Debian" {
		t.Fatalf("journal %v %+v, want the other run kept", st, got)
	}
}
