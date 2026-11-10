package remote

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kanggu12/wsl-shrinker/internal/app"
	"github.com/kanggu12/wsl-shrinker/internal/engine/disks"
	"github.com/kanggu12/wsl-shrinker/internal/engine/journal"
	"github.com/kanggu12/wsl-shrinker/internal/engine/shrink"
	"github.com/kanggu12/wsl-shrinker/internal/engine/survey"
)

// answers is a stand-in wsl-shrinker.exe: for each command line (joined by
// spaces), what it prints, written in pieces of chunk bytes, and the error
// it ends with. It records the command lines it was asked.
type answers struct {
	out   map[string]string
	err   map[string]error
	chunk int
	asked []string
}

func (a *answers) run(_ context.Context, args []string, out io.Writer) error {
	key := strings.Join(args, " ")
	a.asked = append(a.asked, key)
	s := a.out[key]
	// Written in small pieces, as a pipe delivers it.
	for a.chunk > 0 && len(s) > a.chunk {
		if _, err := io.WriteString(out, s[:a.chunk]); err != nil {
			return err
		}
		s = s[a.chunk:]
	}
	if _, err := io.WriteString(out, s); err != nil {
		return err
	}
	return a.err[key]
}

// thisBuild answers hello and settings as the same build does.
func thisBuild() *answers {
	return &answers{out: map[string]string{
		"backend hello":    `{"schema":1,"version":"v9.9.9"}`,
		"backend settings": `{"schema":1,"language":"ko","theme":"dark","min_gain_bytes":1073741824,"protect":["Work"]}`,
	}, err: map[string]error{}}
}

func TestOpenMeetsOnlyItsOwnBuild(t *testing.T) {
	a := thisBuild()
	b, err := Open(context.Background(), a.run, "Ubuntu", "v9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	if s := b.Settings(); s.Language != "ko" || s.Theme != "dark" || !b.Protected("work") || b.MinGain() != 1<<30 {
		t.Fatalf("%+v", s)
	}
	// Another build: the screen then runs in it, as before.
	if _, err := Open(context.Background(), a.run, "Ubuntu", "v1.0.0"); !errors.Is(err, ErrOtherBuild) {
		t.Fatalf("got %v", err)
	}
	// An older one with no backend at all answers with its usage error.
	old := &answers{out: map[string]string{}, err: map[string]error{"backend hello": errors.New(`unknown command "backend"`)}}
	if _, err := Open(context.Background(), old.run, "Ubuntu", "v9.9.9"); !errors.Is(err, ErrOtherBuild) {
		t.Fatalf("got %v", err)
	}
}

func TestAnswersAreRead(t *testing.T) {
	a := thisBuild()
	a.out["backend info"] = `{"schema":1,"version":"3.0.3.0","can_compact":true}`
	a.out["list --json"] = `{"schema":1,"disks":[{"name":"Ubuntu","kind":"distro","distro":"Ubuntu","path":"C:\\d\\ext4.vhdx","running":true,"file":{"size_bytes":10}}]}`
	a.out["inside Ubuntu --json"] = `{"schema":1,"distribution":"Ubuntu","size_bytes":100,"used_bytes":50,"items":[{"id":"apt","what":"apt","label":"/var/cache/apt/archives","bytes":7,"kind":"cache"}],"docker":true,"containers":2}`
	a.out["history --json"] = `{"schema":1,"runs":[{"at":"2026-10-18T09:12:00Z","disk":"Ubuntu","freed_bytes":5,"took_seconds":3}]}`
	a.out["backend pending"] = `{"schema":1,"state":"cutoff","run":{"pid":7,"exe":"wsl-shrinker.exe","disk":"Ubuntu","step":"compact","stopped":true}}`
	b, err := Open(context.Background(), a.run, "Ubuntu", "v9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if info := b.Info(ctx); info.Version != "3.0.3.0" || !info.CanCompact {
		t.Fatalf("info %+v", info)
	}
	ds, err := b.Disks(ctx)
	if err != nil || len(ds) != 1 || ds[0].Name != "Ubuntu" || !ds[0].Running || ds[0].File.Size != 10 {
		t.Fatalf("disks %+v %v", ds, err)
	}
	r, err := b.Survey(ctx, "Ubuntu")
	if err != nil || r.Size != 100 || len(r.Items) != 1 || r.Items[0].ID != "apt" || r.Containers != 2 || !r.Docker {
		t.Fatalf("survey %+v %v", r, err)
	}
	// The commands never come across: the Windows side builds its own.
	if r.Items[0].Command != "" {
		t.Fatalf("a command came across: %q", r.Items[0].Command)
	}
	if h := b.History(); len(h) != 1 || h[0].Freed != 5 {
		t.Fatalf("history %+v", h)
	}
	if e, st := b.Pending(); st != journal.CutOff || e.Disk != "Ubuntu" || !e.Stopped {
		t.Fatalf("pending %+v %v", e, st)
	}
	// This terminal's distribution is known by name, in any case.
	if !b.Hosts("ubuntu") || b.Hosts("Debian") {
		t.Fatal("hosts")
	}
}

