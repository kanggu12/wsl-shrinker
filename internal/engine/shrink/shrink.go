// Package shrink gives a disk's free space back to Windows, one disk at a
// time, in steps the screen can follow:
//
//  1. check   what could stop it before anything changes
//  2. clean   remove what the person chose inside the distribution
//  3. stop    stop containers and services the way they expect
//  4. compact trim and compact the disk file
//  5. verify  read the disk again: a clean filesystem, the same data in use
//  6. restart start the distribution again if it ran, writable, with the
//     containers that were running
//
// Nothing is removed that the person did not choose, and the disk file is
// changed only by Windows' own compaction (WSL's, or the virtual disk API
// for files WSL does not register).
package shrink

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kanggu12/wsl-shrinker/internal/core/estimate"
	"github.com/kanggu12/wsl-shrinker/internal/core/units"
	"github.com/kanggu12/wsl-shrinker/internal/engine/disks"
	"github.com/kanggu12/wsl-shrinker/internal/engine/survey"
	"github.com/kanggu12/wsl-shrinker/internal/machine/ext4"
	"github.com/kanggu12/wsl-shrinker/internal/machine/vhdx"
	"github.com/kanggu12/wsl-shrinker/internal/machine/winapi"
)

// Step names one stage, in the order they run.
type Step string

const (
	StepCheck   Step = "check"
	StepClean   Step = "clean"
	StepStop    Step = "stop"
	StepCompact Step = "compact"
	StepVerify  Step = "verify"
	StepRestart Step = "restart"
)

// Steps lists every step in order.
var Steps = []Step{StepCheck, StepClean, StepStop, StepCompact, StepVerify, StepRestart}

// Status is where a step stands.
type Status string

const (
	Running Status = "running"
	Done    Status = "done"
	Skipped Status = "skipped"
	Failed  Status = "failed"
	Warned  Status = "warned"
)

// Event reports a step's progress to whoever shows it.
type Event struct {
	Step   Step
	Status Status
	// Detail is one short line in English: what was found or done, or why
	// it failed. The screen builds its own words from the fields below.
	Detail string
	// Bytes, Count and Hooks are the step's numbers: space to free, freed or
	// cleaned; items cleaned or containers stopped and started; hooks run.
	Bytes int64
	Count int
	Hooks int
	// Took is how long the compaction took.
	Took time.Duration
	// SecondStart is set when the root came back writable only after a
	// second start; NotRestarted counts containers that did not start.
	SecondStart  bool
	NotRestarted int
	At           time.Time
}

// Options are the person's choices for one disk.
type Options struct {
	// Clean lists the survey items to remove before compacting.
	Clean []survey.Item
	// StayStopped leaves a running distribution stopped afterwards.
	StayStopped bool
}

// Env is everything the steps use. CompactFile and Stat reach Windows;
// tests give fakes.
type Env struct {
	Disks       disks.Env
	CompactFile func(path string) error
	// CanCompact says whether WSL offers `wsl --manage --compact`.
	CanCompact bool
	// Sleep waits between checks; tests make it instant.
	Sleep func(time.Duration)
}

// Result is how one disk's run went.
type Result struct {
	Disk   string            `json:"disk"`
	Before *estimate.Result  `json:"before,omitempty"`
	After  *estimate.Result  `json:"after,omitempty"`
	Freed  int64             `json:"freed"`
	Events []Event           `json:"events"`
	Err    string            `json:"error,omitempty"`
	File   *winapi.FileInfo  `json:"file_after,omitempty"`
	Took   time.Duration     `json:"took"`
	Notes  map[string]string `json:"notes,omitempty"`
}

// ErrCheck wraps a reason found before anything changed.
var ErrCheck = errors.New("not started")

// stopScript stops what runs in a distribution the way it expects: every
// container with its own stop timeout (Docker waits that long after SIGTERM),
// then the person's own pre-stop scripts, then a sync. It prints the ids of
// the containers it stopped, to start them again afterwards.
const stopScript = `ids=""
if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1 && ! docker info --format '{{.OperatingSystem}}' 2>/dev/null | grep -q 'Docker Desktop'; then
  ids=$(docker ps -q)
  [ -n "$ids" ] && docker stop $ids >/dev/null
fi
echo "stopped|$(echo $ids | tr '\n' ' ')"
if [ -d /etc/wsl-shrinker/pre-stop.d ]; then
  for f in /etc/wsl-shrinker/pre-stop.d/*; do
    [ -x "$f" ] || continue
    timeout 120 "$f" >/dev/null 2>&1 && echo "hook|ok|$f" || echo "hook|failed|$f"
  done
fi
sync
`

