package shrink

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kanggu12/wsl-shrinker/internal/engine/disks"
	"github.com/kanggu12/wsl-shrinker/internal/engine/survey"
	"github.com/kanggu12/wsl-shrinker/internal/machine/winapi"
	"github.com/kanggu12/wsl-shrinker/internal/machine/wsl"
	"github.com/kanggu12/wsl-shrinker/internal/testutil/vhdxtest"
)

// world is a distribution on a VHDX file, a fake WSL, and an Env over both.
type world struct {
	t     *testing.T
	path  string
	fake  *wsl.Fake
	env   Env
	mount []string // what readOnly answers, one per call; "rw" after
	pad   int64    // bytes the host file is larger than its blocks
}

// newWorld sets up a running distribution whose file holds three blocks
// plus 30 MiB of space a compaction gives back.
func newWorld(t *testing.T) *world {
	w := &world{t: t, path: filepath.Join(t.TempDir(), "ext4.vhdx"), pad: 30 << 20}
	vhdxtest.Write(t, w.path, vhdxtest.Spec{VirtualSize: 16 << 20, Blocks: map[int64][]byte{0: {1}, 2: {1}, 5: {1}}})
	w.fake = &wsl.Fake{List: []wsl.Distro{{Name: "Dev", VHD: w.path, Version: 2, Running: true}}}
	w.fake.Script = func(_, _, s string) (string, error) {
		switch {
		case strings.Contains(s, "docker ps -q"):
			// The containers the stop step names before it stops them.
			return "running|0123456789ab fedcba987654 \n", nil
		case strings.Contains(s, "pre-stop.d"):
			// The stop script: one hook ran.
			return "hook|ok|/etc/wsl-shrinker/pre-stop.d/save\n", nil
		case strings.Contains(s, "/proc/mounts"):
			if len(w.mount) > 0 {
				m := w.mount[0]
				w.mount = w.mount[1:]
				return m + "\n", nil
			}
			return "rw,relatime,discard\n", nil
		case strings.Contains(s, "docker start"):
			// One line per container, as the start script prints them.
			return "started|0123456789ab\nstarted|fedcba987654\n", nil
		}
		return "", nil
	}
	// Compaction drops the padding, as Windows would.
	w.fake.OnCompact = func(string) error { w.pad = 0; return nil }
	w.env = Env{
		Disks: disks.Env{
			WSL: w.fake,
			Stat: func(p string) (winapi.FileInfo, error) {
				fi, err := winapi.Stat(p)
				fi.OnDisk += w.pad
				return fi, err
			},
			Open: disks.OpenFile,
		},
		CompactFile: func(string) error { w.pad = 0; return nil },
		CanCompact:  true,
		Sleep:       func(time.Duration) {},
	}
	return w
}

// disk finds the distribution's disk as the screen would.
func (w *world) disk() disks.Disk {
	ds, err := disks.Find(context.Background(), w.env.Disks)
	if err != nil || len(ds) != 1 {
		w.t.Fatalf("find: %v %v", ds, err)
	}
	return ds[0]
}

// statuses returns the last status of every step.
func statuses(r Result) map[Step]Status {
	m := map[Step]Status{}
	for _, e := range r.Events {
		m[e.Step] = e.Status
	}
	return m
}

func TestRunningDistribution(t *testing.T) {
	w := newWorld(t)
	var seen []Event
	r := Run(context.Background(), w.env, w.disk(), Options{}, func(e Event) { seen = append(seen, e) })
	if r.Err != "" {
		t.Fatalf("failed: %s\n%+v", r.Err, r.Events)
	}
	// Every step reports, in order, and the ones that apply finish.
	want := map[Step]Status{StepCheck: Done, StepClean: Skipped, StepStop: Done, StepCompact: Done, StepVerify: Done, StepRestart: Done}
	if got := statuses(r); !mapsEqual(got, want) {
		t.Fatalf("statuses %v", got)
	}
	if len(seen) != len(r.Events) {
		t.Fatal("emit missed events")
	}
	// The padding came back; WSL did the compaction, and the distribution and
	// its two containers run again.
	if r.Freed != 30<<20 {
		t.Fatalf("freed %d", r.Freed)
	}
	calls := w.fake.CallList()
	if !slices.Contains(calls, "compact:Dev") || calls[len(calls)-1] != "run:Dev" || !slices.Contains(calls, "start:Dev") {
		t.Fatalf("calls %v", calls)
	}
	if last := r.Events[len(r.Events)-1]; !strings.Contains(last.Detail, "2 containers started again") {
		t.Fatalf("restart: %+v", last)
	}
	// The result counts them, for the note the fronts put under the run.
	if r.Left != LeftRunning || r.ContainersStarted != 2 || r.ContainersNotStarted != 0 {
		t.Fatalf("left %q, started %d, not started %d", r.Left, r.ContainersStarted, r.ContainersNotStarted)
	}
	// The compaction stopped the distribution: the run started it again.
	if !r.DistroStarted {
		t.Fatal("the distribution the compaction stopped is not counted as started again")
	}
}

