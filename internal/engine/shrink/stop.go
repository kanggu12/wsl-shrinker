package shrink

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kanggu12/wsl-shrinker/internal/machine/spawner"
	"github.com/kanggu12/wsl-shrinker/internal/machine/wsl"
)

// The stop step's two ways: this program's own (stopItself), and through
// wsl-spawner where it is installed (stopBySpawner). Both leave the
// distribution with its containers stopped and what is in memory on the
// disk; wsl-spawner's also ends the distribution, which the compaction
// would do next anyway. Both return the step's last event, or an error for
// a stop that failed, and settle then starts what was stopped again.

// errOtherStop is wsl-spawner saying another of its stops of the
// distribution is running (the sign-out hook's, or one asked by hand):
// that stop ends it, so this run neither stops it nor starts it again.
var errOtherStop = errors.New("another wsl-spawner stop is running")

// stepError is a failed stop step's error with a code the screen has words
// for (CodeOtherStop, CodeStillRuns, CodeNoState, CodeBackground,
// CodeNotEnded), and what
// was heard from wsl-spawner of it, which the screen says after its own
// words.
type stepError struct {
	code  string
	heard heard
	err   error
}

// heard is what the screen says of a call to wsl-spawner after its own
// words for the step: what wsl-spawner said, in its own words alone, or,
// when it said nothing, why, as this program knows it (spawner.QuietTimeout
// and the others), with its exit code for one that printed nothing. The
// English the command line says of the call is the step's detail.
type heard struct {
	said, quiet string
	exit        int
}

// heardOf takes a call's error apart: wsl-spawner's own words, or why it
// said none. The call's own error holds them apart (spawner.CallError); any
// other is the words of its kind, then wsl-spawner's, as another stopper (a
// test's) gives them.
func heardOf(err error) heard {
	var ce spawner.CallError
	if errors.As(err, &ce) {
		return heard{said: ce.Said, quiet: ce.Quiet, exit: ce.Exit}
	}
	s := err.Error()
	for _, kind := range []error{spawner.ErrNotStopped, spawner.ErrNoResult} {
		// The words of its kind are this program's: what follows them is
		// wsl-spawner's.
		if rest, ok := strings.CutPrefix(s, kind.Error()); ok {
			return heard{said: strings.TrimSpace(strings.TrimPrefix(rest, ":"))}
		}
	}
	return heard{said: s}
}

// on puts what was heard on the stop step's event e.
func (h heard) on(e *Event) { e.SpawnerSaid, e.SpawnerQuiet, e.SpawnerExit = h.said, h.quiet, h.exit }

// Error is the step's failure in English, for the command line and the
// history.
func (e stepError) Error() string { return e.err.Error() }

// Unwrap gives the error itself, for errors.Is.
func (e stepError) Unwrap() error { return e.err }

// spawnerLimit is how long wsl-spawner's stop may take: up to its own
// stop_limit, which can be an hour, then WSL ending the distribution. Its
// stop keeps to that limit itself; this one is for a call that never ends.
const spawnerLimit = time.Hour + 10*time.Minute

// stopItself stops the containers ids and runs the person's pre-stop
// scripts, then a sync, in distro: the stop of a run without wsl-spawner.
func stopItself(ctx context.Context, sys wsl.System, distro string, ids []string) (Event, error) {
	out, err := sys.Run(within(ctx, stopLimit), distro, "root", stopScript(ids))
	// What the script said of the hooks counts even when it failed.
	hooks, failed := readHooks(out)
	if err != nil {
		return Event{}, err
	}
	return stoppedItself(ids, hooks, failed), nil
}

// stoppedItself is the last event of a stop this program made: the
// containers, the person's pre-stop scripts, and those that failed.
func stoppedItself(ids []string, hooks, failed int) Event {
	// Named as everything else names them: one container, pre-stop scripts.
	detail := counted(len(ids), "container")
	if hooks > 0 {
		detail += ", " + counted(hooks, "pre-stop script")
	}
	e := Event{Step: StepStop, Status: Done, Count: len(ids), Hooks: hooks, HooksFailed: failed, Containers: ids}
	if failed > 0 {
		e.Status = Warned
		detail += fmt.Sprintf(", %d failed", failed)
	}
	e.Detail = detail
	return e
}

// noReason stands in for what wsl-spawner said when it said nothing: the
// step still says it stopped nothing, so it never reads as a run without
// wsl-spawner.
const noReason = "it gave no reason"