// Run shrinks one disk and reports each step through emit.
func Run(ctx context.Context, env Env, d disks.Disk, opt Options, emit func(Event)) (res Result) {
	start := time.Now()
	res = Result{Disk: d.Name, Before: d.Estimate, Notes: map[string]string{}}
	say := func(s Step, st Status, format string, a ...any) {
		e := Event{Step: s, Status: st, Detail: fmt.Sprintf(format, a...), At: time.Now()}
		res.Events = append(res.Events, e)
		if emit != nil {
			emit(e)
		}
	}
	sayE := func(e Event) {
		e.At = time.Now()
		res.Events = append(res.Events, e)
		if emit != nil {
			emit(e)
		}
	}
	fail := func(s Step, err error) Result {
		say(s, Failed, "%s", oneLine(err.Error()))
		res.Err = err.Error()
		res.Took = time.Since(start)
		return res
	}
	sleep := env.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	sys := env.Disks.WSL

	// 1. check: everything that would stop the run, before anything changes.
	say(StepCheck, Running, "")
	if err := check(env, d); err != nil {
		return fail(StepCheck, fmt.Errorf("%w: %w", ErrCheck, err))
	}
	sayE(Event{Step: StepCheck, Status: Done, Detail: checkDetail(d), Bytes: reclaimOf(d)})

	// 2. clean: only what was chosen, only in a running distribution.
	if len(opt.Clean) == 0 || d.Distro == "" || !d.Running {
		say(StepClean, Skipped, "nothing chosen")
	} else {
		say(StepClean, Running, "")
		var n int64
		for _, it := range opt.Clean {
			n += it.Bytes
		}
		if out, err := survey.Clean(ctx, sys, d.Distro, opt.Clean); err != nil {
			return fail(StepClean, err)
		} else if strings.Contains(out, "failed: ") {
			// One item failing does not stop the run; the rest was removed.
			sayE(Event{Step: StepClean, Status: Warned, Detail: fmt.Sprintf("%d items, some failed", len(opt.Clean)), Count: len(opt.Clean), Bytes: n})
		} else {
			sayE(Event{Step: StepClean, Status: Done, Detail: fmt.Sprintf("%d items, about %s", len(opt.Clean), units.Bytes(n)), Count: len(opt.Clean), Bytes: n})
		}
	}

	// 3. stop: the containers and the person's hooks, then the distribution.
	// From here on the run goes to its end even when the caller cancels:
	// a distribution stopped and not started again, or containers left
	// stopped, is worse than a run that takes another minute.
	// A run cancelled before this point has changed nothing but what was
	// cleaned: it stops here, rather than stop a distribution.
	if err := ctx.Err(); err != nil {
		return fail(StepStop, fmt.Errorf("cancelled before the distribution was stopped: %w", err))
	}
	ctx = context.WithoutCancel(ctx)
	var restartIDs []string
	switch {
	case d.Distro == "":
		say(StepStop, Skipped, "not a distribution")
	case !d.Running:
		say(StepStop, Skipped, "not running")
	default:
		say(StepStop, Running, "")
		out, err := sys.Run(within(ctx, stopLimit), d.Distro, "root", stopScript)
		if err != nil {
			return fail(StepStop, err)
		}
		hooks, failed := 0, 0
		for line := range strings.SplitSeq(out, "\n") {
			f := strings.Split(strings.TrimSpace(line), "|")
			switch {
			case f[0] == "stopped" && len(f) == 2:
				restartIDs = strings.Fields(f[1])
			case f[0] == "hook" && len(f) == 3:
				hooks++
				if f[1] == "failed" {
					failed++
				}
			}
		}
		res.Notes["containers"] = strings.Join(restartIDs, " ")
		detail := fmt.Sprintf("%d containers", len(restartIDs))
		if hooks > 0 {
			detail += fmt.Sprintf(", %d hooks", hooks)
		}
		e := Event{Step: StepStop, Status: Done, Detail: detail, Count: len(restartIDs), Hooks: hooks}
		if failed > 0 {
			e.Status, e.Detail = Warned, fmt.Sprintf("%s, %d failed", detail, failed)
		}
		sayE(e)
	}

	// 4. compact: WSL's own for a distribution, the virtual disk API for a
	// file WSL does not register. Which blocks hold data is read first: the
	// verify step checks every one the filesystem uses is still there.
	say(StepCompact, Running, "")
	stored := storedBlocks(env, d.Path)
	t0 := time.Now()
	var err error
	if d.Distro != "" {
		err = sys.Compact(within(ctx, compactLimit), d.Distro)
	} else {
		err = env.CompactFile(d.Path)
	}
	if err != nil {
		// The distribution may already be stopped: bring it back as it was,
		// with its containers, if it ran.
		if d.Running && d.Distro != "" && !opt.StayStopped {
			// Report the compaction's failure first, then how the start went,
			// so the screen shows both steps as they ended.
			res = fail(StepCompact, err)
			e, rerr := restart(ctx, env, d.Distro, restartIDs, sleep)
			if rerr != nil {
				e = Event{Step: StepRestart, Status: Failed, Detail: oneLine(rerr.Error())}
			}
			sayE(e)
			return res
		}
		return fail(StepCompact, err)
	}
	took := time.Since(t0).Round(100 * time.Millisecond)
	sayE(Event{Step: StepCompact, Status: Done, Detail: took.String(), Took: took})

	// 5. verify: read the disk again, now that Windows has let go of it.
	say(StepVerify, Running, "")
	after := d
	disks.Inspect(env.Disks, &after)
	if after.Err != "" || after.Estimate == nil {
		return fail(StepVerify, fmt.Errorf("the disk cannot be read after compacting: %s", after.Err))
	}
	res.After, res.File = after.Estimate, &after.File
	res.Freed = max(0, d.File.OnDisk-after.File.OnDisk)
	if msg := verify(env, d, after, opt, stored); msg != "" {
		return fail(StepVerify, errors.New(msg))
	}
	sayE(Event{Step: StepVerify, Status: Done, Detail: "freed " + units.Bytes(res.Freed), Bytes: res.Freed})

	// 6. restart: as it was, writable, with its containers.
	if !d.Running || d.Distro == "" || opt.StayStopped {
		say(StepRestart, Skipped, "was not running")
	} else {
		say(StepRestart, Running, "")
		e, err := restart(ctx, env, d.Distro, restartIDs, sleep)
		if err != nil {
			return fail(StepRestart, err)
		}
		sayE(e)
	}
	res.Took = time.Since(start)
	return res
}

