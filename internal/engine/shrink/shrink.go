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
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/kanggu12/wsl-shrinker/internal/core/units"
	"github.com/kanggu12/wsl-shrinker/internal/engine/disks"
	"github.com/kanggu12/wsl-shrinker/internal/engine/estimate"
	"github.com/kanggu12/wsl-shrinker/internal/engine/survey"
	"github.com/kanggu12/wsl-shrinker/internal/machine/ext4"
	"github.com/kanggu12/wsl-shrinker/internal/machine/spawner"
	"github.com/kanggu12/wsl-shrinker/internal/machine/vhdx"
	"github.com/kanggu12/wsl-shrinker/internal/machine/winapi"
	"github.com/kanggu12/wsl-shrinker/internal/machine/wsl"
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
	Step   Step   `json:"step"`
	Status Status `json:"status"`
	// Detail is one short line in English: what was found or done, or why
	// it failed. The screen builds its own words from the fields below.
	Detail string `json:"detail"`
	// Bytes, Count and Hooks are the step's numbers: space to free, freed or
	// cleaned; items cleaned or chosen, or containers stopped and started;
	// hooks run.
	Bytes int64 `json:"bytes"`
	Count int   `json:"count"`
	Hooks int   `json:"hooks"`
	// Took is how long the compaction took (took_seconds in JSON).
	Took time.Duration `json:"-"`
	// SecondStart is set when the root came back writable only after a
	// second start; NotRestarted counts containers that did not start.
	SecondStart  bool `json:"second_start"`
	NotRestarted int  `json:"not_restarted"`
	// DistroStarted is set on a restart that started the distribution,
	// which had stopped (the compaction stops it); one still running when
	// the step began keeps what kept it running.
	DistroStarted bool      `json:"distro_started,omitempty"`
	At            time.Time `json:"at"`
	// Containers are the ids the stop step stopped, on its last event: what
	// a run cut off after it must start again.
	Containers []string `json:"containers,omitempty"`
	// Code names why a check failed, for words other than Detail's English:
	// a Block, or CodeNoDistro, CodeWSL, CodeBusy.
	Code string `json:"code,omitempty"`
	// By is who stopped the distribution in the stop step: BySpawner when
	// wsl-spawner did, "" when this program did.
	By string `json:"by,omitempty"`
	// Phase is, on a stop step still running, the step wsl-spawner began:
	// hook, units, docker, podman, sync, or terminate (WSL ending the rest).
	Phase string `json:"phase,omitempty"`
	// Spawner is, on a finished stop step, how wsl-spawner's stop went when
	// it stopped the distribution (clean, limit, failed, error), or
	// SpawnerNotStopped when it is installed but stopped nothing, so this
	// program did. "" when wsl-spawner had no part, or its stop went on in
	// the background, where how it went is in its own history.
	Spawner string `json:"spawner,omitempty"`
	// HooksFailed counts the person's pre-stop scripts that failed.
	HooksFailed int `json:"hooks_failed,omitempty"`
}

// BySpawner is Event.By for a distribution wsl-spawner stopped.
const BySpawner = "wsl-spawner"

// SpawnerNotStopped is Event.Spawner when wsl-spawner is installed but
// stopped nothing (it leaves the distribution to WSL, say, or did not
// start): this program stopped it, as without wsl-spawner.
const SpawnerNotStopped = "not-stopped"

// Codes of a failed check besides the Blocks.
const (
	// CodeNoDistro: the distribution is gone since the disks were read.
	CodeNoDistro = "nodistro"
	// CodeWSL: WSL did not say which distributions run.
	CodeWSL = "wsl"
	// CodeBusy: another run is not finished (the app's journal).
	CodeBusy = "busy"
)

// blockError is a check refused for a Block: its code goes on the event.
type blockError struct {
	block Block
	msg   string
}

func (e blockError) Error() string { return e.msg }

// eventJSON is an Event as JSON: its duration in seconds, as everywhere in
// this program's JSON.
type eventJSON struct {
	Step          Step      `json:"step"`
	Status        Status    `json:"status"`
	Detail        string    `json:"detail"`
	Bytes         int64     `json:"bytes"`
	Count         int       `json:"count"`
	Hooks         int       `json:"hooks"`
	TookSeconds   float64   `json:"took_seconds"`
	SecondStart   bool      `json:"second_start"`
	NotRestarted  int       `json:"not_restarted"`
	DistroStarted bool      `json:"distro_started,omitempty"`
	At            time.Time `json:"at"`
	Containers    []string  `json:"containers,omitempty"`
	Code          string    `json:"code,omitempty"`
	By            string    `json:"by,omitempty"`
	Phase         string    `json:"phase,omitempty"`
	Spawner       string    `json:"spawner,omitempty"`
	HooksFailed   int       `json:"hooks_failed,omitempty"`
}

// MarshalJSON writes the event with took_seconds.
func (e Event) MarshalJSON() ([]byte, error) {
	return json.Marshal(eventJSON{e.Step, e.Status, e.Detail, e.Bytes, e.Count, e.Hooks, e.Took.Seconds(), e.SecondStart, e.NotRestarted, e.DistroStarted, e.At, e.Containers, e.Code,
		e.By, e.Phase, e.Spawner, e.HooksFailed})
}

// UnmarshalJSON reads what MarshalJSON writes.
func (e *Event) UnmarshalJSON(b []byte) error {
	var j eventJSON
	if err := json.Unmarshal(b, &j); err != nil {
		return err
	}
	*e = Event{j.Step, j.Status, j.Detail, j.Bytes, j.Count, j.Hooks, seconds(j.TookSeconds), j.SecondStart, j.NotRestarted, j.DistroStarted, j.At, j.Containers, j.Code,
		j.By, j.Phase, j.Spawner, j.HooksFailed}
	return nil
}

