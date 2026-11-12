package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

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
	// A start that fails is tried again at once: the waits between tries
	// are the engine's to test.
	return &app.App{Config: config.Default(), WSL: fake, History: history.Open(dir), Journal: journal.Open(dir, alive), TerminalIn: elsewhere,
		Sleep: func(time.Duration) {}}, fake
}

// shrinkWith runs a command line on a, returning the exit code and output.
func shrinkWith(a *app.App, args ...string) (int, string, string) {
	var o, e bytes.Buffer
	code := runWith(args, &o, &e, func() (*app.App, error) { return a, nil })
	return code, o.String(), e.String()
}

// inProcess is wsl-shrinker.exe for the screen's remote side, run in this
// test on a: one command line at a time, as separate programs are, which
// share nothing (the command line's language is the process's own).
func inProcess(a *app.App) remote.Runner {
	var one sync.Mutex
	return func(_ context.Context, args []string, out io.Writer) error {
		one.Lock()
		defer one.Unlock()
		var e bytes.Buffer
		if code := runWith(args, out, &e, func() (*app.App, error) { return a, nil }); code != exitOK {
			// The same words back as wsl-shrinker.exe's give.
			if err := remote.ErrorFrom(e.String()); err != nil {
				return err
			}
			return errors.New("exit status " + strconv.Itoa(code))
		}
		return nil
	}
}

