package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"
)

// notAlive is no process at all: every earlier run has ended.
func notAlive(int) bool { return false }

// crashes lists dir's crash files of one kind: not yet said, or said.
func crashes(t *testing.T, dir string, shown bool) []string {
	t.Helper()
	all, _ := filepath.Glob(filepath.Join(dir, "crash-*.txt"))
	var out []string
	for _, p := range all {
		if strings.HasSuffix(p, "-shown.txt") == shown {
			out = append(out, p)
		}
	}
	return out
}

// noRuntime keeps the Go runtime's crash output as it is, for a test that
// is not about it.
func noRuntime(t *testing.T) {
	t.Helper()
	saved := crashOutput
	t.Cleanup(func() { crashOutput = saved })
	crashOutput = func(*os.File, debug.CrashOptions) error { return nil }
}

func TestACrashNoRecoverCatchesIsKeptForTheNextStart(t *testing.T) {
	// The child: it watches, then dies of a panic in a goroutine of its
	// own, which no recover covers.
	if dir := os.Getenv("WSS_CRASH_CHILD"); dir != "" {
		watchCrashes(dir, os.Getpid(), notAlive, false, look{}, io.Discard)
		go func() { panic("a bug no recover catches") }()
		time.Sleep(30 * time.Second)
		return
	}
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := exec.Command(exe, "-test.run=^TestACrashNoRecoverCatchesIsKeptForTheNextStart$")
	c.Env = append(os.Environ(), "WSS_CRASH_CHILD="+dir)
	out, err := c.CombinedOutput()
	// It died as Go ends a program on a panic, saying so on its stderr.
	if err == nil || !strings.Contains(string(out), "a bug no recover catches") {
		t.Fatalf("%v\n%s", err, out)
	}
	// Its file holds the same words.
	kept := crashes(t, dir, false)
	if len(kept) != 1 {
		t.Fatalf("files %v", kept)
	}
	if b, _ := os.ReadFile(kept[0]); !strings.Contains(string(b), "panic: a bug no recover catches") {
		t.Fatalf("%q", b)
	}
	// The next start, its stderr piped: nothing said, nothing moved.
	end := watchCrashes(dir, os.Getpid(), notAlive, false, look{}, io.Discard)
	end()
	if _, err := os.Stat(kept[0]); err != nil {
		t.Fatalf("moved for a pipe: %v", err)
	}
	// In a terminal: it says where the crash is, once, renamed as said.
	var said bytes.Buffer
	end = watchCrashes(dir, os.Getpid(), notAlive, true, look{on: true}, &said)
	end()
	shown := strings.TrimSuffix(kept[0], ".txt") + "-shown.txt"
	if !strings.Contains(said.String(), "an earlier run stopped on a bug; what it printed is in "+shown) || !strings.Contains(said.String(), IssuesURL) {
		t.Fatalf("%q", said.String())
	}
	if b, _ := os.ReadFile(shown); !strings.Contains(string(b), "a bug no recover catches") {
		t.Fatalf("%q", b)
	}
	// Nothing else is left: the crash said, and this run's own gone at its end.
	if left := crashes(t, dir, false); len(left) != 0 {
		t.Fatalf("left %v", left)
	}
	said.Reset()
	end = watchCrashes(dir, os.Getpid(), notAlive, true, look{on: true}, &said)
	end()
	if said.Len() != 0 {
		t.Fatalf("said twice: %q", said.String())
	}
}