func TestShrinkPassesEventsOnAsTheyCome(t *testing.T) {
	a := thisBuild()
	// Seven bytes at a time: lines arrive in pieces, as from a pipe.
	a.chunk = 7
	a.out["backend shrink --disk Ubuntu --clean Ubuntu=apt --clean Ubuntu=pip:/home/a,b/.cache/pip"] = `{"event":{"step":"check","status":"running"}}
{"event":{"step":"check","status":"done","bytes":5}}
{"event":{"step":"stop","status":"done","count":1,"containers":["0123456789ab"]}}
{"result":{"disk":"Ubuntu","freed_bytes":5,"events":null,"compacted":true,"left":"running","took_seconds":2}}
`
	b, err := Open(context.Background(), a.run, "Ubuntu", "v9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	var seen []shrink.Event
	d := disks.Disk{Name: "Ubuntu", Distro: "Ubuntu", Running: true}
	items := []survey.Item{{ID: "apt"}, {ID: "pip:/home/a,b/.cache/pip"}}
	res := b.Shrink(context.Background(), d, shrink.Options{Clean: items}, func(e shrink.Event) { seen = append(seen, e) })
	if len(seen) != 3 || seen[2].Containers[0] != "0123456789ab" || res.Left != shrink.LeftRunning || !res.Compacted || res.Freed != 5 {
		t.Fatalf("events %+v, result %+v", seen, res)
	}
	// Each item on its own flag: an id with a comma reaches the other side whole.
	if !slices.Contains(a.asked, "backend shrink --disk Ubuntu --clean Ubuntu=apt --clean Ubuntu=pip:/home/a,b/.cache/pip") {
		t.Fatalf("asked %q", a.asked)
	}
}

func TestShrinkWithoutAResultSaysSo(t *testing.T) {
	a := thisBuild()
	b, err := Open(context.Background(), a.run, "Ubuntu", "v9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	d := disks.Disk{Name: "Ubuntu", Distro: "Ubuntu", Running: true}
	// Refused before the run: the program's words, nothing touched.
	a.err["backend shrink --disk Ubuntu"] = errors.New(`no disk named "Ubuntu"`)
	res := b.Shrink(context.Background(), d, shrink.Options{}, nil)
	if res.FailedAt != shrink.StepCheck || res.Left != shrink.LeftRunning || !strings.Contains(res.Err, "no disk named") {
		t.Fatalf("%+v", res)
	}
	// Ended mid-run: what it left is not known here, so it says the worst
	// and the journal on the other side knows the rest.
	a.err["backend shrink --disk Ubuntu"] = errors.New("exit status 1")
	a.out["backend shrink --disk Ubuntu"] = `{"event":{"step":"compact","status":"running"}}` + "\n"
	res = b.Shrink(context.Background(), d, shrink.Options{}, nil)
	if res.FailedAt != shrink.StepCompact || res.Left != shrink.LeftDown {
		t.Fatalf("%+v", res)
	}
}

func TestReopenPassesTheHandoffAfterDashes(t *testing.T) {
	a := thisBuild()
	b, err := Open(context.Background(), a.run, "Ubuntu", "v9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Reopen([]string{"--reopened", "--run", "Ubuntu"}); err != nil {
		t.Fatal(err)
	}
	if last := a.asked[len(a.asked)-1]; last != "backend reopen -- --reopened --run Ubuntu" {
		t.Fatalf("asked %q", last)
	}
}

func TestRecoverTellsAStartThatFailedApart(t *testing.T) {
	a := thisBuild()
	b, err := Open(context.Background(), a.run, "Ubuntu", "v9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, c := range []struct {
		answer, did, err string
		still            bool
	}{
		// What a recovery did, said as the Windows side says it.
		{`{"schema":1,"did":"Ubuntu: running, 2 containers started again"}`, "Ubuntu: running, 2 containers started again", "", false},
		// A start that failed: the run stays cut off, and F may forget it.
		{`{"schema":1,"error":"the root filesystem stays read-only","still_cut_off":true}`, "", "the root filesystem stays read-only", true},
		// Any other failure is a plain one: no forgetting is offered.
		{`{"schema":1,"error":"WSL is still compacting Ubuntu; try again when it is done"}`, "", "WSL is still compacting Ubuntu; try again when it is done", false},
	} {
		a.out["backend recover"] = c.answer
		did, err := b.Recover(ctx)
		var still app.StillCutOff
		if did != c.did || errors.As(err, &still) != c.still || (err == nil) != (c.err == "") || err != nil && err.Error() != c.err {
			t.Errorf("%s: %q, %v", c.answer, did, err)
		}
	}
	// Forget names the run asked about, each value in one argument with
	// its flag, so an empty one survives; and says its failure.
	run := journal.Entry{PID: 7, Started: time.Date(2026, 11, 10, 9, 0, 0, 5, time.UTC), Disk: "Docker Desktop data"}
	line := "backend forget --pid=7 --started=2026-11-10T09:00:00.000000005Z --disk=Docker Desktop data"
	a.err[line] = errors.New("Ubuntu is being shrunk by another wsl-shrinker; let it finish")
	if err := b.Forget(ctx, run); err == nil || !strings.Contains(err.Error(), "let it finish") {
		t.Fatalf("forget: %v", err)
	}
	if !slices.Contains(a.asked, line) {
		t.Fatalf("asked %q", a.asked)
	}
	if err := b.Forget(ctx, journal.Entry{}); err != nil || !slices.Contains(a.asked, "backend forget --pid=0 --started=0001-01-01T00:00:00Z --disk=") {
		t.Fatalf("forget of an unknown run: %v, asked %q", err, a.asked)
	}
}