func TestShrinkFromStartToEnd(t *testing.T) {
	// The whole command: the disk found by name, the engine's rule, the
	// journal, the steps printed one per line, the history, exit 0.
	a, fake := flowApp(t)
	code, out, errOut := shrinkWith(a, "shrink", "dev", "--yes")
	if code != exitOK {
		t.Fatalf("exit %d: %s\n%s", code, errOut, out)
	}
	for _, want := range []string{"Dev\n", "  check    done", "  stop     done     1 container\n", "  compact  done", "  verify   done", "  restart  done", "  freed "} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
	// Under the run, after what it freed: the container it started again
	// runs as long as WSL keeps Dev running.
	if i, j := strings.Index(out, "  freed "), strings.Index(out, "  note: with no terminal open in Dev, WSL stops it"); j < 0 || j < i {
		t.Errorf("no note about WSL stopping Dev under the run:\n%s", out)
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
	run := inProcess(a)
	ctx := context.Background()
	// What the backend asks, counted: the screen later asks from several
	// goroutines at once, hence the lock.
	var (
		asked []string
		mu    sync.Mutex
	)
	counted := func(ctx context.Context, args []string, out io.Writer) error {
		mu.Lock()
		asked = append(asked, strings.Join(args, " "))
		mu.Unlock()
		return run(ctx, args, out)
	}
	b, err := remote.Open(ctx, counted, "Other", version.String())
	if err != nil {
		t.Fatal(err)
	}
	// One start of wsl-shrinker.exe: hello brings the settings with it, the
	// same as settings says them.
	mu.Lock()
	opened := slices.Clone(asked)
	mu.Unlock()
	if len(opened) != 1 || opened[0] != "backend hello" {
		t.Fatalf("opening asked %q", opened)
	}
	var said remote.Settings
	var out bytes.Buffer
	if err := run(ctx, []string{"backend", "settings"}, &out); err != nil || json.Unmarshal(out.Bytes(), &said) != nil {
		t.Fatalf("settings: %v\n%s", err, out.String())
	}
	if !reflect.DeepEqual(said, b.Settings()) {
		t.Fatalf("hello's settings %+v, settings says %+v", b.Settings(), said)
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

func TestShrinkJSONFromInsideIsRefusedBeforeTheQuestion(t *testing.T) {
	// From a terminal inside Dev, a run of Dev moves to a new window, which
	// --json cannot follow: that is said before anything is asked.
	a, fake := flowApp(t)
	a.TerminalIn = func(d string) bool { return d == "Dev" }
	saved := stdinIsTerminal
	stdinIsTerminal = func() bool { return true }
	t.Cleanup(func() { stdinIsTerminal = saved })
	code, out, errOut := shrinkWith(a, "shrink", "Dev", "--json")
	if code != exitUsage || !strings.Contains(errOut, "--json cannot follow a run into a new window") || strings.Contains(errOut, "[y/N]") || out != "" {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	if calls := strings.Join(fake.CallList(), " "); strings.Contains(calls, "terminate:") || strings.Contains(calls, "compact:") {
		t.Fatalf("calls %s", calls)
	}
}

func TestListJSONSaysWhatStandsInTheWay(t *testing.T) {
	// A script learns a disk is protected from the list, not from a
	// shrink that fails.
	a, _ := flowApp(t)
	a.Config.Protect = []string{"dev"}
	code, out, errOut := shrinkWith(a, "list", "--json")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	var got struct {
		Disks []struct {
			Name      string `json:"name"`
			Protected bool   `json:"protected"`
			Blocked   string `json:"blocked"`
		} `json:"disks"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || len(got.Disks) != 1 || got.Disks[0].Name != "Dev" || !got.Disks[0].Protected || got.Disks[0].Blocked != "protected" {
		t.Fatalf("%v %+v\n%s", err, got, out)
	}
	// Nothing in the way: "" and false, never missing.
	a.Config.Protect = nil
	_, out, _ = shrinkWith(a, "list", "--json")
	if !strings.Contains(out, `"protected": false`) || !strings.Contains(out, `"blocked": ""`) {
		t.Fatalf("%s", out)
	}
	// The table says it in its state column.
	a.Config.Protect = []string{"Dev"}
	if _, out, _ = shrinkWith(a, "list"); !regexp.MustCompile(`(?m)^Dev\s+protected\s`).MatchString(out) {
		t.Fatalf("%s", out)
	}
}

func TestInsideJSONNamesTheDistributionAsWSLDoes(t *testing.T) {
	a, _ := flowApp(t)
	code, out, errOut := shrinkWith(a, "inside", "dev", "--json")
	if code != exitOK || !strings.Contains(out, `"distribution": "Dev"`) {
		t.Fatalf("exit %d: %s\n%s", code, errOut, out)
	}
}

func TestDoctorSaysAProtectedNameNoDiskHas(t *testing.T) {
	// "Gone" protects nothing: a typo, or a disk gone. Dev, in another
	// letter case, is a disk's.
	a, _ := flowApp(t)
	a.Config.Protect = []string{"dev", "Gone"}
	_, out, _ := shrinkWith(a, "doctor")
	if !strings.Contains(out, `protect names "Gone", which no disk is called now`) || strings.Contains(out, `protect names "dev"`) {
		t.Fatalf("%s", out)
	}
}

func TestAWSLThatDoesNotAnswerIsSaidSoWithWhatToDo(t *testing.T) {
	// WSL starting, updating or stuck: the commands end with that, and with
	// what to do, never with advice for an old WSL.
	a, fake := flowApp(t)
	silent := fmt.Errorf("asking WSL which distributions run: %w: wsl --list --running --quiet ran for 1m0s", wsl.ErrNoAnswer)
	fake.DistrosErr = silent
	code, _, errOut := shrinkWith(a, "list")
	if code == exitOK || !strings.Contains(errOut, "did not answer in time") || !strings.Contains(errOut, "wsl --shutdown restarts WSL") {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	fake.InfoErr = fmt.Errorf("%w: wsl --version ran for 1m0s", wsl.ErrNoAnswer)
	_, out, _ := shrinkWith(a, "doctor")
	if !strings.Contains(out, "WSL did not answer wsl --version in time") || strings.Contains(out, "wsl --update") || strings.Contains(out, "wsl --install") {
		t.Fatalf("%s", out)
	}
	if !strings.Contains(out, "could not list the distributions") || !strings.Contains(out, "may still be starting or updating") {
		t.Fatalf("%s", out)
	}
}

// fixedDisk writes the fake's only disk anew as a fixed-size one: every
// block in the file, all of them zeros but the two that held data.
func fixedDisk(t *testing.T, fake *wsl.Fake) {
	t.Helper()
	blocks := map[int64][]byte{0: {1}, 3: {1}}
	for i := int64(0); i < 16; i++ {
		if _, ok := blocks[i]; !ok {
			blocks[i] = []byte{}
		}
	}
	vhdxtest.Write(t, fake.List[0].VHD, vhdxtest.Spec{VirtualSize: 16 << 20, Blocks: blocks, Fixed: true})
}

func TestAFixedSizeDiskIsSaidAndRefusedOnTheCommandLine(t *testing.T) {
	// Windows' compaction of a fixed-size disk reports success and leaves
	// the file as it is. Every command says what the disk is and promises
	// nothing; shrink refuses it before the question, with nothing stopped.
	a, fake := flowApp(t)
	fixedDisk(t, fake)
	// The table: its state, the file after a shrink as it is now, nothing
	// to free.
	code, out, errOut := shrinkWith(a, "list")
	row := regexp.MustCompile(`(?m)^Dev\s+fixed\s+(\S+ \S+)\s+(\S+ \S+)\s+0 B\s*$`).FindStringSubmatch(out)
	if code != exitOK || row == nil || row[1] != row[2] {
		t.Fatalf("exit %d: %s\n%s", code, errOut, out)
	}
	// For a script: what stands in the way, and the estimate's own word.
	_, out, _ = shrinkWith(a, "list", "--json")
	var got struct {
		Disks []struct {
			Blocked  string `json:"blocked"`
			Estimate struct {
				Fixed   bool  `json:"fixed"`
				File    int64 `json:"file_size_bytes"`
				After   int64 `json:"after_bytes"`
				Reclaim int64 `json:"reclaim_bytes"`
			} `json:"estimate"`
		} `json:"disks"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || len(got.Disks) != 1 {
		t.Fatalf("%v\n%s", err, out)
	}
	if d := got.Disks[0]; d.Blocked != "fixed" || !d.Estimate.Fixed || d.Estimate.Reclaim != 0 || d.Estimate.After != d.Estimate.File || d.Estimate.File < 16<<20 {
		t.Fatalf("%+v\n%s", d, out)
	}
	// shrink: refused with the reason, whether asked to ask or not.
	for _, args := range [][]string{{"shrink", "Dev", "--yes"}, {"shrink", "dev", "--yes", "--json"}} {
		code, out, errOut = shrinkWith(a, args...)
		if code != exitFail || !strings.Contains(errOut, "Dev cannot be shrunk now: the disk is fixed-size") || !strings.Contains(errOut, "only disks that grow") {
			t.Fatalf("%v: exit %d: %s\n%s", args, code, errOut, out)
		}
	}
	// Nothing was stopped or compacted, and no run was written down.
	if calls := strings.Join(fake.CallList(), " "); strings.Contains(calls, "terminate:") || strings.Contains(calls, "compact:") {
		t.Fatalf("calls %s", calls)
	}
	if all, _ := a.History.All(); len(all) != 0 {
		t.Fatalf("history %+v", all)
	}
	// doctor: said as what the disk is, not as a problem of this PC.
	code, out, errOut = shrinkWith(a, "doctor")
	if !strings.Contains(out, "Dev: a fixed-size disk; its file keeps its full size") || strings.Contains(errOut, "problem") {
		t.Fatalf("exit %d: %s\n%s", code, errOut, out)
	}
}

func TestTheOverviewSaysAFixedSizeDisk(t *testing.T) {
	// CLI mode's picture, in both languages: the state in the disk's row,
	// why beside it, and no shrink suggested for the only disk there is.
	for lang, row := range map[string]string{
		"en": `(?m)^\S+\s+Dev\s+fixed\s+(\S+ \S+)\s+(\S+ \S+)\s+0 B\s+Windows compacts only disks that grow$`,
		"ko": `(?m)^\S+\s+Dev\s+고정 크기\s+(\S+ \S+)\s+(\S+ \S+)\s+0 B\s+Windows는 커지는 디스크만 압축함$`,
	} {
		a, fake := flowApp(t)
		fixedDisk(t, fake)
		withSettings(t, a, "mode = \"cli\"\nlanguage = \""+lang+"\"\n")
		inTerminal(t, tui.SetupChoice{}, false)
		code, out, errOut := shrinkWith(a)
		plain := ansi.Strip(out)
		if code != exitOK || strings.Contains(plain, "wsl-shrinker shrink") {
			t.Fatalf("%s: exit %d: %s\n%s", lang, code, errOut, plain)
		}
		// The file after a shrink is the file as it is.
		if m := regexp.MustCompile(row).FindStringSubmatch(plain); m == nil || m[1] != m[2] {
			t.Errorf("%s: no row like %s in:\n%s", lang, row, plain)
		}
	}
}
