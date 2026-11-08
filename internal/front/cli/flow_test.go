package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/kanggu12/wsl-shrinker/internal/app"
	"github.com/kanggu12/wsl-shrinker/internal/app/remote"
	"github.com/kanggu12/wsl-shrinker/internal/engine/history"
	"github.com/kanggu12/wsl-shrinker/internal/engine/journal"
	"github.com/kanggu12/wsl-shrinker/internal/engine/shrink"
	"github.com/kanggu12/wsl-shrinker/internal/front/tui"
	"github.com/kanggu12/wsl-shrinker/internal/machine/wsl"
	"github.com/kanggu12/wsl-shrinker/internal/settings/config"
	"github.com/kanggu12/wsl-shrinker/internal/testutil/vhdxtest"
	"github.com/kanggu12/wsl-shrinker/internal/version"
)

// flowApp is an App on a fake WSL with one running distribution, Dev, on a
// small VHDX in the test's folder, with its history and journal there too.
// The command line runs on it from start to end, as on a PC.
func flowApp(t *testing.T) (*app.App, *wsl.Fake) {
	t.Helper()
	dir := t.TempDir()
	// Nothing the commands read on their own reaches the person's files.
	settingsIn(t, dir)
	path := filepath.Join(dir, "ext4.vhdx")
	vhdxtest.Write(t, path, vhdxtest.Spec{VirtualSize: 16 << 20, Blocks: map[int64][]byte{0: {1}, 3: {1}}})
	fake := &wsl.Fake{List: []wsl.Distro{{Name: "Dev", VHD: path, Version: 2, Running: true}}}
	fake.Script = func(_, _, s string) (string, error) {
		switch {
		case strings.Contains(s, "docker ps -q"):
			// One container running, as the stop step names it.
			return "running|0123456789ab \n", nil
		case strings.Contains(s, "/proc/mounts"):
			return "rw,relatime\n", nil
		case strings.Contains(s, "docker start"):
			return "started|0123456789ab\n", nil
		case strings.Contains(s, "docker info") && strings.Contains(s, "echo up"):
			// Docker answers at once: the restart does not wait for it.
			return "up\n", nil
		}
		return "", nil
	}
	alive := func(int, string) bool { return false }
	// This test's terminal is in no distribution: on Windows a test binary
	// run from WSL would look inside one, and its run would move to a new
	// window.
	elsewhere := func(string) bool { return false }
	return &app.App{Config: config.Default(), WSL: fake, History: history.Open(dir), Journal: journal.Open(dir, alive), TerminalIn: elsewhere}, fake
}

// shrinkWith runs a command line on a, returning the exit code and output.
func shrinkWith(a *app.App, args ...string) (int, string, string) {
	var o, e bytes.Buffer
	code := runWith(args, &o, &e, func() (*app.App, error) { return a, nil })
	return code, o.String(), e.String()
}

func TestShrinkFromStartToEnd(t *testing.T) {
	// The whole command: the disk found by name, the engine's rule, the
	// journal, the steps printed one per line, the history, exit 0.
	a, fake := flowApp(t)
	code, out, errOut := shrinkWith(a, "shrink", "dev", "--yes")
	if code != exitOK {
		t.Fatalf("exit %d: %s\n%s", code, errOut, out)
	}
	for _, want := range []string{"Dev\n", "  check    done", "  stop     done     1 containers", "  compact  done", "  verify   done", "  restart  done", "  freed "} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
	// Under the run, after what it freed: the container it started again
	// runs as long as WSL keeps Dev running.
	if i, j := strings.Index(out, "  freed "), strings.Index(out, "  note: WSL stops Dev again once no terminal"); j < 0 || j < i {
		t.Errorf("no note about WSL stopping Dev again under the run:\n%s", out)
	}
	// WSL compacted it and started it again; the container came back.
	calls := strings.Join(fake.CallList(), " ")
	if !strings.Contains(calls, "compact:Dev") || !strings.Contains(calls, "start:Dev") {
		t.Fatalf("calls %s", calls)
	}
	// The run is in the history, with its steps; no journal is left.
	all, _ := a.History.All()
	if len(all) != 1 || all[0].Left != "running" || len(all[0].Events) == 0 {
		t.Fatalf("history %+v", all)
	}
	if _, st, _ := a.Journal.Read(); st != journal.None {
		t.Fatalf("journal left: %v", st)
	}
}