func TestTheNoteSaysWSLStopsItAgainWhenNothingRunsInIt(t *testing.T) {
	// What the fronts say under a run that started a distribution and its
	// containers again: the distribution by name, when WSL stops it, the
	// setting, and what keeps the containers running.
	n := IdleNote("Dev")
	for _, want := range []string{"with no terminal open in Dev", "WSL stops it in about 15 seconds", "instanceIdleTimeout in .wslconfig", "its containers with it", "keep one open to keep them running"} {
		if !strings.Contains(n, want) {
			t.Errorf("no %q in %q", want, n)
		}
	}
	// Said only for a distribution that had stopped, with containers started.
	for _, c := range []struct {
		stopped bool
		started int
		want    bool
	}{{true, 2, true}, {true, 0, false}, {false, 2, false}} {
		if got := Idle(c.stopped, c.started); got != c.want {
			t.Errorf("Idle(%v, %d) = %v", c.stopped, c.started, got)
		}
	}
}

func TestCleanRunsChosenItemsOnly(t *testing.T) {
	w := newWorld(t)
	var scripts []string
	inner := w.fake.Script
	w.fake.Script = func(d, u, s string) (string, error) {
		scripts = append(scripts, s)
		return inner(d, u, s)
	}
	items := []survey.Item{{ID: "apt", What: "apt", Bytes: 1 << 20, Command: "apt-get clean"}}
	r := Run(context.Background(), w.env, w.disk(), Options{Clean: items}, nil)
	if r.Err != "" || statuses(r)[StepClean] != Done {
		t.Fatalf("%s %v", r.Err, statuses(r))
	}
	// The clean script is the first script run, before the stop.
	if !strings.Contains(scripts[0], "apt-get clean") || strings.Contains(scripts[0], "docker ps") {
		t.Fatalf("first script:\n%s", scripts[0])
	}
}

func TestReadOnlyAfterCompactGetsASecondStart(t *testing.T) {
	w := newWorld(t)
	w.mount = []string{"ro,relatime"}
	r := Run(context.Background(), w.env, w.disk(), Options{}, nil)
	if r.Err != "" || statuses(r)[StepRestart] != Warned {
		t.Fatalf("%s %v", r.Err, statuses(r))
	}
	calls := strings.Join(w.fake.CallList(), " ")
	if !strings.Contains(calls, "start:Dev run:Dev terminate:Dev start:Dev") {
		t.Fatalf("calls %s", calls)
	}
	// Read-only twice: the run fails and says how to clear it.
	w = newWorld(t)
	w.mount = []string{"ro", "ro"}
	r = Run(context.Background(), w.env, w.disk(), Options{}, nil)
	if !strings.Contains(r.Err, "wsl --shutdown") {
		t.Fatalf("err %q", r.Err)
	}
}

func TestFailedCompactBringsTheDistributionBack(t *testing.T) {
	w := newWorld(t)
	w.fake.Fail = map[string]error{"compact:Dev": errors.New("Wsl/Service/E_FAIL")}
	r := Run(context.Background(), w.env, w.disk(), Options{}, nil)
	if statuses(r)[StepCompact] != Failed || !strings.Contains(r.Err, "E_FAIL") {
		t.Fatalf("%s %v", r.Err, statuses(r))
	}
	if !w.fake.List[0].Running {
		t.Fatal("left stopped after a failed compaction")
	}
}