func TestALaterRunWithTheSameIDKeepsTheCrash(t *testing.T) {
	// Windows gives a dead run's process id to a later one: that run's own
	// file has another name, and the earlier crash is said, not emptied.
	noRuntime(t)
	dir := t.TempDir()
	old := filepath.Join(dir, "crash-7-111.txt")
	if err := os.WriteFile(old, []byte("panic: X\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Process 7 runs now: this run, the same program.
	isSeven := func(pid int) bool { return pid == 7 }
	var said bytes.Buffer
	end := watchCrashes(dir, 7, isSeven, true, look{}, &said)
	if b, _ := os.ReadFile(old); string(b) != "panic: X\n" {
		t.Fatalf("the earlier crash was emptied: %q", b)
	}
	end()
	if !strings.Contains(said.String(), "crash-7-111-shown.txt") {
		t.Fatalf("%q", said.String())
	}
}

func TestTwoRunsSayACrashOnce(t *testing.T) {
	// Two terminals start while a crash waits: the first to end says it,
	// the other says nothing, and what was kept stays whole.
	noRuntime(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "crash-50-1.txt"), []byte("panic: X\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var a, b bytes.Buffer
	endA := watchCrashes(dir, 60, notAlive, true, look{}, &a)
	endB := watchCrashes(dir, 61, notAlive, true, look{}, &b)
	endA()
	endB()
	if !strings.Contains(a.String(), "crash-50-1-shown.txt") || b.Len() != 0 {
		t.Fatalf("A %q\nB %q", a.String(), b.String())
	}
	if kept, _ := os.ReadFile(filepath.Join(dir, "crash-50-1-shown.txt")); string(kept) != "panic: X\n" {
		t.Fatalf("%q", kept)
	}
}

func TestACrashIsSaidWithoutColorsToo(t *testing.T) {
	// NO_COLOR, or a console without colors: the same two lines, plain.
	var said bytes.Buffer
	sayCrashes(&said, look{}, []string{"a-shown.txt", "b-shown.txt"})
	want := "! an earlier run stopped on a bug; what it printed is in b-shown.txt, and 1 more beside it\n" +
		"  → please report it with that file: " + IssuesURL + "\n"
	if said.String() != want {
		t.Fatalf("got  %q\nwant %q", said.String(), want)
	}
}

func TestAFolderLeftEmptyGoesAtTheEnd(t *testing.T) {
	noRuntime(t)
	// Inside WSL the folder holds crash files alone: a run that ends
	// cleanly leaves nothing, not even the folder.
	dir := filepath.Join(t.TempDir(), "wsl-shrinker")
	end := watchCrashes(dir, os.Getpid(), notAlive, false, look{}, io.Discard)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("no folder while the run goes: %v", err)
	}
	end()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("the folder stayed: %v", err)
	}
	// With the history in it, it stays, and so does the history.
	history := filepath.Join(dir, "history.jsonl")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(history, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	watchCrashes(dir, os.Getpid(), notAlive, false, look{}, io.Discard)()
	if _, err := os.Stat(history); err != nil {
		t.Fatalf("the history went: %v", err)
	}
}

func TestCrashFilesAreKeptWithinBounds(t *testing.T) {
	// An empty file of a run that ended goes; one of a run still going
	// stays; past keptCrashes, the oldest of each kind go.
	dir := t.TempDir()
	saved := crashOutput
	t.Cleanup(func() { crashOutput = saved })
	set := 0
	crashOutput = func(f *os.File, _ debug.CrashOptions) error {
		if f != nil {
			set++
		}
		return nil
	}
	write := func(name, body string, age time.Duration) {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-age)
		_ = os.Chtimes(p, at, at)
	}
	write("crash-100-1.txt", "", time.Hour)
	write("crash-200-1.txt", "", time.Hour)
	for i := range keptCrashes + 3 {
		write(fmt.Sprintf("crash-%d-1.txt", 1000+i), fmt.Sprintf("panic: %d\n", i), time.Duration(keptCrashes+3-i)*time.Minute)
		write(fmt.Sprintf("crash-%d-1-shown.txt", 2000+i), "panic\n", time.Duration(keptCrashes+3-i)*time.Hour)
	}
	alive := func(pid int) bool { return pid == 200 }
	var said bytes.Buffer
	end := watchCrashes(dir, 1, alive, true, look{}, &said)
	// The runtime was given this run's file; the empty one of an ended run
	// is gone, the running one's stays; the said ones are the newest ten.
	if set != 1 {
		t.Fatalf("set %d times", set)
	}
	if _, err := os.Stat(filepath.Join(dir, "crash-100-1.txt")); !os.IsNotExist(err) {
		t.Fatalf("an ended run's empty file stayed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "crash-200-1.txt")); err != nil {
		t.Fatalf("a running one's went: %v", err)
	}
	if n := len(crashes(t, dir, true)); n != keptCrashes {
		t.Fatalf("%d said crashes kept", n)
	}
	end()
	// The newest keptCrashes crashes are said, the newest named; the three
	// oldest went unsaid.
	newest := filepath.Join(dir, fmt.Sprintf("crash-%d-1-shown.txt", 1000+keptCrashes+2))
	if !strings.Contains(said.String(), newest+", and 9 more beside it") {
		t.Fatalf("%q", said.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "crash-1002-1-shown.txt")); !os.IsNotExist(err) {
		t.Fatalf("an old crash past the bound was kept: %v", err)
	}
	// Left: the running one's file, and the said ones, which the next start
	// brings back within bounds.
	if left := crashes(t, dir, false); len(left) != 1 {
		t.Fatalf("left %v", left)
	}
	watchCrashes(dir, 1, alive, false, look{}, io.Discard)()
	if n := len(crashes(t, dir, true)); n != keptCrashes {
		t.Fatalf("%d said crashes kept", n)
	}
}
