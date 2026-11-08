package shrink

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kanggu12/wsl-shrinker/internal/engine/disks"
	"github.com/kanggu12/wsl-shrinker/internal/engine/survey"
	"github.com/kanggu12/wsl-shrinker/internal/machine/vhdx"
	"github.com/kanggu12/wsl-shrinker/internal/testutil/vhdxtest"
)

// lastEvent returns the last event of a step.
func lastEvent(r Result, s Step) (Event, bool) {
	for i := len(r.Events) - 1; i >= 0; i-- {
		if r.Events[i].Step == s {
			return r.Events[i], true
		}
	}
	return Event{}, false
}

func TestRunReadsWhetherTheDistributionRunsNow(t *testing.T) {
	// Listed while stopped, started since (a terminal opened): the run sees
	// it running, stops its containers the way they expect, and starts it
	// again, rather than let the compaction stop it cold and leave it so.
	w := newWorld(t)
	w.fake.List[0].Running = false
	d := w.disk()
	w.fake.List[0].Running = true
	r := Run(context.Background(), w.env, d, Options{}, nil)
	if r.Err != "" {
		t.Fatalf("%s %v", r.Err, statuses(r))
	}
	if s := statuses(r); s[StepStop] != Done || s[StepRestart] != Done || !w.fake.List[0].Running {
		t.Fatalf("%v, running %v", s, w.fake.List[0].Running)
	}
	// And the other way: listed running, stopped since: nothing to stop.
	w = newWorld(t)
	d = w.disk()
	w.fake.List[0].Running = false
	r = Run(context.Background(), w.env, d, Options{}, nil)
	if s := statuses(r); r.Err != "" || s[StepStop] != Skipped || s[StepRestart] != Skipped {
		t.Fatalf("%s %v", r.Err, s)
	}
}

func TestUnknownRunningStateStopsAtCheck(t *testing.T) {
	// WSL does not say which distributions run: nothing is changed, rather
	// than guess "stopped".
	w := newWorld(t)
	d := w.disk()
	w.fake.DistrosErr = errors.New("wsl did not answer")
	r := Run(context.Background(), w.env, d, Options{}, nil)
	if statuses(r)[StepCheck] != Failed || !strings.Contains(r.Err, "did not answer") || len(w.fake.CallList()) != 0 {
		t.Fatalf("%s %v %v", r.Err, statuses(r), w.fake.CallList())
	}
	// A distribution gone since the listing stops there too.
	w = newWorld(t)
	d = w.disk()
	w.fake.List = nil
	if r := Run(context.Background(), w.env, d, Options{}, nil); statuses(r)[StepCheck] != Failed || !strings.Contains(r.Err, "no such distribution") || len(w.fake.CallList()) != 0 {
		t.Fatalf("%s %v", r.Err, w.fake.CallList())
	}
}

func TestStopFailureStartsContainersAgain(t *testing.T) {
	// The stop ran out of time during `docker stop`: the containers it
	// named are started again, as after any failure past this point.
	w := newWorld(t)
	inner := w.fake.Script
	var scripts []string
	w.fake.Script = func(a, b, s string) (string, error) {
		scripts = append(scripts, s)
		if strings.Contains(s, "pre-stop.d") {
			return "", errors.New("context deadline exceeded")
		}
		return inner(a, b, s)
	}
	r := Run(context.Background(), w.env, w.disk(), Options{}, nil)
	if statuses(r)[StepStop] != Failed {
		t.Fatalf("%v", statuses(r))
	}
	started := false
	for _, s := range scripts {
		started = started || strings.Contains(s, "docker start") && strings.Contains(s, "0123456789ab")
	}
	if !started {
		t.Fatalf("containers left stopped; scripts %q", scripts)
	}
	if e, ok := lastEvent(r, StepRestart); !ok || e.Status == Failed {
		t.Fatalf("restart: %+v", e)
	}
	// The distribution never stopped, and its terminals with it: it keeps
	// running as before, so no note on WSL stopping it is due.
	if r.ContainersStarted == 0 || r.DistroStarted || Idle(r.DistroStarted, r.ContainersStarted) {
		t.Fatalf("started %d containers, distribution started %v: the note would be said", r.ContainersStarted, r.DistroStarted)
	}
}

func TestVerifyThatCannotOpenTheFileStartsTheDistributionAgain(t *testing.T) {
	// The file cannot be opened after compacting (another program has it a
	// moment): nothing says the disk is damaged, so the distribution and
	// its containers come back, and the run still reports the failure.
	w := newWorld(t)
	d := w.disk()
	open := w.env.Disks.Open
	compacted := false
	w.fake.OnCompact = func(string) error { compacted, w.pad = true, 0; return nil }
	w.env.Disks.Open = func(p string) (disks.ReaderAtCloser, int64, error) {
		if compacted {
			return nil, 0, errors.New("the process cannot access the file")
		}
		return open(p)
	}
	r := Run(context.Background(), w.env, d, Options{}, nil)
	if statuses(r)[StepVerify] != Failed || !w.fake.List[0].Running {
		t.Fatalf("%s %v, running %v", r.Err, statuses(r), w.fake.List[0].Running)
	}
	if e, _ := lastEvent(r, StepRestart); e.Status != Done || e.Count != 2 {
		t.Fatalf("restart: %+v", e)
	}
}