// fileNow makes the disk's file read, from now on, as change has it: the
// state a run's check reads afresh, whatever the listing said.
func fileNow(w *world, change func(*winapi.FileInfo) error) {
	stat := w.env.Disks.Stat
	w.env.Disks.Stat = func(p string) (winapi.FileInfo, error) {
		fi, err := stat(p)
		if err != nil {
			return fi, err
		}
		return fi, change(&fi)
	}
}

func TestChecksStopBeforeAnythingChanges(t *testing.T) {
	// Each the way it is found when the run starts, though the disks were
	// listed fine: a program took the file since, or it was encrypted.
	cases := map[string]func(*world, *disks.Disk){
		"old WSL": func(w *world, d *disks.Disk) { w.env.CanCompact = false },
		"encrypted": func(w *world, d *disks.Disk) {
			fileNow(w, func(fi *winapi.FileInfo) error { fi.Encrypted = true; return nil })
		},
		"unreadable": func(w *world, d *disks.Disk) {
			fileNow(w, func(*winapi.FileInfo) error { return errors.New("boom") })
		},
		"docker disk held": func(w *world, d *disks.Disk) {
			d.Distro, d.Kind = "", disks.KindDocker
			fileNow(w, func(fi *winapi.FileInfo) error { fi.Held = true; return nil })
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			d := w.disk()
			change(w, &d)
			r := Run(context.Background(), w.env, d, Options{}, nil)
			if statuses(r)[StepCheck] != Failed || len(r.Events) != 2 {
				t.Fatalf("%v", r.Events)
			}
			// Nothing was asked of WSL beyond reading.
			for _, c := range w.fake.CallList() {
				if !strings.HasPrefix(c, "run:") {
					t.Fatalf("changed something: %v", w.fake.CallList())
				}
			}
		})
	}
}

func TestUnregisteredFile(t *testing.T) {
	w := newWorld(t)
	d := w.disk()
	d.Distro, d.Kind, d.Running = "", disks.KindDocker, false
	compacted := false
	w.env.CompactFile = func(p string) error { compacted = p == w.path; w.pad = 0; return nil }
	r := Run(context.Background(), w.env, d, Options{}, nil)
	if r.Err != "" || !compacted || r.Freed != 30<<20 {
		t.Fatalf("%s compacted=%v freed=%d", r.Err, compacted, r.Freed)
	}
	if s := statuses(r); s[StepStop] != Skipped || s[StepRestart] != Skipped {
		t.Fatalf("%v", s)
	}
	if len(w.fake.CallList()) != 0 {
		t.Fatalf("WSL was asked: %v", w.fake.CallList())
	}
}

func TestSafeIDs(t *testing.T) {
	got := safeIDs([]string{"0123456789ab", "x;rm -rf /", "ABCDEF012345", "abc"})
	if len(got) != 1 || got[0] != "0123456789ab" {
		t.Fatalf("%v", got)
	}
	// What the fronts tell a person to type is what a run would start: the
	// same ids, and nothing a shell would read as more than an id.
	if got := Startable([]string{"0123456789ab", "$(reboot)", "ba9876543210"}); strings.Join(got, " ") != "0123456789ab ba9876543210" {
		t.Fatalf("startable %v", got)
	}
}