// stoppedItselfInstead is the last event of a stop this program made
// because wsl-spawner, installed, stopped nothing: what kept it from this
// distribution is said in English, why (its own words, what is known of a
// call that said none, or noReason), and for the screen as h heard it.
func stoppedItselfInstead(ids []string, hooks, failed int, why string, h heard) Event {
	if why == "" {
		why = noReason
	}
	e := stoppedItself(ids, hooks, failed)
	e.Spawner = SpawnerNotStopped
	h.on(&e)
	e.Detail += "; wsl-spawner stopped nothing (" + why + "), so wsl-shrinker did"
	return e
}

// stopBySpawner stops distro through wsl-spawner: the person's own pre-stop
// scripts first (wsl-spawner ends the distribution when its stop is done,
// so nothing runs in it after), then wsl-spawner's stop, each of whose
// steps say tells as it begins. A wsl-spawner that stopped nothing (it
// leaves the distribution to WSL, say, or did not start) leaves the stop to
// this program, as without it.
func stopBySpawner(ctx context.Context, env Env, distro string, ids []string, say func(Event)) (Event, error) {
	sys := env.Disks.WSL
	out, err := sys.Run(within(ctx, stopLimit), distro, "root", hooksScript)
	hooks, failed := readHooks(out)
	if err != nil {
		return Event{}, err
	}
	st, err := env.Spawner.Stop(within(ctx, spawnerLimit), distro, func(s spawner.Step) {
		// The step under way, for the screen's row: the stop goes on as
		// one step of this run's.
		say(Event{Step: StepStop, Status: Running, By: BySpawner, Phase: s.Name})
	})
	switch {
	case errors.Is(err, spawner.ErrNotStopped):
		// Nothing of it stopped: the containers stop as without wsl-spawner,
		// the hooks run already. What it said is kept up to its "; ", after
		// which it says what to change in its own settings: in the English,
		// and in its own words for the screen.
		why := strings.TrimSpace(strings.TrimPrefix(err.Error(), spawner.ErrNotStopped.Error()+":"))
		why, _, _ = strings.Cut(why, "; ")
		h := heardOf(err)
		h.said, _, _ = strings.Cut(h.said, "; ")
		if _, err := sys.Run(within(ctx, stopLimit), distro, "root", containersStopScript(ids, true)); err != nil {
			return Event{}, err
		}
		return stoppedItselfInstead(ids, hooks, failed, why, h), nil
	case err != nil:
		// It began and said no result (it was killed at the limit, or broke
		// off): what counts is whether the distribution still runs.
		return afterPartialStop(ctx, sys, distro, ids, hooks, failed, spawner.EndError, err.Error(), heardOf(err))
	case st.Detached:
		// It went on in the background: the compaction waits for its end.
		return stoppedInBackground(ctx, env, distro, ids, hooks, failed)
	case st.NotRunning:
		// It stopped on its own since the check: as when the list of
		// containers finds it stopped.
		return Event{}, fmt.Errorf("%s is %w", distro, wsl.ErrNotRunning)
	case st.End == spawner.EndBusy:
		return Event{}, stepError{code: CodeOtherStop, err: fmt.Errorf("%w for %s, which ends it: wsl-shrinker left it to that stop", errOtherStop, distro)}
	case st.Err == "" && (st.End == spawner.EndClean || st.End == spawner.EndLimit || st.End == spawner.EndFailed):
		// Its stop ended, every step in its time or not, and WSL ended the
		// distribution after it: it said nothing went wrong past its steps.
		return stoppedBySpawner(ids, hooks, failed, st.End, ""), nil
	}
	// It said something went wrong: its stop did not run to its end
	// (error, or a word this program does not know), or WSL did not end the
	// distribution after it (an error with any end). Whether it still runs
	// decides.
	end := st.End
	if end != spawner.EndClean && end != spawner.EndLimit && end != spawner.EndFailed {
		end = spawner.EndError
	}
	return afterPartialStop(ctx, sys, distro, ids, hooks, failed, end, st.Err, heard{said: st.Err})
}