// seconds turns a JSON duration back into a time.Duration.
func seconds(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }

// Options are the person's choices for one disk.
type Options struct {
	// Clean lists the survey items to remove before compacting.
	Clean []survey.Item
}

// Left is how a run left the disk's distribution, failed or not: what the
// person needs to know next, said once by the engine for every front.
type Left string

const (
	// LeftRunning: a distribution that ran runs again, or was never stopped.
	LeftRunning Left = "running"
	// LeftStopped: a distribution that was not running is still not running.
	LeftStopped Left = "stopped"
	// LeftFile: the disk is a file WSL does not register; nothing runs on it.
	LeftFile Left = "file"
	// LeftSuspect: the disk does not read as before the compaction, so the
	// distribution was left stopped until someone looks at it.
	LeftSuspect Left = "suspect"
	// LeftDown: a distribution that ran did not start again.
	LeftDown Left = "down"
)

// Env is everything the steps use. CompactFile and Stat reach Windows;
// tests give fakes.
type Env struct {
	Disks       disks.Env
	CompactFile func(path string) error
	// CanCompact says whether WSL offers `wsl --manage --compact`.
	CanCompact bool
	// Sleep waits between checks; tests make it instant.
	Sleep func(time.Duration)
	// Protected says whether the settings protect a disk, by name: the run
	// refuses it whatever front asked. nil protects none.
	Protected func(name string) bool
	// Spawner is wsl-spawner, when it is installed: the stop step asks it to
	// stop a running distribution, so its services get their time and it
	// records the stop (without, it takes the next start for one after a
	// distribution cut off). nil when it is not installed.
	Spawner spawner.Stopper
}

// Block is a reason a disk cannot be shrunk now. The run's check refuses
// for the first one, and every front says it in its own words: the rule is
// written once, here.
type Block string

const (
	// BlockProtected: the settings protect the disk.
	BlockProtected Block = "protected"
	// BlockUnreadable: the disk file could not be read.
	BlockUnreadable Block = "unreadable"
	// BlockEncrypted: the file is encrypted (EFS); Windows cannot compact it.
	BlockEncrypted Block = "encrypted"
	// BlockFSErrors: the kernel recorded errors in the filesystem; e2fsck
	// should see it before anything else touches it.
	BlockFSErrors Block = "fserrors"
	// BlockFixed: a fixed-size disk. Windows compacts only disks that grow:
	// asked to compact this one it reports success and leaves the file as it
	// is, so a run would stop the distribution for nothing.
	BlockFixed Block = "fixed"
	// BlockHeld: a program holds the file (Docker Desktop its data disk).
	BlockHeld Block = "held"
	// BlockNoCompact: this WSL cannot compact a distribution.
	BlockNoCompact Block = "nocompact"
)

// Preflight lists why disk d cannot be shrunk now, the most pressing first,
// or nothing when it can. canCompact is whether WSL offers --compact;
// protected whether the settings protect d.
func Preflight(d disks.Disk, canCompact, protected bool) []Block {
	var out []Block
	if protected {
		out = append(out, BlockProtected)
	}
	// Nothing else can be said about a disk that was not read.
	if d.Err != "" || d.Estimate == nil {
		return append(out, BlockUnreadable)
	}
	if d.File.Encrypted {
		out = append(out, BlockEncrypted)
	}
	if d.Estimate.FSErrors {
		out = append(out, BlockFSErrors)
	}
	// Before what a program holds: quitting the program would not help.
	if d.Estimate.Fixed {
		out = append(out, BlockFixed)
	}
	if d.Distro == "" && d.File.Held {
		out = append(out, BlockHeld)
	}
	// A distribution another program registers for itself is that
	// program's to run: while it runs, the program holds it as Docker
	// Desktop holds its data disk, and a compaction would stop it under the
	// program's feet.
	if d.Distro != "" && OwnerOf(d) != OwnerNone && d.Running {
		out = append(out, BlockHeld)
	}
	if d.Distro != "" && !canCompact {
		out = append(out, BlockNoCompact)
	}
	return out
}

// Owner is a program that keeps a disk for itself: Docker Desktop its data
// disk and its own distributions, Rancher Desktop and Podman theirs. While
// that program runs, the disk is its own, and only quitting or stopping the
// program lets it go.
type Owner string

const (
	// OwnerNone: the person's own disk, or a file another program may have
	// open for a moment.
	OwnerNone Owner = ""
	// OwnerDocker: Docker Desktop's data disk, or docker-desktop and the
	// older docker-desktop-data.
	OwnerDocker Owner = "docker-desktop"
	// OwnerRancher: Rancher Desktop's rancher-desktop and
	// rancher-desktop-data.
	OwnerRancher Owner = "rancher-desktop"
	// OwnerPodman: a Podman machine, the distribution `podman machine init`
	// imports.
	OwnerPodman Owner = "podman"
)

// OwnerOf says which program keeps disk d for itself, OwnerNone for none.
// The distributions go by the names their programs register, and a Podman
// machine by where Podman imports it, as its name is the person's choice.
func OwnerOf(d disks.Disk) Owner {
	switch {
	case d.Kind == disks.KindDocker:
		return OwnerDocker
	case d.Distro == "":
		return OwnerNone
	case strings.EqualFold(d.Distro, "docker-desktop"), strings.EqualFold(d.Distro, "docker-desktop-data"):
		return OwnerDocker
	case strings.EqualFold(d.Distro, "rancher-desktop"), strings.EqualFold(d.Distro, "rancher-desktop-data"):
		return OwnerRancher
	case podmanMachine(d.Path):
		return OwnerPodman
	}
	return OwnerNone
}

