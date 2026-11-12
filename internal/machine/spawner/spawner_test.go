package spawner

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The fake wsl-spawner is this test binary, started again with these
// variables: it plays back what the real one printed in a case recorded in
// testdata (see testdata/README.md), after writing down how it was called.
const (
	// fakeCase names the case to play back; set, the binary is the fake.
	fakeCase = "WSL_SHRINKER_FAKE_SPAWNER"
	// fakeDir is the folder of the recorded cases.
	fakeDir = "WSL_SHRINKER_FAKE_SPAWNER_DIR"
	// fakeRecord is the file it writes its arguments and environment to.
	fakeRecord = "WSL_SHRINKER_FAKE_SPAWNER_RECORD"
	// fakeHang makes it wait this long after printing, as a stop still
	// going on would, before it exits.
	fakeHang = "WSL_SHRINKER_FAKE_SPAWNER_HANG"
)

func TestMain(m *testing.M) {
	if c := os.Getenv(fakeCase); c != "" {
		os.Exit(playBack(c))
	}
	os.Exit(m.Run())
}

// playBack is the fake wsl-spawner: it records its command line and
// whether WSL_DISTRO_NAME reached it, prints the case's recorded stdout and
// stderr, and exits with the recorded code.
func playBack(c string) int {
	dir := os.Getenv(fakeDir)
	got := strings.Join(os.Args[1:], " ") + "\nWSL_DISTRO_NAME=" + os.Getenv("WSL_DISTRO_NAME") + "\n"
	_ = os.WriteFile(os.Getenv(fakeRecord), []byte(got), 0o644)
	out, _ := os.ReadFile(filepath.Join(dir, c+".stdout"))
	errOut, _ := os.ReadFile(filepath.Join(dir, c+".stderr"))
	code, _ := os.ReadFile(filepath.Join(dir, c+".code"))
	_, _ = os.Stdout.Write(out)
	_, _ = os.Stderr.Write(errOut)
	if d, err := time.ParseDuration(os.Getenv(fakeHang)); err == nil {
		time.Sleep(d)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(code)))
	if err != nil {
		return 99
	}
	return n
}

// fake is an Exe that is this test binary playing case c, and the file
// it writes how it was called to.
func fake(t *testing.T, c string) (Exe, string) {
	t.Helper()
	dir, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(t.TempDir(), "called.txt")
	t.Setenv(fakeCase, c)
	t.Setenv(fakeDir, dir)
	t.Setenv(fakeRecord, record)
	return Exe{Path: self}, record
}

// stop runs a stop of Ubuntu-24.04, the name the recorded cases stop, and
// returns the steps it said, its result and its error.
func stop(t *testing.T, x Exe) ([]Step, Result, error) {
	t.Helper()
	var steps []Step
	res, err := x.Stop(context.Background(), "Ubuntu-24.04", func(s Step) { steps = append(steps, s) })
	return steps, res, err
}

// names is the steps' names, a hook's with its file.
func names(steps []Step) []string {
	var out []string
	for _, s := range steps {
		if s.Hook != "" {
			out = append(out, s.Name+":"+s.Hook)
			continue
		}
		out = append(out, s.Name)
	}
	return out
}

func TestEachEndIsReadAsWslSpawnerPrintsIt(t *testing.T) {
	for _, c := range []struct {
		name string
		// end is the result's End; steps the steps said, in order.
		end   string
		steps []string
		// errHas is a word the result's Err holds, "" for none.
		errHas string
	}{
		// Every step in its time, then WSL ended the rest.
		{name: "clean", end: EndClean, steps: []string{"docker", "sync", StepTerminate}},
		// A hook failed (exit 3): the stop went on and ended it all the same.
		{name: "hookfailed", end: EndFailed, steps: []string{"hook:10-save", "docker", "sync", StepTerminate}},
		// Docker ran out of the time left (exit 3).
		{name: "limit", end: EndLimit, steps: []string{"docker", StepTerminate}},
		// Another stop of it runs (exit 0): this one ended nothing.
		{name: "busy", end: EndBusy, errHas: "another stop of Ubuntu-24.04 is running"},
		// The stop inside did not run to its end (exit 3); WSL ended it.
		{name: "error", end: EndError, steps: []string{StepTerminate}, errHas: "did not run to its end"},
	} {
		t.Run(c.name, func(t *testing.T) {
			x, _ := fake(t, c.name)
			steps, res, err := stop(t, x)
			if err != nil || res.End != c.end || res.Detached || res.NotRunning {
				t.Fatalf("%+v, %v", res, err)
			}
			if got := names(steps); !slices.Equal(got, c.steps) {
				t.Fatalf("steps %q, want %q", got, c.steps)
			}
			if c.errHas != "" && !strings.Contains(res.Err, c.errHas) || c.errHas == "" && res.Err != "" {
				t.Fatalf("error %q", res.Err)
			}
		})
	}
	// A step's count is said with it: docker names the two containers.
	x, _ := fake(t, "clean")
	if steps, _, _ := stop(t, x); len(steps) == 0 || steps[0].Count != 2 {
		t.Fatalf("%+v", steps)
	}
}

func TestWhatStopsNothingSaysWhy(t *testing.T) {
	// Refused (exit 2, nothing on stdout): its own words, without its name
	// in front, say why; the run then stops the distribution itself.
	for c, words := range map[string]string{
		"skipped": "Work is left to WSL in the settings",
		"unknown": "no such distribution: Nope",
	} {
		x, _ := fake(t, c)
		_, _, err := stop(t, x)
		if !errors.Is(err, ErrNotStopped) || !strings.Contains(err.Error(), words) || strings.Contains(err.Error(), "wsl-spawner: ") {
			t.Fatalf("%s: %v", c, err)
		}
	}
	// A program that is not there stopped nothing either.
	_, _, err := stop(t, Exe{Path: filepath.Join(t.TempDir(), "wsl-spawner.exe")})
	if !errors.Is(err, ErrNotStopped) {
		t.Fatalf("%v", err)
	}
}