// mapsEqual compares two status maps.
func mapsEqual(a, b map[Step]Status) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// ext4Blocks makes a 256 MiB ext4 image with a 200 MiB file in it and returns
// its non-zero 1 MiB blocks, ready to store in a VHDX.
func ext4Blocks(t *testing.T) map[int64][]byte {
	t.Helper()
	for _, tool := range []string{"mkfs.ext4", "debugfs"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed", tool)
		}
	}
	dir := t.TempDir()
	img, data := filepath.Join(dir, "fs.img"), filepath.Join(dir, "data")
	if err := os.WriteFile(data, bytes.Repeat([]byte{7}, 200<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range [][]string{{"truncate", "-s", "256M", img}, {"mkfs.ext4", "-q", "-F", img}, {"debugfs", "-w", "-R", "write " + data + " big", img}} {
		if out, err := exec.Command(c[0], c[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	raw, err := os.ReadFile(img)
	if err != nil {
		t.Fatal(err)
	}
	blocks := map[int64][]byte{}
	for i := int64(0); i < 256; i++ {
		if !bytes.Equal(raw[i<<20:(i+1)<<20], make([]byte, 1<<20)) {
			blocks[i] = raw[i<<20 : (i+1)<<20]
		}
	}
	return blocks
}

func TestVerifyCatchesLostData(t *testing.T) {
	blocks := ext4Blocks(t)
	w := newWorld(t)
	vhdxtest.Write(t, w.path, vhdxtest.Spec{VirtualSize: 256 << 20, Blocks: blocks})
	// A healthy compaction keeps every block: the run passes.
	if r := Run(context.Background(), w.env, w.disk(), Options{}, nil); r.Err != "" {
		t.Fatalf("healthy run failed: %s", r.Err)
	}
	// A compaction that loses the file's data: the filesystem's bitmaps go
	// with the dropped blocks, and the run must not call that a success.
	w = newWorld(t)
	vhdxtest.Write(t, w.path, vhdxtest.Spec{VirtualSize: 256 << 20, Blocks: blocks})
	d := w.disk()
	w.fake.OnCompact = func(string) error {
		lost := map[int64][]byte{}
		for i, b := range blocks {
			// Keep the first blocks (superblock, descriptors), clear the
			// bitmaps' worth of the data by zeroing every bitmap block.
			lost[i] = b
		}
		// Rewrite both groups' block bitmaps (flex_bg keeps them side by
		// side in group 0) as empty: 200 MiB "gone".
		fs := lost[0]
		bm := make([]byte, len(fs))
		copy(bm, fs)
		bitmapAt := bitmapBlock(t, fs)
		clear(bm[bitmapAt*4096 : bitmapAt*4096+2*4096])
		lost[0] = bm
		vhdxtest.Write(t, w.path, vhdxtest.Spec{VirtualSize: 256 << 20, Blocks: lost})
		w.pad = 0
		return nil
	}
	r := Run(context.Background(), w.env, d, Options{}, nil)
	if statuses(r)[StepVerify] != Failed || !strings.Contains(r.Err, "more went than was cleaned") {
		t.Fatalf("lost data passed: %q %v", r.Err, statuses(r))
	}
}

// bitmapBlock reads group 0's block bitmap location from the first MiB of
// a 4 KiB-block ext4 image.
func bitmapBlock(t *testing.T, first []byte) int {
	t.Helper()
	// The descriptor table is in block 1; the bitmap's block number is its
	// first field.
	return int(binary.LittleEndian.Uint32(first[4096:]))
}

func TestVerifyCatchesDroppedDataBlocks(t *testing.T) {
	// A compaction that drops the blocks holding a file's data but keeps the
	// filesystem's own blocks: the counts still match, the data is gone.
	blocks := ext4Blocks(t)
	w := newWorld(t)
	vhdxtest.Write(t, w.path, vhdxtest.Spec{VirtualSize: 256 << 20, Blocks: blocks})
	d := w.disk()
	sevens := bytes.Repeat([]byte{7}, 1<<20)
	w.fake.OnCompact = func(string) error {
		kept := map[int64][]byte{}
		for i, b := range blocks {
			if !bytes.Equal(b, sevens) {
				kept[i] = b
			}
		}
		vhdxtest.Write(t, w.path, vhdxtest.Spec{VirtualSize: 256 << 20, Blocks: kept})
		w.pad = 0
		return nil
	}
	r := Run(context.Background(), w.env, d, Options{}, nil)
	if statuses(r)[StepVerify] != Failed || !strings.Contains(r.Err, "no longer stored") {
		t.Fatalf("dropped data passed: %q %v", r.Err, statuses(r))
	}
}

func TestFailedCompactStartsContainersAgain(t *testing.T) {
	w := newWorld(t)
	var scripts []string
	inner := w.fake.Script
	w.fake.Script = func(a, b, s string) (string, error) {
		scripts = append(scripts, s)
		return inner(a, b, s)
	}
	w.fake.Fail = map[string]error{"compact:Dev": errors.New("boom")}
	Run(context.Background(), w.env, w.disk(), Options{}, nil)
	if !slices.ContainsFunc(scripts, func(s string) bool {
		return strings.Contains(s, "docker start") && strings.Contains(s, "0123456789ab fedcba987654")
	}) {
		t.Fatalf("containers left stopped; scripts %q", scripts)
	}
}

func TestDockerStartWaitsForDocker(t *testing.T) {
	w := newWorld(t)
	inner := w.fake.Script
	up := 0
	w.fake.Script = func(a, b, s string) (string, error) {
		// Docker answers on the third ask, as it does a few seconds after boot.
		if strings.Contains(s, "docker info >/dev/null 2>&1 && echo up") {
			up++
			if up < 3 {
				return "", nil
			}
			return "up\n", nil
		}
		return inner(a, b, s)
	}
	r := Run(context.Background(), w.env, w.disk(), Options{}, nil)
	if up != 3 || !strings.Contains(r.Events[len(r.Events)-1].Detail, "2 containers started again") {
		t.Fatalf("asked %d times; %+v", up, r.Events[len(r.Events)-1])
	}
}

func TestCancelAfterStopStillFinishes(t *testing.T) {
	// Ctrl+C while Windows compacts: the run still verifies and starts the
	// distribution and its containers again.
	w := newWorld(t)
	ctx, cancel := context.WithCancel(context.Background())
	w.fake.OnCompact = func(string) error { cancel(); w.pad = 0; return nil }
	r := Run(ctx, w.env, w.disk(), Options{}, nil)
	if r.Err != "" || statuses(r)[StepRestart] != Done || !w.fake.List[0].Running {
		t.Fatalf("%q %v", r.Err, statuses(r))
	}
}

func TestVerifyAllowsOnlyWhatWasCleaned(t *testing.T) {
	// Items chosen in a distribution that was not running are not removed:
	// the clean step skips them, and a compaction that loses data is not
	// excused by their size.
	blocks := ext4Blocks(t)
	w := newWorld(t)
	vhdxtest.Write(t, w.path, vhdxtest.Spec{VirtualSize: 256 << 20, Blocks: blocks})
	w.fake.List[0].Running = false
	d := w.disk()
	w.fake.OnCompact = func(string) error {
		// Group 0's block bitmaps cleared: 200 MiB the filesystem no longer
		// counts, as in TestVerifyCatchesLostData.
		lost := map[int64][]byte{}
		maps.Copy(lost, blocks)
		bm := slices.Clone(blocks[0])
		at := bitmapBlock(t, blocks[0])
		clear(bm[at*4096 : at*4096+2*4096])
		lost[0] = bm
		vhdxtest.Write(t, w.path, vhdxtest.Spec{VirtualSize: 256 << 20, Blocks: lost})
		w.pad = 0
		return nil
	}
	items := []survey.Item{{ID: "cache", What: "cache", Bytes: 300 << 20, Command: "true"}}
	r := Run(context.Background(), w.env, d, Options{Clean: items}, nil)
	s := statuses(r)
	if s[StepClean] != Skipped || s[StepVerify] != Failed || !strings.Contains(r.Err, "more went than was cleaned") {
		t.Fatalf("lost data passed on what was never removed: %q %v", r.Err, s)
	}
}

func TestADistributionThatStopsDuringTheCleanIsLeftStopped(t *testing.T) {
	// It stopped on its own while the items were being removed: the run
	// stopped nothing, starts nothing, and says it is stopped.
	w := newWorld(t)
	inner := w.fake.Script
	w.fake.Script = func(a, b, s string) (string, error) {
		if strings.Contains(s, "apt-get clean") {
			w.fake.List[0].Running = false
			return "", fmt.Errorf("Dev is %w", wsl.ErrNotRunning)
		}
		return inner(a, b, s)
	}
	items := []survey.Item{{ID: "apt", What: "apt", Bytes: 1 << 20, Command: "apt-get clean"}}
	r := Run(context.Background(), w.env, w.disk(), Options{Clean: items}, nil)
	if r.FailedAt != StepClean || r.Left != LeftStopped {
		t.Fatalf("failed at %q, left %q: %s", r.FailedAt, r.Left, r.Err)
	}
	if calls := strings.Join(w.fake.CallList(), " "); strings.Contains(calls, "start:") || strings.Contains(calls, "terminate:") {
		t.Fatalf("calls %s", calls)
	}
}