func TestShrinkThatLeavesItDownExitsThree(t *testing.T) {
	// The distribution did not start again: the output says what to do,
	// and the exit code is 3, not a plain 1.
	a, fake := flowApp(t)
	fake.Fail = map[string]error{"start:Dev": errors.New("WSL_E_VM_MODE_INVALID_STATE")}
	code, out, errOut := shrinkWith(a, "shrink", "Dev", "--yes")
	if code != exitAttention || !strings.Contains(out, "Dev did not start again: wsl -d Dev starts it") {
		t.Fatalf("exit %d: %s\n%s", code, errOut, out)
	}
	// A failure that left it running is a plain 1.
	a, fake = flowApp(t)
	fake.Fail = map[string]error{"compact:Dev": errors.New("E_ACCESSDENIED")}
	if code, out, _ := shrinkWith(a, "shrink", "Dev", "--yes"); code != exitFail || !strings.Contains(out, "Dev runs as it did") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
}

func TestShrinkJSONSaysHowItLeftTheDisk(t *testing.T) {
	a, _ := flowApp(t)
	code, out, errOut := shrinkWith(a, "shrink", "Dev", "--yes", "--json")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	var got struct {
		Schema  int `json:"schema"`
		Results []struct {
			Disk      string `json:"disk"`
			Left      string `json:"left"`
			Compacted bool   `json:"compacted"`
			Events    []struct {
				Step       string   `json:"step"`
				Containers []string `json:"containers"`
			} `json:"events"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.Schema != 1 || len(got.Results) != 1 {
		t.Fatalf("%v\n%s", err, out)
	}
	r := got.Results[0]
	if r.Disk != "Dev" || r.Left != "running" || !r.Compacted {
		t.Fatalf("%+v", r)
	}
	// The stop step names the containers it stopped.
	i := slices.IndexFunc(r.Events, func(e struct {
		Step       string   `json:"step"`
		Containers []string `json:"containers"`
	}) bool {
		return e.Step == "stop" && len(e.Containers) == 1
	})
	if i < 0 {
		t.Fatalf("no stop event with its container: %+v", r.Events)
	}
}

func TestSettingsAreReadOnce(t *testing.T) {
	// The language before the command and the command itself use the same
	// App: the maker runs once a command line.
	a, _ := flowApp(t)
	made := 0
	var o, e bytes.Buffer
	runWith([]string{"history"}, &o, &e, func() (*app.App, error) { made++; return a, nil })
	if made != 1 {
		t.Fatalf("the App was made %d times", made)
	}
}

func TestTheLinuxScreenTalksToTheProgram(t *testing.T) {
	// The screen on the Linux side of WSL asks wsl-shrinker.exe everything
	// through the backend command. Here both ends run in this test: the
	// remote side, and the command line on the fake PC.
	a, fake := flowApp(t)
	run := func(_ context.Context, args []string, out io.Writer) error {
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
	if info := b.Info(ctx); !info.CanCompact {
		t.Fatalf("info %+v", info)
	}
	// The settings carry the mode, and whether setup has to come first: the
	// launcher decides with them what a bare start opens.
	if s := b.Settings(); s.Mode != "tui" || s.First {
		t.Fatalf("settings %+v", s)
	}
	withSettings(t, a, "")
	if b2, err := remote.Open(ctx, run, "Other", version.String()); err != nil || !b2.Settings().First {
		t.Fatalf("a PC with no settings file: %+v %v", b2, err)
	}
	ds, err := b.Disks(ctx)
	if err != nil || len(ds) != 1 || ds[0].Name != "Dev" || ds[0].Estimate == nil {
		t.Fatalf("disks %+v %v", ds, err)
	}
	if _, st := b.Pending(); st != journal.None {
		t.Fatalf("pending %v", st)
	}
	// A run: its events come as it goes, then the result.
	var seen []shrink.Event
	res := b.Shrink(ctx, ds[0], shrink.Options{}, func(e shrink.Event) { seen = append(seen, e) })
	if res.Err != "" || res.Left != shrink.LeftRunning || len(seen) < 6 {
		t.Fatalf("result %+v after %d events", res, len(seen))
	}
	if !slices.Contains(fake.CallList(), "compact:Dev") {
		t.Fatalf("calls %v", fake.CallList())
	}
	if h := b.History(); len(h) != 1 {
		t.Fatalf("history %+v", h)
	}
	// And the screen draws over it, this terminal's distribution apart.
	frame := ansi.Strip(tui.Snapshot(b, 120, 30, true, nil, ""))
	if !strings.Contains(frame, "Dev") || b.Hosts("Dev") {
		t.Fatalf("\n%s", frame)
	}
}

func TestTheBackendRefusesItemsOfAnotherDisk(t *testing.T) {
	// The screen names the disk it shrinks; an item of any other is a
	// mistake to say, never one to drop without a word.
	a, fake := flowApp(t)
	code, _, errOut := shrinkWith(a, "backend", "shrink", "--disk", "Dev", "--clean", "Other=apt")
	if code != exitUsage || !strings.Contains(errOut, `--clean names "Other", which is not being shrunk`) {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if slices.Contains(fake.CallList(), "compact:Dev") {
		t.Fatalf("ran: %v", fake.CallList())
	}
}