func TestSuspectDiskStaysStoppedAndSaysSo(t *testing.T) {
	// A file that no longer reads as a VHDX after compacting: starting the
	// distribution on it could make things worse. It stays stopped, and the
	// restart step says so instead of staying silent.
	w := newWorld(t)
	d := w.disk()
	w.fake.OnCompact = func(string) error {
		f, err := os.OpenFile(w.path, os.O_RDWR, 0)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = f.WriteAt([]byte("XXXXXXXX"), 0)
		w.pad = 0
		return err
	}
	r := Run(context.Background(), w.env, d, Options{}, nil)
	if statuses(r)[StepVerify] != Failed || w.fake.List[0].Running {
		t.Fatalf("%s %v, running %v", r.Err, statuses(r), w.fake.List[0].Running)
	}
	if e, ok := lastEvent(r, StepRestart); !ok || e.Status != Skipped || e.Detail == "" {
		t.Fatalf("restart: %+v %v", e, ok)
	}
}

func TestDeletionsAfterListingAreNotDataLoss(t *testing.T) {
	// The disk was listed with a 200 MiB file; the person deleted it before
	// shrinking. What is in use is measured again right before the
	// compaction, so the deletion is not taken for data the compaction lost.
	blocks := ext4Blocks(t)
	w := newWorld(t)
	vhdxtest.Write(t, w.path, vhdxtest.Spec{VirtualSize: 256 << 20, Blocks: blocks})
	d := w.disk()
	// The deletion: the file's bits go from the block bitmaps.
	freed := map[int64][]byte{}
	for i, b := range blocks {
		freed[i] = b
	}
	first := append([]byte(nil), blocks[0]...)
	used := d.Estimate.Used
	clearBigFile(t, first)
	freed[0] = first
	vhdxtest.Write(t, w.path, vhdxtest.Spec{VirtualSize: 256 << 20, Blocks: freed})
	r := Run(context.Background(), w.env, d, Options{}, nil)
	if r.Err != "" {
		t.Fatalf("listed with %d in use: %s %v", used, r.Err, statuses(r))
	}
}

// clearBigFile clears, in group 0's block bitmap, the bits of every block
// past the first 32 MiB: where the 200 MiB file of ext4Blocks lies, and no
// metadata.
func clearBigFile(t *testing.T, first []byte) {
	t.Helper()
	at := bitmapBlock(t, first) * 4096
	// 4 KiB blocks: bit n is block n, so blocks 8192 to 32767 are bytes
	// 1024 to 4095 of the bitmap.
	clear(first[at+1024 : at+4096])
}

func TestFilesystemErrorsBeforeTheRunStopAtCheck(t *testing.T) {
	// The kernel had recorded filesystem errors before the run: the check
	// refuses (e2fsck first), rather than compact and then blame the
	// compaction for errors that were already there.
	blocks := ext4Blocks(t)
	first := append([]byte(nil), blocks[0]...)
	first[1024+0x3A] |= 2 // s_state: errors detected
	blocks[0] = first
	w := newWorld(t)
	vhdxtest.Write(t, w.path, vhdxtest.Spec{VirtualSize: 256 << 20, Blocks: blocks})
	r := Run(context.Background(), w.env, w.disk(), Options{}, nil)
	if statuses(r)[StepCheck] != Failed || !strings.Contains(r.Err, "e2fsck") {
		t.Fatalf("%s %v", r.Err, statuses(r))
	}
	for _, c := range w.fake.CallList() {
		if !strings.HasPrefix(c, "run:") {
			t.Fatalf("changed something: %v", w.fake.CallList())
		}
	}
}

func TestCleanSkippedOnAStoppedDistributionSaysSo(t *testing.T) {
	// Items were chosen, but the distribution is not running: the clean step
	// says why it removed nothing, and how many items that was.
	w := newWorld(t)
	w.fake.List[0].Running = false
	items := []survey.Item{{ID: "apt", Command: "apt-get clean", Bytes: 1}, {ID: "journal", Command: "true", Bytes: 1}}
	r := Run(context.Background(), w.env, w.disk(), Options{Clean: items}, nil)
	if e, _ := lastEvent(r, StepClean); e.Status != Skipped || e.Detail != "not running" || e.Count != 2 {
		t.Fatalf("clean: %+v", e)
	}
}