// podmanMachine reports whether path is a Podman machine's disk: Podman
// imports each machine's distribution into a folder of its own under
// %USERPROFILE%\.local\share\containers\podman\machine\wsl\wsldist.
func podmanMachine(path string) bool {
	p := strings.ToLower(strings.ReplaceAll(path, "/", `\`))
	return strings.Contains(p, `\containers\podman\machine\wsl\`)
}

// Reason is a block in English, as the run's check and the command line
// say it, with what to do about it.
func (b Block) Reason(d disks.Disk) string {
	switch b {
	case BlockProtected:
		return "the disk is protected in the settings"
	case BlockUnreadable:
		return "the disk cannot be read: " + d.Err
	case BlockEncrypted:
		// WSL's compaction fails on encrypted files (0x80071772).
		return "the disk file is encrypted (EFS); Windows cannot compact it"
	case BlockFSErrors:
		// Errors recorded before the run: a compaction would be blamed for
		// them, and e2fsck should see the disk first.
		return "the filesystem records errors; check it with e2fsck first"
	case BlockFixed:
		return "the disk is fixed-size: its file keeps its full size, and Windows compacts only disks that grow"
	case BlockHeld:
		switch OwnerOf(d) {
		case OwnerDocker:
			return "the file is in use; quit Docker Desktop first"
		case OwnerRancher:
			return "the distribution is Rancher Desktop's; quit Rancher Desktop first"
		case OwnerPodman:
			return "the distribution is a Podman machine; stop it first: podman machine stop"
		}
		return "the file is in use; close the program that uses it first"
	case BlockNoCompact:
		return "this WSL has no `wsl --manage --compact`; run `wsl --update`"
	}
	return string(b)
}

// Result is how one disk's run went. Before and After are the estimates
// read before and after; the file's size in bytes is in them and in File.
type Result struct {
	Disk   string           `json:"disk"`
	Before *estimate.Result `json:"before,omitempty"`
	After  *estimate.Result `json:"after,omitempty"`
	Freed  int64            `json:"freed_bytes"`
	Events []Event          `json:"events"`
	Err    string           `json:"error,omitempty"`
	File   *winapi.FileInfo `json:"file_after,omitempty"`
	// Took is how long the run took (took_seconds in JSON).
	Took  time.Duration     `json:"-"`
	Notes map[string]string `json:"notes,omitempty"`
	// FailedAt is the step that failed, "" for a run that did not fail.
	FailedAt Step `json:"failed_at,omitempty"`
	// Compacted is set once the compaction itself went through: the disk
	// is smaller even when a later step failed.
	Compacted bool `json:"compacted"`
	// Left is how the run left the distribution.
	Left Left `json:"left"`
	// ContainersNotStarted counts the containers that ran before and did
	// not start again.
	ContainersNotStarted int `json:"containers_not_started,omitempty"`
	// ContainersStarted counts the containers that ran before and were
	// started again. They run as long as WSL keeps the distribution
	// running, which IdleNote says more of.
	ContainersStarted int `json:"containers_started,omitempty"`
	// DistroStarted: the run started the distribution again after it had
	// stopped, so what kept it running before, a terminal open in it, is
	// gone. With ContainersStarted, the fronts say what IdleNote says.
	DistroStarted bool `json:"distro_started,omitempty"`
}

// Idle reports whether the note on WSL's idle stop goes with a start of a
// distribution: one that had stopped, with containers started again.
func Idle(distroStarted bool, started int) bool { return distroStarted && started > 0 }

// IdleNote says, for a distribution started again with containers a run or
// a recovery started, how long they run. WSL stops a distribution once no
// terminal or other program from Windows runs in it, after its
// instanceIdleTimeout (15 seconds unless .wslconfig sets it), and what runs
// inside stops with it. The command line says it under a run and a
// recovery, the screen under a run.
func IdleNote(distro string) string {
	return "with no terminal open in " + distro + ", WSL stops it in about 15 seconds " +
		"(instanceIdleTimeout in .wslconfig), and its containers with it; keep one open to keep them running"
}

// MarshalJSON writes the result with took_seconds.
func (r Result) MarshalJSON() ([]byte, error) {
	// The same fields through a type without this method, plus the seconds.
	type plain Result
	return json.Marshal(struct {
		plain
		TookSeconds float64 `json:"took_seconds"`
	}{plain(r), r.Took.Seconds()})
}

// UnmarshalJSON reads what MarshalJSON writes.
func (r *Result) UnmarshalJSON(b []byte) error {
	type plain Result
	var j struct {
		plain
		TookSeconds float64 `json:"took_seconds"`
	}
	if err := json.Unmarshal(b, &j); err != nil {
		return err
	}
	*r = Result(j.plain)
	r.Took = seconds(j.TookSeconds)
	return nil
}

// Untouched is how a run refused before it changed anything leaves a disk:
// as it found it.
func Untouched(d disks.Disk) Left {
	switch {
	case d.Distro == "":
		return LeftFile
	case d.Running:
		return LeftRunning
	}
	return LeftStopped
}

// errCheck wraps a reason found before anything changed.
var errCheck = errors.New("not started")

// listScript names the containers the stop step is to stop, before any of
// them stops: the running ones, unless Docker Desktop's integration runs
// them (they are Docker Desktop's, which this program leaves alone). It
// prints "running|ID ID ...". The shell does it alone: with grep, a
// distribution without one would take Docker Desktop's containers for its
// own. The ids are hex, so echo joins them as they are.
const listScript = `if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
  case $(docker info --format '{{.OperatingSystem}}' 2>/dev/null) in
  (*'Docker Desktop'*) ;;
  (*) echo "running|$(echo $(docker ps -q))" ;;
  esac
fi
`

// containerID is a container's id as docker ps -q prints it: only those go
// into the stop script.
var containerID = regexp.MustCompile(`^[0-9a-f]{12,64}$`)

// runningIDs reads what listScript printed: the ids of the containers to
// stop. Anything that is not an id (a warning wsl.exe put in between) is
// passed over.
func runningIDs(listed string) []string {
	var ids []string
	for line := range strings.SplitSeq(listed, "\n") {
		if f := strings.Split(strings.TrimSpace(line), "|"); f[0] == "running" && len(f) == 2 {
			for _, id := range strings.Fields(f[1]) {
				if containerID.MatchString(id) {
					ids = append(ids, id)
				}
			}
		}
	}
	return ids
}

// stopScript stops what runs in a distribution the way it expects: the
// containers ids, each with its own stop timeout (Docker waits that long
// after SIGTERM), then the person's own pre-stop scripts, then a sync.
func stopScript(ids []string) string {
	return containersStopScript(ids, false) + hooksScript + "sync\n"
}

// containersStopScript stops the containers ids in one call, each with its
// own stop timeout, and with sync, writes what is in memory to the disk
// after: the stop of a run wsl-spawner stopped nothing of, its hooks run
// already.
func containersStopScript(ids []string, sync bool) string {
	var b strings.Builder
	if len(ids) > 0 {
		b.WriteString("docker stop " + strings.Join(ids, " ") + " >/dev/null\n")
	}
	if sync {
		b.WriteString("sync\n")
	}
	return b.String()
}

// hooksScript runs the person's own pre-stop scripts, each as root up to
// two minutes, in name order, and says of each "hook|ok|FILE" or
// "hook|failed|FILE". Before wsl-spawner's stop it runs alone: wsl-spawner
// ends the distribution as its stop ends, so nothing runs in it after.
const hooksScript = `if [ -d /etc/wsl-shrinker/pre-stop.d ]; then
  for f in /etc/wsl-shrinker/pre-stop.d/*; do
    [ -x "$f" ] || continue
    timeout 120 "$f" >/dev/null 2>&1 && echo "hook|ok|$f" || echo "hook|failed|$f"
  done
fi
`

// Run shrinks one disk and reports each step through emit. However it ends,
// at a failed step or at a panic in this program's own code, what it stopped
// is settled in one place, settle: started again, or left stopped when the
// disk does not read as before. res.Left says which.
func Run(ctx context.Context, env Env, d disks.Disk, opt Options, emit func(Event)) (res Result) {
	start := time.Now()
	res = Result{Disk: d.Name, Before: d.Estimate, Notes: map[string]string{}}
	// What the run has done so far, which settle may have to undo.
	var (
		// cur is the step running now: a panic is that step's failure.
		cur = StepCheck
		// touched is set once something may be stopped: the containers, or
		// the distribution by the compaction.
		touched bool
		// restartIDs are the containers the stop step stopped.
		restartIDs []string
		// settled is set once settle ran, so a panic inside it is not
		// followed by a second settle.
		settled bool
		// stoppedAlone is set when the run failed because the distribution
		// stopped on its own, before the run stopped anything.
		stoppedAlone bool
		// cleaned is what the clean step removed, which the verify step
		// allows for: nothing when it was skipped.
		cleaned int64
	)
	sayE := func(e Event) {
		e.At = time.Now()
		if e.Status == Running {
			cur = e.Step
		}
		res.Events = append(res.Events, e)
		if emit != nil {
			emit(e)
		}
	}
	say := func(s Step, st Status, format string, a ...any) {
		sayE(Event{Step: s, Status: st, Detail: fmt.Sprintf(format, a...)})
	}
	// settle leaves the distribution as it should be once the run has ended,
	// and says how in res.Left.
	settle := func(suspect bool) {
		settled = true
		ran := d.Distro != "" && d.Running
		switch {
		case suspect:
			// The disk does not read as before: starting the distribution on
			// it could make that worse. It stays stopped, and says so.
			res.Left = LeftSuspect
			if ran {
				sayE(Event{Step: StepRestart, Status: Skipped, Detail: "left stopped: check the disk first", Count: len(restartIDs)})
			}
		case d.Distro == "":
			res.Left = LeftFile
		case !ran:
			res.Left = LeftStopped
		case !touched && stoppedAlone:
			// It stopped by itself, as during the clean step: nothing here
			// stopped it, and it is not started again.
			res.Left = LeftStopped
		case !touched:
			// Nothing was stopped yet: it runs as it did.
			res.Left = LeftRunning
		case res.FailedAt == StepRestart:
			// Starting it again is what failed; it is not tried twice.
			res.Left = LeftDown
		default:
			// Started again as it was, writable, with the containers that ran.
			e, err := restart(ctx, env, d.Distro, restartIDs, sleep(env))
			res.Left, res.ContainersNotStarted = LeftRunning, e.NotRestarted
			if err != nil {
				e = Event{Step: StepRestart, Status: Failed, Detail: oneLine(err.Error())}
				res.Left = LeftDown
			} else {
				// Those that ran before, less those that did not start, and
				// whether the distribution itself had stopped.
				res.restarted(e)
			}
			sayE(e)
		}
		res.Took = time.Since(start)
	}
	// fail ends the run at step s: the failure is the run's result, and
	// settle deals with what was stopped. A failed check carries a code
	// for why, which the fronts say in their own words.
	fail := func(s Step, err error, suspect bool) Result {
		e := Event{Step: s, Status: Failed, Detail: oneLine(err.Error())}
		var be blockError
		switch {
		case s != StepCheck:
		case errors.As(err, &be):
			e.Code = string(be.block)
		case errors.Is(err, wsl.ErrNoDistro):
			e.Code = CodeNoDistro
		default:
			e.Code = CodeWSL
		}
		sayE(e)
		res.Err, res.FailedAt = err.Error(), s
		stoppedAlone = errors.Is(err, wsl.ErrNotRunning)
		settle(suspect)
		return res
	}
	// A panic in this code fails the step it hit and is settled like any
	// failure: a distribution stopped by a bug and left so is worse than
	// the bug.
	defer func() {
		p := recover()
		if p == nil {
			return
		}
		err := fmt.Errorf("internal error: %v", p)
		if settled {
			// It came from settling itself, or from saying how it ended: the
			// outcome stands when it was set, and is not known otherwise.
			say(cur, Failed, "%s", oneLine(err.Error()))
			res.Err = err.Error()
			if d.Distro != "" && d.Running && res.Left == "" {
				res.Left = LeftDown
			}
			return
		}
		func() {
			// A second panic while settling is recorded, not raised.
			defer func() {
				if p := recover(); p != nil {
					res.Err += fmt.Sprintf("; then, starting it again: %v", p)
					if d.Distro != "" && d.Running {
						res.Left = LeftDown
					}
				}
			}()
			res = fail(cur, err, false)
		}()
	}()
	sys := env.Disks.WSL

	// 1. check: everything that would stop the run, before anything changes.
	say(StepCheck, Running, "")
	if err := check(ctx, env, &d); err != nil {
		return fail(StepCheck, fmt.Errorf("%w: %w", errCheck, err), false)
	}
	// What it gives back with what was chosen to remove inside, as the
	// question before the run said.
	gain := reclaimOf(d)
	if d.Running {
		gain += chosenBytes(opt.Clean)
	}
	sayE(Event{Step: StepCheck, Status: Done, Detail: checkDetail(d, gain), Bytes: gain})

	// 2. clean: only what was chosen, only in a running distribution.
	switch {
	case len(opt.Clean) == 0:
		say(StepClean, Skipped, "nothing chosen")
	case d.Distro == "":
		sayE(Event{Step: StepClean, Status: Skipped, Detail: "not a distribution", Count: len(opt.Clean)})
	case !d.Running:
		// Chosen, but nothing inside a stopped distribution can be removed.
		sayE(Event{Step: StepClean, Status: Skipped, Detail: "not running", Count: len(opt.Clean)})
	default:
		say(StepClean, Running, "")
		n := chosenBytes(opt.Clean)
		if out, err := survey.Clean(ctx, sys, d.Distro, opt.Clean); err != nil {
			return fail(StepClean, err, false)
		} else if strings.Contains(out, "failed: ") {
			// One item failing does not stop the run; the rest was removed.
			cleaned = n
			sayE(Event{Step: StepClean, Status: Warned, Detail: fmt.Sprintf("%d items, some failed", len(opt.Clean)), Count: len(opt.Clean), Bytes: n})
		} else {
			cleaned = n
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
		return fail(StepStop, fmt.Errorf("cancelled before the distribution was stopped: %w", err), false)
	}
	ctx = context.WithoutCancel(ctx)
	switch {
	case d.Distro == "":
		say(StepStop, Skipped, "not a distribution")
	case !d.Running:
		say(StepStop, Skipped, "not running")
	default:
		say(StepStop, Running, "")
		// From the first container stopped on, settle starts things again.
		touched = true
		// The containers are named first, and said before any of them stops:
		// the journal follows that, so a run cut off while they stop still
		// starts them again.
		listed, err := sys.Run(within(ctx, stopLimit), d.Distro, "root", listScript)
		if err != nil {
			return fail(StepStop, err, false)
		}
		restartIDs = runningIDs(listed)
		if len(restartIDs) > 0 {
			sayE(Event{Step: StepStop, Status: Running, Containers: restartIDs})
		}
		res.Notes["containers"] = strings.Join(restartIDs, " ")
		var e Event
		if env.Spawner != nil {
			// wsl-spawner stops it, its way: what it records of the stop
			// is what makes its next start a start after a clean stop.
			e, err = stopBySpawner(ctx, env, d.Distro, restartIDs, sayE)
		} else {
			e, err = stopItself(ctx, sys, d.Distro, restartIDs)
		}
		if errors.Is(err, errOtherStop) {
			// Another wsl-spawner stop of it runs: that stop ends it, and the
			// run stopped none of it, so it is not started again.
			touched = false
		}
		if err != nil {
			// A distribution that still runs (only the compaction, or
			// wsl-spawner's stop, stops it) gets what was stopped started
			// again by settle.
			return fail(StepStop, err, false)
		}
		sayE(e)
	}

	// 4. compact: WSL's own for a distribution, the virtual disk API for a
	// file WSL does not register. What the verify step compares with is read
	// first, now that the stop has settled the disk: the filesystem's use
	// then, not when the disk was listed (files may have gone since).
	say(StepCompact, Running, "")
	base := readBaseline(env, d)
	t0 := time.Now()
	var err error
	if d.Distro != "" {
		err = sys.Compact(within(ctx, compactLimit), d.Distro)
	} else {
		err = env.CompactFile(d.Path)
	}
	if err != nil {
		// The distribution may already be stopped: settle brings it back as
		// it was, with its containers, if it ran.
		return fail(StepCompact, err, false)
	}
	res.Compacted = true
	took := time.Since(t0).Round(100 * time.Millisecond)
	sayE(Event{Step: StepCompact, Status: Done, Detail: took.String(), Took: took})

	// 5. verify: read the disk again, now that Windows has let go of it. A
	// disk that does not read as before stays stopped; one that could not
	// be read just then (another program had the file a moment) does not.
	say(StepVerify, Running, "")
	if v := verify(env, d, cleaned, base); v.msg != "" {
		return fail(StepVerify, errors.New(v.msg), v.suspect)
	}
	after := d
	disks.Inspect(env.Disks, &after)
	if after.Estimate != nil {
		res.After, res.File = after.Estimate, &after.File
		res.Freed = max(0, base.onDisk-after.File.OnDisk)
	}
	sayE(Event{Step: StepVerify, Status: Done, Detail: "freed " + units.Bytes(res.Freed), Bytes: res.Freed})

	// 6. restart: as it was, writable, with its containers.
	if d.Distro == "" || !d.Running {
		say(StepRestart, Skipped, "was not running")
		settle(false)
		return res
	}
	say(StepRestart, Running, "")
	e, err := restart(ctx, env, d.Distro, restartIDs, sleep(env))
	if err != nil {
		return fail(StepRestart, err, false)
	}
	// The outcome first, then the saying of it: a bug in the screen as the
	// last event goes out does not turn a distribution that runs into one
	// recorded as down.
	settled = true
	res.Left, res.ContainersNotStarted = LeftRunning, e.NotRestarted
	// Those that ran before, less those that did not start, and whether the
	// distribution itself had stopped.
	res.restarted(e)
	res.Took = time.Since(start)
	sayE(e)
	return res
}

// StartAgain starts a distribution the way a run does after compacting it:
// writable, with the containers named. It finishes a run that was cut off.
func StartAgain(ctx context.Context, env Env, distro string, ids []string) (Event, error) {
	return restart(ctx, env, distro, ids, sleep(env))
}

// sleep is the environment's way to wait, time.Sleep by default.
func sleep(env Env) func(time.Duration) {
	if env.Sleep != nil {
		return env.Sleep
	}
	return time.Sleep
}

// check finds what would stop a run before anything changes. It reads again
// whether the distribution runs: the disk may have been listed long before
// (a screen left open, the second disk of a queue), and a distribution
// started since would otherwise be stopped by the compaction without its
// containers' own stop, and not started again.
func check(ctx context.Context, env Env, d *disks.Disk) error {
	// The disk as it is now, not as it was listed: a program may have taken
	// the file since (Docker Desktop started), or errors been recorded.
	disks.Inspect(env.Disks, d)
	// The one rule every front shows; refused here whoever asked.
	protected := env.Protected != nil && env.Protected(d.Name)
	if bs := Preflight(*d, env.CanCompact, protected); len(bs) > 0 {
		return blockError{bs[0], bs[0].Reason(*d)}
	}
	if d.Distro == "" {
		return nil
	}
	// Whether it runs now; a WSL that does not say is an error, not "stopped".
	ds, err := env.Disks.WSL.Distros(within(ctx, callLimit))
	if err != nil {
		return err
	}
	for _, x := range ds {
		if strings.EqualFold(x.Name, d.Distro) {
			d.Running = x.Running
			return nil
		}
	}
	return fmt.Errorf("%w: %s", wsl.ErrNoDistro, d.Distro)
}

// checkDetail sums up what the check saw: what the run gives back.
func checkDetail(d disks.Disk, gain int64) string {
	if d.Estimate == nil {
		return ""
	}
	return fmt.Sprintf("about %s to give back", units.Bytes(gain))
}

// baseline is the disk as read right before the compaction: which payload
// blocks were stored, and the filesystem's use and state then.
type baseline struct {
	// stored has one entry per payload block; nil when the file could not
	// be read (the verify step then compares the use only).
	stored []bool
	// fs is false when no ext4 filesystem was read; used and errors are
	// its numbers then.
	fs     bool
	used   int64
	errors bool
	// onDisk is what the file took on the drive then, for what was freed.
	onDisk int64
}

// readBaseline reads the disk before the compaction. A distribution still
// runs here (WSL stops it in the compaction): its filesystem is read through
// the replayed log, and lags it by the last seconds of writes, which the
// verify step allows for.
func readBaseline(env Env, d disks.Disk) baseline {
	// Without a fresh read, the listing's numbers are the next best.
	b := baseline{onDisk: d.File.OnDisk}
	if d.Estimate != nil && d.Estimate.Filesystem {
		b.fs, b.used, b.errors = true, d.Estimate.Used, d.Estimate.FSErrors
	}
	if fi, err := env.Disks.Stat(d.Path); err == nil {
		b.onDisk = fi.OnDisk
	}
	f, size, err := env.Disks.Open(d.Path)
	if err != nil {
		return b
	}
	defer f.Close()
	vd, err := vhdx.Read(f, size)
	if err != nil {
		return b
	}
	b.stored = make([]bool, len(vd.Blocks))
	for i, x := range vd.Blocks {
		b.stored[i] = x.Present()
	}
	if fs, err := ext4.Read(vd.Contents(), vd.VirtualSize, int64(vd.BlockSize)); err == nil {
		b.fs, b.used, b.errors = true, int64(fs.UsedBlocks)*fs.BlockSize, fs.HasErrors
	}
	return b
}

// verdict is what the verify step found: why the result is not trusted
// ("" when it is), and whether the disk itself looks wrong (suspect), as
// opposed to a file that could not be opened just then.
type verdict struct {
	msg     string
	suspect bool
}

// verify reads the disk after compaction and compares it with the baseline.
// cleaned is what the clean step removed inside: the filesystem may use that
// much less, and no more.
func verify(env Env, d disks.Disk, cleaned int64, base baseline) verdict {
	// Opening can fail for a moment (another program has the file): that
	// says nothing about the disk.
	f, size, err := env.Disks.Open(d.Path)
	if err != nil {
		return verdict{msg: "the disk cannot be opened after compacting: " + err.Error()}
	}
	defer f.Close()
	// From here on, what is read is the disk itself.
	bad := func(format string, a ...any) verdict { return verdict{msg: fmt.Sprintf(format, a...), suspect: true} }
	vd, err := vhdx.Read(f, size)
	if err != nil {
		return bad("the disk is no longer a valid VHDX: %v", err)
	}
	// The file must read cleanly now: no log left to replay. A distribution's
	// file may still be held by WSL a moment after its compaction.
	if vd.LogActive && d.Distro == "" {
		return verdict{msg: "the disk file is still in use after compacting"}
	}
	if !base.fs {
		return verdict{}
	}
	// The filesystem must still be there, without recorded errors.
	fs, err := ext4.Read(vd.Contents(), vd.VirtualSize, int64(vd.BlockSize))
	if err != nil {
		return bad("the filesystem cannot be read after compacting: %v", err)
	}
	if fs.HasErrors && !base.errors {
		return bad("the filesystem records errors after compacting; run e2fsck before using it")
	}
	// What is in use may shrink by what was cleaned, and by a little more:
	// a filesystem read while mounted lags its journal by the last few
	// seconds of writes, which the stop and the trim settle. Losing more
	// than that means data went missing; compaction never changes data.
	// The baseline was read after the clean, but through bitmaps that may
	// not show the clean yet, so what was cleaned stays allowed: taking it
	// out would fail good runs and leave their distribution stopped. A
	// block gone that the filesystem still uses is caught below anyway.
	// What was chosen but not removed, as in a distribution that was not
	// running, is not allowed for.
	usedAfter := int64(fs.UsedBlocks) * fs.BlockSize
	slack := base.used/200 + 32<<20
	if usedAfter < base.used-cleaned-slack {
		return bad("the filesystem uses %s after compacting, %s before: more went than was cleaned", units.Bytes(usedAfter), units.Bytes(base.used))
	}
	// Block by block, for WSL's compaction: a block that held data before
	// and that the filesystem still uses must still be stored. WSL keeps
	// every such block, zeros included (measured: the result matched the
	// estimate, which counts them, to the byte); one gone is data gone,
	// whatever the counts say.
	//
	// Windows' own compaction (a file WSL does not register) is left out:
	// it does not know the filesystem and drops exactly the stored blocks
	// that hold only zeros (measured; see winapi's tests), in use or not.
	// Such a block reads as zeros afterwards, as before, so no data can go
	// that way; a block with any data in it is never dropped. The checks
	// above (a clean filesystem, the same use) are what applies there.
	if d.Distro == "" {
		return verdict{}
	}
	per := uint64(vd.BlockSize) / uint64(fs.BlockSize)
	lost := 0
	for i, b := range vd.Blocks {
		if i < len(base.stored) && base.stored[i] && !b.Present() && fs.AnyInUse(uint64(i)*per, uint64(i+1)*per) {
			lost++
		}
	}
	if lost > 0 {
		return bad("%d blocks the filesystem uses are no longer stored after compacting: data went missing", lost)
	}
	return verdict{}
}

// restart starts the distribution again, makes sure its root is writable,
// and starts the containers that were running. The event is Warned when
// something needed a second try or did not come back.
func restart(ctx context.Context, env Env, distro string, ids []string, sleep func(time.Duration)) (Event, error) {
	sys := env.Disks.WSL
	e := Event{Step: StepRestart, Status: Done, Detail: "running"}
	// Whether the distribution stopped: the compaction stops it, a stop step
	// that failed first does not. One this step starts again has lost what
	// kept it running, a terminal open in it, and WSL stops it again once
	// nothing from Windows runs in it (IdleNote).
	e.DistroStarted = !runningNow(ctx, sys, distro)
	ro, tries, err := startWritable(ctx, env, distro, sleep)
	if err != nil {
		return e, err
	}
	if tries > 1 {
		e.Detail = fmt.Sprintf("running (started on try %d)", tries)
	}
	// WSL has been seen to leave the root read-only after compacting until
	// it is stopped and started again; check, and do that once.
	if ro {
		_ = sys.Terminate(within(ctx, callLimit), distro)
		sleep(2 * time.Second)
		if ro, _, err = startWritable(ctx, env, distro, sleep); err != nil {
			return e, err
		}
		if ro {
			return e, errors.New("the root filesystem stays read-only; `wsl --shutdown` restarts WSL and clears it")
		}
		e.Status, e.SecondStart, e.Detail = Warned, true, "running (writable after a second start)"
	}
	ids = safeIDs(ids)
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
		// One start per container, each reported: one that fails does not
		// count the others as failed too. One that runs already (started
		// with the distribution, by wsl-spawner or by its restart policy)
		// is not started a second time, and runs again all the same.
		started := map[string]bool{}
		read := func(out string) {
			for line := range strings.SplitSeq(out, "\n") {
				line = strings.TrimSpace(line)
				if id, ok := strings.CutPrefix(line, "started|"); ok {
					started[id] = true
				} else if id, ok := strings.CutPrefix(line, "running|"); ok {
					started[id] = true
				}
			}
		}
		out, _ := sys.Run(within(ctx, stopLimit), distro, "root", startScript(ids))
		read(out)
		// A container can wait on another (one on another's network, or
		// linked to it), which may not have run yet when its turn came: a
		// second round, a little later, starts it once that one runs.
		var left []string
		for _, id := range ids {
			if !started[id] {
				left = append(left, id)
			}
		}
		if len(left) > 0 {
			sleep(containerRetryWait)
			out, _ = sys.Run(within(ctx, stopLimit), distro, "root", startScript(left))
			read(out)
		}
		for _, id := range ids {
			if !started[id] {
				e.NotRestarted++
			}
		}
		if e.NotRestarted > 0 {
			e.Status = Warned
			e.Detail += fmt.Sprintf(", %d of %d containers not started again", e.NotRestarted, len(ids))
		} else {
			e.Detail += fmt.Sprintf(", %d containers started again", len(ids))
		}
	}
	return e, nil
}

// restarted records what the start-again step e started: the containers
// that ran before and run again, and whether the distribution itself was
// started (stopped by the run, not by itself).
func (r *Result) restarted(e Event) {
	r.ContainersStarted, r.DistroStarted = e.Count-e.NotRestarted, e.DistroStarted
}

// chosenBytes is what the items chosen to remove inside take, together.
func chosenBytes(items []survey.Item) int64 {
	var n int64
	for _, it := range items {
		n += it.Bytes
	}
	return n
}

// readHooks reads what the stop script said of the person's pre-stop
// scripts: how many ran, and how many of them failed. A line of its is
// "hook|ok|NAME" or "hook|failed|NAME"; any other line is not about them.
func readHooks(out string) (hooks, failed int) {
	for line := range strings.SplitSeq(out, "\n") {
		f := strings.Split(strings.TrimSpace(line), "|")
		if f[0] == "hook" && len(f) == 3 {
			hooks++
			if f[1] == "failed" {
				failed++
			}
		}
	}
	return hooks, failed
}

// startTries are the waits before each start of a distribution a run
// starts again: WSL can refuse a start for a moment right after a
// compaction, and a distribution started can stop on its own before the run
// reads its root (a short instanceIdleTimeout). A few tries, seconds apart,
// ride over both; a distribution that does not start after them is left
// down, and the run says so.
var startTries = []time.Duration{0, 3 * time.Second, 10 * time.Second}

// containerRetryWait is how long the second round of container starts
// waits after the first.
const containerRetryWait = 5 * time.Second

// startWritable starts distro and reads whether its root is read-only,
// trying again as startTries say. It returns how many tries it took. A
// call that ran out of time is not tried again (a WSL that did not answer
// in minutes will not in seconds), nor is a name WSL does not have.
func startWritable(ctx context.Context, env Env, distro string, sleep func(time.Duration)) (ro bool, tries int, err error) {
	sys := env.Disks.WSL
	for i, wait := range startTries {
		if wait > 0 {
			sleep(wait)
		}
		tries = i + 1
		if err = sys.Start(within(ctx, callLimit), distro); err == nil {
			if ro, err = readOnly(ctx, env, distro); err == nil {
				return ro, tries, nil
			}
		}
		if errors.Is(err, wsl.ErrNoAnswer) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, wsl.ErrNoDistro) {
			return false, tries, err
		}
	}
	return false, tries, err
}

// runningNow reports whether WSL lists distro as running. A WSL that does
// not answer counts as stopped: the note this decides then errs towards
// being said.
func runningNow(ctx context.Context, sys wsl.System, distro string) bool {
	ds, err := sys.Distros(within(ctx, callLimit))
	if err != nil {
		return false
	}
	for _, d := range ds {
		if strings.EqualFold(d.Name, distro) {
			return d.Running
		}
	}
	return false
}

// startScript starts each container on its own and prints "started|id" or
// "failed|id" for it, or "running|id" for one that runs already and is
// left as it is: wsl-spawner's start, or Docker for a container with a
// restart policy, may have started it with the distribution. The ids are
// hex only (safeIDs), so they need no quoting; the running ones are read
// once, with the shell alone, as docker ps -q prints them.
func startScript(ids []string) string {
	return `running=" $(echo $(docker ps --quiet 2>/dev/null)) "
for id in ` + strings.Join(ids, " ") + `; do
  case "$running" in
  (*" $id "*) echo "running|$id" ;;
  (*) if docker start "$id" >/dev/null 2>&1; then echo "started|$id"; else echo "failed|$id"; fi ;;
  esac
done
`
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
	// The shell reads /proc/mounts itself: with awk, a distribution that has
	// none would answer nothing, and nothing reads as "writable".
	out, err := env.Disks.WSL.Run(within(ctx, callLimit), distro, "root", survey.RootOptions)
	if err != nil {
		return false, err
	}
	// The options are the last line: wsl.exe can put a warning of its own
	// before what the command printed.
	lines := strings.Split(strings.TrimSpace(out), "\n")
	opts := strings.TrimSpace(lines[len(lines)-1])
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

// Startable is the containers of ids a run starts again: the ids made of
// hex digits, all that docker start is ever given. The fronts name them
// where a person is told how to start them by hand.
func Startable(ids []string) []string { return safeIDs(ids) }

// oneLine keeps the first line of an error, for a step's detail.
func oneLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}