// check finds what would stop a run before anything changes.
func check(env Env, d disks.Disk) error {
	switch {
	case d.Err != "":
		return fmt.Errorf("the disk cannot be read: %s", d.Err)
	case d.File.Encrypted:
		// WSL's compaction fails on encrypted files (0x80071772).
		return errors.New("the disk file is encrypted (EFS); Windows cannot compact it")
	case d.Distro != "" && !env.CanCompact:
		return errors.New("this WSL has no `wsl --manage --compact`; run `wsl --update`")
	case d.Distro == "" && d.File.Held:
		// Docker Desktop holds its data disk while it runs.
		return errors.New("the file is in use; quit the program that uses it (Docker Desktop) first")
	}
	return nil
}

// checkDetail sums up what the check saw.
func checkDetail(d disks.Disk) string {
	if d.Estimate == nil {
		return ""
	}
	return fmt.Sprintf("about %s to free", units.Bytes(d.Estimate.Reclaim))
}

// verify compares the disk after compaction with before. It returns why the
// result is not trusted, or "".
func verify(env Env, before, after disks.Disk, opt Options, stored []bool) string {
	// The file must read cleanly now: no log left to replay.
	if after.Estimate.InUse && before.Distro == "" {
		return "the disk file is still in use after compacting"
	}
	// The filesystem must still be there, without recorded errors.
	f, size, err := env.Disks.Open(after.Path)
	if err != nil {
		return "the disk cannot be opened after compacting: " + err.Error()
	}
	defer f.Close()
	vd, err := vhdx.Read(f, size)
	if err != nil {
		return "the disk is no longer a valid VHDX: " + err.Error()
	}
	if before.Estimate != nil && before.Estimate.Filesystem {
		fs, err := ext4.Read(vd.Contents(), vd.VirtualSize)
		if err != nil {
			return "the filesystem cannot be read after compacting: " + err.Error()
		}
		if fs.HasErrors {
			return "the filesystem records errors after compacting; run e2fsck before using it"
		}
		// What is in use may shrink by what was cleaned, and by a little more:
		// a filesystem read while mounted lags its journal by the last few
		// seconds of writes, which the stop and the trim settle. Losing more
		// than that means data went missing; compaction never changes data.
		var cleaned int64
		for _, it := range opt.Clean {
			cleaned += it.Bytes
		}
		usedBefore, usedAfter := before.Estimate.Used, int64(fs.UsedBlocks)*fs.BlockSize
		slack := usedBefore/200 + 32<<20
		if usedAfter < usedBefore-cleaned-slack {
			return fmt.Sprintf("the filesystem uses %s after compacting, %s before: more went than was cleaned", units.Bytes(usedAfter), units.Bytes(usedBefore))
		}
		// Block by block: a block that held data before and that the
		// filesystem still uses must still be stored. Windows' compaction
		// keeps every such block, zeros included (measured: the result
		// matched the estimate, which counts them, to the byte); one gone
		// is data gone, whatever the counts say.
		per := uint64(vd.BlockSize) / uint64(fs.BlockSize)
		lost := 0
		for i, b := range vd.Blocks {
			if i < len(stored) && stored[i] && !b.Present() && fs.AnyInUse(uint64(i)*per, uint64(i+1)*per) {
				lost++
			}
		}
		if lost > 0 {
			return fmt.Sprintf("%d blocks the filesystem uses are no longer stored after compacting: data went missing", lost)
		}
	}
	return ""
}