func TestNothingRunningAndGoingOnInTheBackgroundAreTheirOwnResults(t *testing.T) {
	// No results and no error: the distribution did not run.
	x, _ := fake(t, "notrunning")
	if _, res, err := stop(t, x); err != nil || !res.NotRunning || res.End != "" {
		t.Fatalf("%+v %v", res, err)
	}
	// detached_from: it went on without this call.
	x, _ = fake(t, "detached")
	if _, res, err := stop(t, x); err != nil || !res.Detached {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestTheCallIsTheDocumentedOneWithoutTheTerminalsDistribution(t *testing.T) {
	// The window a run moved to from inside WSL still has WSL_DISTRO_NAME:
	// passed on, it would make wsl-spawner go on in the background.
	x, record := fake(t, "clean")
	t.Setenv("WSL_DISTRO_NAME", "Ubuntu-24.04")
	if _, _, err := stop(t, x); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	if want := "stop --yes --json --events Ubuntu-24.04\nWSL_DISTRO_NAME=\n"; string(b) != want {
		t.Fatalf("called with %q, want %q", b, want)
	}
	// In any letter case: Windows' variable names ignore it.
	env := withoutTerminalDistro([]string{"Path=x", "wsl_distro_name=Dev", "WSL_DISTRO_NAME=Dev", "WSLENV=WSL_DISTRO_NAME/u"})
	if !slices.Equal(env, []string{"Path=x", "WSLENV=WSL_DISTRO_NAME/u"}) {
		t.Fatalf("%q", env)
	}
}

func TestACallStillGoingAtTheRunsLimitSaysWhetherItBegan(t *testing.T) {
	// It printed its steps and the results, then hung: killed at the
	// limit, what it printed is not its end.
	x, _ := fake(t, "clean")
	t.Setenv(fakeHang, "30s")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	t0 := time.Now()
	_, err := x.Stop(ctx, "Ubuntu-24.04", nil)
	if !errors.Is(err, ErrNoResult) {
		t.Fatalf("%v", err)
	}
	// Killed at the limit, not after the hang.
	if took := time.Since(t0); took > 15*time.Second {
		t.Fatalf("took %s", took)
	}
	// One that said nothing of the distribution before the limit stopped
	// nothing this call knows of.
	x, _ = fake(t, "skipped")
	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()
	if _, err := x.Stop(ctx2, "Ubuntu-24.04", nil); !errors.Is(err, ErrNotStopped) {
		t.Fatalf("%v", err)
	}
}

func TestLinesAreReadHoweverTheyArrive(t *testing.T) {
	// One byte at a time, as a pipe may hand them over, the steps and the
	// end are the same as all at once.
	b, err := os.ReadFile(filepath.Join("testdata", "hookfailed.stdout"))
	if err != nil {
		t.Fatal(err)
	}
	var steps []Step
	r := &reader{distro: "Ubuntu-24.04", on: func(s Step) { steps = append(steps, s) }}
	for i := range b {
		if n, err := r.Write(b[i : i+1]); n != 1 || err != nil {
			t.Fatalf("write took %d: %v", n, err)
		}
	}
	r.flush()
	res, err := r.outcome(context.Background(), nil, "")
	if err != nil || res.End != EndFailed || len(steps) != 4 {
		t.Fatalf("%+v %v %+v", res, err, steps)
	}
	// Each Write says it took all of what it was given.
	r = &reader{distro: "Ubuntu-24.04"}
	if n, err := r.Write(b); n != len(b) || err != nil {
		t.Fatalf("took %d of %d: %v", n, len(b), err)
	}
}

func TestALineTooLongIsPassedOverAndTheRestRead(t *testing.T) {
	// A line past the limit, with no break for a while, then the real end:
	// the long one is dropped, nothing of it kept, and the next one read.
	r := &reader{distro: "Ubuntu-24.04"}
	long := bytes.Repeat([]byte("x"), maxLine+10)
	_, _ = r.Write(long[:maxLine/2])
	_, _ = r.Write(long[maxLine/2:])
	if len(r.buf) != 0 || !r.skipping {
		t.Fatalf("kept %d bytes, skipping %v", len(r.buf), r.skipping)
	}
	_, _ = r.Write([]byte("\n" + `{"event":"finished","distro":"Ubuntu-24.04","took_seconds":2,"result":"clean"}` + "\n"))
	res, err := r.outcome(context.Background(), nil, "")
	if err != nil || res.End != EndClean || res.Took != 2*time.Second {
		t.Fatalf("%+v %v", res, err)
	}
	// Another distribution's lines are not this one's.
	r = &reader{distro: "Dev"}
	_, _ = r.Write([]byte(`{"event":"line","distro":"Ubuntu-24.04","kind":"start","step":"docker"}` + "\n"))
	if r.began {
		t.Fatal("another distribution's step counted")
	}
	// What is kept of stderr stops at its limit.
	c := &capped{max: 4}
	if n, _ := c.Write([]byte("abcdef")); n != 6 || c.String() != "abcd" {
		t.Fatalf("%d %q", n, c.String())
	}
}

func TestFindNeverFindsTheRealOneInATest(t *testing.T) {
	// On this PC's Windows, a test that found the installed wsl-spawner
	// would stop a real distribution of a made-up one's name.
	if x, ok := Find(); ok {
		t.Fatalf("found %s", x.Path)
	}
}
