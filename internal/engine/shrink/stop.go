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
	return stoppedItself(ids, hooks, failed, ""), nil
}

// stoppedItself is the last event of a stop this program made: the
// containers, the hooks, and those that failed. why is, when wsl-spawner is
// installed and stopped nothing, what it said; "" otherwise.
func stoppedItself(ids []string, hooks, failed int, why string) Event {
	detail := fmt.Sprintf("%d containers", len(ids))
	if hooks > 0 {
		detail += fmt.Sprintf(", %d hooks", hooks)
	}
	e := Event{Step: StepStop, Status: Done, Count: len(ids), Hooks: hooks, HooksFailed: failed, Containers: ids}
	if failed > 0 {
		e.Status = Warned
		detail += fmt.Sprintf(", %d failed", failed)
	}
	if why != "" {
		// The person set wsl-spawner up to stop distributions: what kept it
		// from this one is said, as its own words give it.
		e.Spawner = SpawnerNotStopped
		detail += "; wsl-spawner stopped nothing (" + why + "), so wsl-shrinker did"
	}
	e.Detail = detail
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
		// which it says what to change in its own settings.
		why := strings.TrimSpace(strings.TrimPrefix(err.Error(), spawner.ErrNotStopped.Error()+":"))
		why, _, _ = strings.Cut(why, "; ")
		if _, err := sys.Run(within(ctx, stopLimit), distro, "root", containersStopScript(ids, true)); err != nil {
			return Event{}, err
		}
		return stoppedItself(ids, hooks, failed, why), nil
	case err != nil:
		// It began and said no result (it was killed at the limit, or broke
		// off): what counts is whether the distribution still runs.
		return endedWithout(ctx, sys, distro, ids, hooks, failed, spawner.EndError, err.Error())
	case st.Detached:
		// It went on in the background: the compaction waits for its end.
		return stoppedInBackground(ctx, env, distro, ids, hooks, failed)
	case st.NotRunning:
		// It stopped on its own since the check: as when the list of
		// containers finds it stopped.
		return Event{}, fmt.Errorf("%s is %w", distro, wsl.ErrNotRunning)
	case st.End == spawner.EndBusy:
		return Event{}, fmt.Errorf("%w for %s, which ends it: wsl-shrinker left it to that stop", errOtherStop, distro)
	case st.End == spawner.EndClean, st.End == spawner.EndLimit, st.End == spawner.EndFailed:
		// Its stop ended, every step in its time or not, and WSL ended the
		// distribution after it.
		return stoppedBySpawner(ids, hooks, failed, st.End, ""), nil
	}
	// error, or a word this program does not know: its stop did not run to
	// its end. WSL ends the distribution after it all the same.
	return endedWithout(ctx, sys, distro, ids, hooks, failed, spawner.EndError, st.Err)
}

// stoppedBySpawner is the last event of a stop wsl-spawner made: end is its
// word for how it went, why what it said went wrong.
func stoppedBySpawner(ids []string, hooks, failed int, end, why string) Event {
	detail := fmt.Sprintf("%d containers, by wsl-spawner", len(ids))
	if hooks > 0 {
		detail += fmt.Sprintf(", %d hooks", hooks)
	}
	e := Event{Step: StepStop, Status: Done, Count: len(ids), Hooks: hooks, HooksFailed: failed, Containers: ids, By: BySpawner, Spawner: end}
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
	// Anything but a clean stop, and a hook of the person's own that
	// failed, is worth a look; the run goes on, the distribution is down.
	if end != spawner.EndClean || failed > 0 {
		e.Status = Warned
		detail += "; wsl-spawner history has the steps"
	}
	e.Detail = detail
	return e
}

// endedWithout settles a wsl-spawner stop that did not run to its end: a
// distribution it left running fails the step, and settle starts its
// containers again; one that is down goes on to the compaction, with the
// step marked for a look.
func endedWithout(ctx context.Context, sys wsl.System, distro string, ids []string, hooks, failed int, end, why string) (Event, error) {
	if runningNow(ctx, sys, distro) {
		return Event{}, fmt.Errorf("wsl-spawner's stop of %s did not run to its end, and it still runs: %s", distro, why)
	}
	return stoppedBySpawner(ids, hooks, failed, end, why), nil
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
	// the loop still ends.
	for range int(spawnerLimit / backgroundPoll) {
		if !runningNow(ctx, sys, distro) {
			e := stoppedBySpawner(ids, hooks, failed, spawner.EndClean, "")
			// How it went is in wsl-spawner's history, not known here.
			e.Spawner, e.Detail = "", strings.Replace(e.Detail, "by wsl-spawner", "by wsl-spawner in the background", 1)
			return e, nil
		}
		wait(backgroundPoll)
	}
	return Event{}, fmt.Errorf("wsl-spawner went on stopping %s in the background, and it still runs", distro)
}