// stoppedBySpawner is the last event of a stop wsl-spawner made: end is its
// word for how it went, why what it said went wrong.
func stoppedBySpawner(ids []string, hooks, failed int, end, why string) Event {
	// The same words as this program's own stop, and who stopped it.
	detail := counted(len(ids), "container") + ", by wsl-spawner"
	if hooks > 0 {
		detail += ", " + counted(hooks, "pre-stop script")
	}
	e := Event{Step: StepStop, Status: Done, Count: len(ids), Hooks: hooks, HooksFailed: failed, Containers: ids, By: BySpawner, Spawner: end, SpawnerSaid: why}
	if failed > 0 {
		detail += fmt.Sprintf(", %d failed", failed)
	}
	switch end {
	case spawner.EndLimit:
		detail += ": a step ran out of time"
	case spawner.EndFailed:
		detail += ": a step failed"
	case spawner.EndError:
		detail += ": its stop did not run to its end"
		if why != "" {
			detail += " (" + why + ")"
		}
	}
	if why != "" && end != spawner.EndError {
		// Its steps went as end says, then something else went wrong (WSL
		// did not end it, say): said in its own words.
		detail += ": " + why
	}
	if end == spawner.EndLimit || end == spawner.EndFailed || end == spawner.EndError {
		// A step of its own did not go in its time: its history has them.
		// Not for a script of the person's own, which its history does not
		// hold.
		detail += "; wsl-spawner history has the steps"
	}
	// Anything but a clean stop, an error wsl-spawner said, and a hook of
	// the person's own that failed, is worth a look; the run goes on, the
	// distribution is down.
	if end != spawner.EndClean || why != "" || failed > 0 {
		e.Status = Warned
	}
	e.Detail = detail
	return e
}

// afterPartialStop settles a wsl-spawner stop that said something went
// wrong: its stop inside did not run to its end, WSL did not end the
// distribution after it, or it said no result. One that is down goes on to
// the compaction, the step marked for a look. One that still runs, or whose
// state WSL does not say, fails the step, and is ended here first:
// wsl-spawner may have stopped Docker's daemon through systemd, its units
// and Podman, which only a fresh start of the distribution brings back,
// with wsl-spawner's own start where it is set up; the start settle makes
// only starts containers. why is what went wrong in English, h what the
// screen says of it.
func afterPartialStop(ctx context.Context, sys wsl.System, distro string, ids []string, hooks, failed int, end, why string, h heard) (Event, error) {
	state, serr := stateOf(ctx, sys, distro)
	if state == stateStopped {
		e := stoppedBySpawner(ids, hooks, failed, end, why)
		// For the screen, wsl-spawner's words alone, or why there are none.
		h.on(&e)
		return e, nil
	}
	// Ended as wsl-spawner's stop would have ended it (endIt), or said to
	// be one that could not be.
	if state == stateUnknown {
		ended, code := endIt(ctx, sys, distro, CodeNoState)
		return Event{}, stepError{code: code, heard: h, err: fmt.Errorf("after wsl-spawner's stop (%s), WSL did not say whether %s still runs (%v); %s", whyOr(why), distro, serr, ended)}
	}
	ended, code := endIt(ctx, sys, distro, CodeStillRuns)
	return Event{}, stepError{code: code, heard: h, err: fmt.Errorf("wsl-spawner's stop of %s did not end it (%s), and it still ran; %s", distro, whyOr(why), ended)}
}

// endIt ends distro, which wsl-spawner's stop left running or of which WSL
// did not say, as that stop would have ended it, so that it starts afresh.
// It says how that went in English, and gives the failed step's code: code,
// or CodeNotEnded when WSL did not end it either, which the screen then
// says rather than that it was ended. settle's start then finds it running.
func endIt(ctx context.Context, sys wsl.System, distro, code string) (string, string) {
	if err := sys.Terminate(within(ctx, callLimit), distro); err != nil {
		return "wsl-shrinker could not end it either (" + oneLine(err.Error()) + ")", CodeNotEnded
	}
	return "wsl-shrinker ended it so that it starts afresh", code
}

// whyOr is what wsl-spawner said, or that it said nothing.
func whyOr(why string) string {
	if why == "" {
		return noReason
	}
	return why
}

// distroState is whether a distribution runs, as WSL lists it.
type distroState int

const (
	// stateUnknown: WSL did not answer, or no longer lists the
	// distribution. Never taken for stopped: a run would compact a disk on
	// a guess.
	stateUnknown distroState = iota
	stateRunning
	stateStopped
)

