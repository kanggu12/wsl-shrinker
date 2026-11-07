//go:build windows

package winapi

import (
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