func TestContainersAreCountedOneByOne(t *testing.T) {
	// One of two containers does not start again: the count says one, not
	// both.
	w := newWorld(t)
	inner := w.fake.Script
	w.fake.Script = func(a, b, s string) (string, error) {
		if strings.Contains(s, "docker start") {
			return "started|0123456789ab\nfailed|fedcba987654\n", nil
		}
		return inner(a, b, s)
	}
	r := Run(context.Background(), w.env, w.disk(), Options{}, nil)
	e, _ := lastEvent(r, StepRestart)
	if e.Status != Warned || e.NotRestarted != 1 || e.Count != 2 {
		t.Fatalf("restart: %+v", e)
	}
}

// dropZeroBlocks rewrites the VHDX at path without its stored blocks that
// hold only zeros: what Windows' compaction of a file nobody has attached
// does (measured on Windows; see winapi's tests).
func dropZeroBlocks(t *testing.T, path string, size int64) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	vd, err := vhdx.Read(f, size)
	if err != nil {
		f.Close()
		t.Fatal(err)
	}
	kept := map[int64][]byte{}
	zero := make([]byte, vd.BlockSize)
	for i, b := range vd.Blocks {
		if !b.Present() {
			continue
		}
		buf := make([]byte, vd.BlockSize)
		if _, err := vd.Contents().ReadAt(buf, int64(i)*int64(vd.BlockSize)); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(buf, zero) {
			kept[int64(i)] = buf
		}
	}
	vs := vd.VirtualSize
	f.Close()
	vhdxtest.Write(t, path, vhdxtest.Spec{VirtualSize: vs, Blocks: kept})
}

func TestWindowsCompactionOfZeroBlocksIsNotDataLoss(t *testing.T) {
	// Docker Desktop's disk, every block stored, zeros included (the guest
	// wrote them: a journal, a file of zeros). Windows' compaction drops the
	// stored blocks of zeros, those the filesystem uses too; they still
	// read as zeros, so the data is all there and the run passes.
	blocks := ext4Blocks(t)
	for i := int64(0); i < 256; i++ {
		if _, ok := blocks[i]; !ok {
			blocks[i] = []byte{}
		}
	}
	w := newWorld(t)
	size := vhdxtest.Write(t, w.path, vhdxtest.Spec{VirtualSize: 256 << 20, Blocks: blocks})
	d := w.disk()
	d.Distro, d.Kind, d.Running = "", disks.KindDocker, false
	disks.Inspect(w.env.Disks, &d)
	w.env.CompactFile = func(p string) error { dropZeroBlocks(t, p, size); w.pad = 0; return nil }
	r := Run(context.Background(), w.env, d, Options{}, nil)
	if r.Err != "" {
		t.Fatalf("%s %v", r.Err, statuses(r))
	}
	// The estimate kept the zeros in use, so it promised no more than came.
	if r.After == nil || r.After.FileSize > d.Estimate.After {
		t.Fatalf("estimated %d after, got %+v", d.Estimate.After, r.After)
	}
}

func TestJSONKeys(t *testing.T) {
	// Every key is snake_case, durations are seconds under one name, and
	// byte counts say so where a name alone would be read two ways.
	e := Event{Step: StepCompact, Status: Done, Detail: "d", Took: 1500 * time.Millisecond, At: time.Unix(0, 0).UTC()}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"step":"compact","status":"done","detail":"d","bytes":0,"count":0,"hooks":0,"took_seconds":1.5,"second_start":false,"not_restarted":0,"at":"1970-01-01T00:00:00Z"}`
	if string(b) != want {
		t.Fatalf("event\ngot  %s\nwant %s", b, want)
	}
	b, err = json.Marshal(Result{Disk: "d", Took: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	want = `{"disk":"d","freed_bytes":0,"events":null,"compacted":false,"left":"","took_seconds":2}`
	if string(b) != want {
		t.Fatalf("result\ngot  %s\nwant %s", b, want)
	}
	// And it reads back the same.
	var back Result
	if err := json.Unmarshal(b, &back); err != nil || back.Took != 2*time.Second {
		t.Fatalf("%+v %v", back, err)
	}
}

func TestABugAsTheLastEventGoesOutKeepsTheOutcome(t *testing.T) {
	// The screen's code fails on the run's last event: the distribution
	// was started again, and the result says so, with the bug beside it.
	w := newWorld(t)
	r := Run(context.Background(), w.env, w.disk(), Options{}, func(e Event) {
		if e.Step == StepRestart && e.Status == Done {
			panic("the screen's bug")
		}
	})
	if r.Left != LeftRunning || !strings.Contains(r.Err, "internal error: the screen's bug") {
		t.Fatalf("left %q, err %q", r.Left, r.Err)
	}
}