// restart starts the distribution again, makes sure its root is writable,
// and starts the containers that were running. The event is Warned when
// something needed a second try or did not come back.
func restart(ctx context.Context, env Env, distro string, ids []string, sleep func(time.Duration)) (Event, error) {
	sys := env.Disks.WSL
	e := Event{Step: StepRestart, Status: Done, Detail: "running"}
	if err := sys.Start(within(ctx, callLimit), distro); err != nil {
		return e, err
	}
	// WSL has been seen to leave the root read-only after compacting until
	// it is stopped and started again; check, and do that once.
	ro, err := readOnly(ctx, env, distro)
	if err != nil {
		return e, err
	}
	if ro {
		_ = sys.Terminate(within(ctx, callLimit), distro)
		sleep(2 * time.Second)
		if err := sys.Start(within(ctx, callLimit), distro); err != nil {
			return e, err
		}
		if ro, err = readOnly(ctx, env, distro); err != nil {
			return e, err
		}
		if ro {
			return e, errors.New("the root filesystem stays read-only; `wsl --shutdown` restarts WSL and clears it")
		}
		e.Status, e.SecondStart, e.Detail = Warned, true, "running (writable after a second start)"
	}
	if len(ids) > 0 {
		// Start only the containers that ran before; a container someone had
		// stopped stays stopped. Docker may still be starting with the
		// distribution: wait for it, up to a minute.
		e.Count = len(ids)
		for range 30 {
			if out, err := sys.Run(within(ctx, callLimit), distro, "root", "docker info >/dev/null 2>&1 && echo up"); err == nil && strings.Contains(out, "up") {
				break
			}
			sleep(2 * time.Second)
		}
		out, err := sys.Run(within(ctx, stopLimit), distro, "root", "docker start "+strings.Join(safeIDs(ids), " ")+" >/dev/null 2>&1 && echo ok || echo failed")
		if err != nil || !strings.Contains(out, "ok") {
			e.Status, e.NotRestarted = Warned, len(ids)
			e.Detail += fmt.Sprintf(", %d containers not started again", len(ids))
		} else {
			e.Detail += fmt.Sprintf(", %d containers started again", len(ids))
		}
	}
	return e, nil
}

// storedBlocks reads which payload blocks hold data, or nil when the file
// cannot be read (the verify step then compares counts only).
func storedBlocks(env Env, path string) []bool {
	f, size, err := env.Disks.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	vd, err := vhdx.Read(f, size)
	if err != nil {
		return nil
	}
	out := make([]bool, len(vd.Blocks))
	for i, b := range vd.Blocks {
		out[i] = b.Present()
	}
	return out
}

// Limits on the calls made once a distribution is stopped, so that one that
// never answers cannot hold a run forever (the screen keeps Ctrl+C from
// leaving it half done): a quick call, the stop with its containers' own
// timeouts and the hooks', and the compaction of a very large disk.
const (
	callLimit    = 2 * time.Minute
	stopLimit    = 15 * time.Minute
	compactLimit = 6 * time.Hour
)

// within gives a call a deadline. The context it derives from is never
// cancelled by the caller after the stop, so only the deadline ends a call.
func within(ctx context.Context, d time.Duration) context.Context {
	c, cancel := context.WithTimeout(ctx, d)
	// The deadline's timer is released when it fires; holding no cancel
	// function past this call is what a fire-and-forget deadline needs.
	_ = cancel
	return c
}

// reclaimOf is a disk's estimated gain, zero when unknown.
func reclaimOf(d disks.Disk) int64 {
	if d.Estimate == nil {
		return 0
	}
	return d.Estimate.Reclaim
}

// readOnly reports whether the distribution's root is mounted read-only.
func readOnly(ctx context.Context, env Env, distro string) (bool, error) {
	out, err := env.Disks.WSL.Run(within(ctx, callLimit), distro, "root", `awk '$2=="/"{print $4}' /proc/mounts | tail -1`)
	if err != nil {
		return false, err
	}
	opts := strings.TrimSpace(out)
	return opts == "ro" || strings.HasPrefix(opts, "ro,"), nil
}

// safeIDs keeps only container ids made of hex digits.
func safeIDs(ids []string) []string {
	var out []string
	for _, id := range ids {
		ok := len(id) >= 12 && len(id) <= 64
		for _, c := range id {
			ok = ok && (c >= '0' && c <= '9' || c >= 'a' && c <= 'f')
		}
		if ok {
			out = append(out, id)
		}
	}
	return out
}

// oneLine keeps the first line of an error, for a step's detail.
func oneLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}
