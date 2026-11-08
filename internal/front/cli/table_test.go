package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kanggu12/wsl-shrinker/internal/app"
	"github.com/kanggu12/wsl-shrinker/internal/engine/disks"
	"github.com/kanggu12/wsl-shrinker/internal/engine/shrink"
	"github.com/kanggu12/wsl-shrinker/internal/engine/survey"
	"github.com/kanggu12/wsl-shrinker/internal/machine/wsl"
)

func TestTableLinesUpWordsLeftAndNumbersRight(t *testing.T) {
	var b bytes.Buffer
	rows := [][]string{
		{"DISK", "STATE", "FILE", ""},
		{"Debian", "stopped", "8.3 GB", ""},
		{"Ubuntu-24.04", "running", "471 GB", ""},
		// A name in Korean takes two cells a letter.
		{"우분투", "file", "12.0 MB", "cannot read"},
	}
	if err := writeTable(&b, []bool{false, false, true, false}, rows); err != nil {
		t.Fatal(err)
	}
	want := "DISK          STATE       FILE\n" +
		"Debian        stopped   8.3 GB\n" +
		"Ubuntu-24.04  running   471 GB\n" +
		"우분투        file     12.0 MB  cannot read\n"
	if b.String() != want {
		t.Fatalf("got\n%s\nwant\n%s", b.String(), want)
	}
}

func TestPickItemsJoinsIdsThatHoldCommas(t *testing.T) {
	items := []survey.Item{{ID: "apt"}, {ID: "pip:/home/a,b/.cache/pip"}, {ID: "journal"}}
	ids := func(its []survey.Item) []string {
		var out []string
		for _, it := range its {
			out = append(out, it.ID)
		}
		return out
	}
	// A folder with a comma in its name is one id; one named twice is one item.
	got, bad := pickItems("apt,pip:/home/a,b/.cache/pip,apt,journal", items)
	if bad != "" || !slices.Equal(ids(got), []string{"apt", "pip:/home/a,b/.cache/pip", "journal"}) {
		t.Fatalf("%v, %q", ids(got), bad)
	}
	// An id that names nothing is reported by its first piece.
	if _, bad := pickItems("apt,nope,journal", items); bad != "nope" {
		t.Fatalf("%q", bad)
	}
}

func TestScreenCleanDropsRepeats(t *testing.T) {
	// One item per flag, a repeat counted once, and an id that holds a comma
	// kept whole.
	got, err := parseScreenClean([]string{"Ubuntu=apt", "Ubuntu=apt", "Ubuntu=journal", "Ubuntu=pip:/home/a,b/.cache/pip"})
	if err != nil || !slices.Equal(got["Ubuntu"], []string{"apt", "journal", "pip:/home/a,b/.cache/pip"}) {
		t.Fatalf("%v, %v", got, err)
	}
}

func TestReopenedRunGetsEveryItemWhole(t *testing.T) {
	// From inside WSL the run goes on in a new window: what this command
	// resolved reaches it item by item, an id with a comma in it included.
	queue := []disks.Disk{{Name: "Ubuntu"}, {Name: "Debian"}}
	chosen := map[string][]survey.Item{"Ubuntu": {{ID: "apt"}, {ID: "pip:/home/a,b/.cache/pip"}}}
	args := app.HandoffArgs("ko", queue, chosen)
	want := "--reopened --lang ko --run Ubuntu --clean Ubuntu=apt --clean Ubuntu=pip:/home/a,b/.cache/pip --run Debian"
	if got := strings.Join(args, " "); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	// And the new window reads back the same ids.
	var flags []string
	for i, a := range args {
		if a == "--clean" {
			flags = append(flags, args[i+1])
		}
	}
	got, err := parseScreenClean(flags)
	if err != nil || !slices.Equal(got["Ubuntu"], []string{"apt", "pip:/home/a,b/.cache/pip"}) {
		t.Fatalf("%v, %v", got, err)
	}
}

func TestAskEndsOnCtrlC(t *testing.T) {
	// Nobody types: the read blocks, as on a terminal.
	r, w := io.Pipe()
	defer w.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	var out bytes.Buffer
	go func() {
		_, err := ask(ctx, r, &out, "Shrink? [y/N] ")
		done <- err
	}()
	// Ctrl+C reaches the program as the context ending.
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("no error after Ctrl+C")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the question still waits after Ctrl+C")
	}
}

func TestAskReadsYes(t *testing.T) {
	for in, want := range map[string]bool{"y\n": true, "YES\r\n": true, "\n": false, "": false, "nope\n": false} {
		if got, err := ask(context.Background(), strings.NewReader(in), io.Discard, "?"); got != want || err != nil {
			t.Errorf("%q: %v, %v", in, got, err)
		}
	}
}

func TestUnknownDistributionIsAUsageError(t *testing.T) {
	// "inside nosuch" names a distribution WSL does not have: a mistake in
	// the command line, like an unknown disk for shrink.
	if got := exitFor(fmt.Errorf("look inside: %w: nosuch", wsl.ErrNoDistro)); got != exitUsage {
		t.Errorf("exit %d", got)
	}
	if got := exitFor(errors.New("wsl.exe failed")); got != exitFail {
		t.Errorf("a failure exits %d", got)
	}
}

func TestLeftWordsSayWhatToDoNext(t *testing.T) {
	// After a failure the command line says whether the disk changed and
	// whether the distribution runs, as the engine reports it.
	for _, c := range []struct {
		r    shrink.Result
		want string
	}{
		// A check that refused changed nothing, and says so in its error.
		{shrink.Result{Disk: "Dev", FailedAt: shrink.StepCheck, Left: shrink.LeftRunning}, ""},
		{shrink.Result{Disk: "Dev", FailedAt: shrink.StepCompact, Left: shrink.LeftRunning}, "the disk was not changed; Dev runs as it did"},
		{shrink.Result{Disk: "Dev", FailedAt: shrink.StepStop, Left: shrink.LeftRunning, ContainersNotStarted: 1}, "the disk was not changed; Dev runs again, 1 containers did not start"},
		{shrink.Result{Disk: "Dev", FailedAt: shrink.StepRestart, Compacted: true, Left: shrink.LeftDown}, "the disk was compacted; Dev did not start again: wsl -d Dev starts it"},
		{shrink.Result{Disk: "Dev", FailedAt: shrink.StepVerify, Compacted: true, Left: shrink.LeftSuspect}, "the disk was compacted, but it does not read as before; Dev stays stopped until you know why (see Troubleshooting)"},
		{shrink.Result{Disk: "data.vhdx", FailedAt: shrink.StepCompact, Left: shrink.LeftFile}, "the disk was not changed"},
	} {
		if got := leftWords(c.r); got != c.want {
			t.Errorf("%+v:\ngot  %q\nwant %q", c.r, got, c.want)
		}
	}
}
