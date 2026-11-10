//go:build windows

package winapi

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMain lets the test binary play the reopened program: started with
// REOPEN_CHILD set, it records its arguments and exits.
func TestMain(m *testing.M) {
	if out := os.Getenv("REOPEN_CHILD"); out != "" {
		_ = os.WriteFile(out, []byte(strings.Join(os.Args[1:], "\n")), 0o644)
		os.Exit(0)
	}
	// Started with WAIT_CHILD set, it is another copy of the program,
	// running until its stdin closes.
	if os.Getenv("WAIT_CHILD") != "" {
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestReopenArgumentsArriveWhole(t *testing.T) {
	// `start /b` runs in this console: the same command line as Reopen's,
	// without opening a window on the desktop.
	out := filepath.Join(t.TempDir(), "args.txt")
	t.Setenv("REOPEN_CHILD", out)
	args := []string{"--reopened", "--run", "Ubuntu-24.04", "--clean", "Ubuntu-24.04=pip:/home/ann/.cache/pip,docker-build-cache"}
	line, err := reopenLine(args, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := runCmdLine(line); err != nil {
		t.Fatal(err)
	}
	// start returns before the program finishes.
	var got []byte
	for i := 0; i < 50 && len(got) == 0; i++ {
		time.Sleep(100 * time.Millisecond)
		got, _ = os.ReadFile(out)
	}
	if string(got) != strings.Join(args, "\n") {
		t.Fatalf("arrived as %q\nline: %s", got, line)
	}
}

func TestReopenRefusesShellSyntax(t *testing.T) {
	for _, a := range []string{"a&b", "x|y", `q"`, "%PATH%", "a>b"} {
		if _, err := reopenLine([]string{a}, true); err == nil {
			t.Errorf("%q accepted", a)
		}
	}
}

func TestRunningKnowsThisProgramByName(t *testing.T) {
	// The journal's check: this process runs, under its own name; the same id
	// under another name is another program; an id no process has is none.
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Base(self)
	if !Running(os.Getpid(), name) || !Running(os.Getpid(), strings.ToUpper(name)) {
		t.Fatalf("this process (%s) does not read as running", name)
	}
	if Running(os.Getpid(), "another.exe") {
		t.Fatal("another name read as this program")
	}
	if Running(0x7ffffff0, name) {
		t.Fatal("an id no process has read as running")
	}
}

func TestReopenOpensNoWindowFromATest(t *testing.T) {
	// Whatever a test does, Reopen does not reach the screen from it: a test
	// binary run from WSL looks started from WSL, and one did open windows.
	if err := Reopen([]string{"--reopened"}); !errors.Is(err, errNoWindowInTests) {
		t.Fatalf("got %v", err)
	}
}

func TestProcessStartIsWhenThisTestStarted(t *testing.T) {
	// The journal's check for an id given again: this process started a
	// moment ago, and an id no process has says nothing.
	got, ok := ProcessStart(os.Getpid())
	if !ok || got.After(time.Now()) || time.Since(got) > time.Hour {
		t.Fatalf("%v %v", got, ok)
	}
	if _, ok := ProcessStart(0x7ffffff0); ok {
		t.Fatal("an id no process has said when it started")
	}
}