// stateOf asks WSL whether distro runs, with why it could not say.
func stateOf(ctx context.Context, sys wsl.System, distro string) (distroState, error) {
	ds, err := sys.Distros(within(ctx, callLimit))
	if err != nil {
		return stateUnknown, err
	}
	for _, d := range ds {
		if strings.EqualFold(d.Name, distro) {
			if d.Running {
				return stateRunning, nil
			}
			return stateStopped, nil
		}
	}
	return stateUnknown, fmt.Errorf("%w: %s", wsl.ErrNoDistro, distro)
}

// backgroundPoll is how often a run looks whether wsl-spawner's stop that
// went on in the background has ended the distribution: a stop takes
// seconds to minutes.
const backgroundPoll = 2 * time.Second

// stoppedInBackground waits for wsl-spawner's stop that went on in the
// background to end the distribution, as long as such a stop may take.
func stoppedInBackground(ctx context.Context, env Env, distro string, ids []string, hooks, failed int) (Event, error) {
	sys := env.Disks.WSL
	wait := sleep(env)
	// Counted in looks, not by the clock: a test's waits take no time, and
	// the loop still ends. A look WSL does not answer is looked at again.
	for range int(spawnerLimit / backgroundPoll) {
		if state, _ := stateOf(ctx, sys, distro); state == stateStopped {
			e := stoppedBySpawner(ids, hooks, failed, spawner.EndClean, "")
			// How it went is in wsl-spawner's history, not known here.
			e.Spawner, e.Detail = "", strings.Replace(e.Detail, "by wsl-spawner", "by wsl-spawner in the background", 1)
			return e, nil
		}
		wait(backgroundPoll)
	}
	// Still not down after as long as such a stop may take: ended here, as
	// that stop would have ended it, so it starts afresh.
	ended, code := endIt(ctx, sys, distro, CodeBackground)
	return Event{}, stepError{code: code, err: fmt.Errorf("wsl-spawner went on stopping %s in the background, and it still ran; %s", distro, ended)}
}

// spawnerLockScript looks at wsl-spawner's stop lock in a distribution, as
// wsl-spawner's own stop takes it, under root ("" for the distribution
// itself; a test's folder otherwise, never quoted, so without a quote). It
// prints "spawner|stopping" while a stop of wsl-spawner's is at work. With
// flock, the stop holds /run/wsl-spawner/stop.lock for as long as it runs:
// a lock this look cannot take is held. It takes it as wsl-spawner does, on
// a descriptor of the shell's (no program to run but flock), and lets go
// at once: a stop that began in that instant would find it busy. Without
// flock, the stop keeps a folder there with its pid: at work while that
// process runs. No lock file, or no folder, is no stop. It ends with
// success whatever it found (a stop's folder left with its process gone
// would otherwise end it with kill's failure, which wsl.exe passes on as
// an error). Only the shell's own words and flock.
func spawnerLockScript(root string) string {
	return `l='` + root + `/run/wsl-spawner'
if command -v flock >/dev/null 2>&1; then
  if { command exec 9<"$l/stop.lock"; } 2>/dev/null; then
    flock -n 9 || echo "spawner|stopping"
    exec 9<&-
  fi
elif [ -d "$l/stop" ]; then
  pid=""
  { read -r pid <"$l/stop/pid"; } 2>/dev/null
  case "$pid" in '' | *[!0-9]*) ;; *) kill -0 "$pid" 2>/dev/null && echo "spawner|stopping" ;; esac
fi
:
`
}

// SpawnerStopping reports whether a wsl-spawner stop of distro is at work
// in it now: one a run asked for goes on when the run's window closes, and
// what the run had stopped is not started again under it. Without
// wsl-spawner (env.Spawner nil) there is none; a distribution that does not
// run has none either, its stop having ended it.
func SpawnerStopping(ctx context.Context, env Env, distro string) (bool, error) {
	if env.Spawner == nil {
		return false, nil
	}
	out, err := env.Disks.WSL.Run(within(ctx, callLimit), distro, "root", spawnerLockScript(""))
	switch {
	case errors.Is(err, wsl.ErrNotRunning):
		return false, nil
	case err != nil:
		return false, err
	}
	// The look's own line, among any wsl.exe put before it.
	for line := range strings.SplitSeq(out, "\n") {
		if strings.TrimSpace(line) == "spawner|stopping" {
			return true, nil
		}
	}
	return false, nil
}
