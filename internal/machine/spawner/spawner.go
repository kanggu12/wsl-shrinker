// Package spawner asks wsl-spawner, when it is installed, to stop a
// distribution the way it stops one: the person's hooks in its pre-stop.d,
// the units it is told of, then Docker and Podman with each container's own
// timeout, all within its stop_limit, and a record of the stop inside the
// distribution. WSL then ends what is left. A distribution a run stopped
// without it reads to wsl-spawner, at its next start, as one cut off.
//
// The call is wsl-spawner's own command line, `stop NAME --yes --json
// --events`, read as its documentation gives it: one JSON object per line as
// the steps happen, the results last, and its exit codes.
package spawner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Step is a step wsl-spawner began, as one of its --events lines says it.
type Step struct {
	// Name is the step: hook, units, docker, podman or sync inside the
	// distribution, or terminate, WSL ending what is left.
	Name string
	// Hook is the pre-stop file's name, for a hook.
	Hook string
	// Count is how many containers or units the step names, when it says.
	Count int
}

// StepTerminate is the step of WSL ending the distribution after the
// stop inside: wsl-spawner's terminate event, given a name of its own.
const StepTerminate = "terminate"

// Result is how wsl-spawner's stop of one distribution ended.
type Result struct {
	// End is wsl-spawner's word for it: clean, limit (a step ran out of the
	// time left), failed (a step failed), error (the stop inside did not
	// run to its end) or busy (another wsl-spawner stop of it is running,
	// which ends it). After all but busy, WSL ended the distribution.
	End string
	// Err is what wsl-spawner said went wrong with it, if anything.
	Err string
	// Took is how long wsl-spawner's stop of it took.
	Took time.Duration
	// Detached is set when wsl-spawner went on without this call, in the
	// background: it took the distribution for this terminal's own.
	Detached bool
	// NotRunning is set when wsl-spawner found it not running: nothing to
	// stop.
	NotRunning bool
}

// The ends of a stop, as wsl-spawner words them.
const (
	EndClean  = "clean"
	EndLimit  = "limit"
	EndFailed = "failed"
	EndError  = "error"
	EndBusy   = "busy"
)

// ErrNotStopped wraps a call that stopped nothing: wsl-spawner did not
// start, refused the distribution (one it leaves to WSL, say), or failed
// before its first step. What it said follows.
var ErrNotStopped = errors.New("wsl-spawner stopped nothing")

// ErrNoResult wraps a call that began stopping the distribution and ended
// without saying how its stop went: it was killed at the run's limit, or
// broke off. Whether the distribution still runs is not known from it.
var ErrNoResult = errors.New("wsl-spawner said no result")

// Why a call that ended with no result left no words of wsl-spawner's own
// to say why, as this program knows it (CallError.Quiet). The screen says
// these in its own language; wsl-spawner's own words it shows as they are.
const (
	// QuietTimeout: the call ran out of the time a run gives it, and was
	// ended.
	QuietTimeout = "timeout"
	// QuietSilent: it printed nothing of its own, and exited with Exit.
	QuietSilent = "silent"
	// QuietSystem: Windows did not run it to its end (it did not start, or
	// its end is not known); the system's words are in System.
	QuietSystem = "system"
)

// CallError is a call that ended with no result of wsl-spawner's: it
// stopped nothing (Kind ErrNotStopped), or it began and said no result
// (ErrNoResult). Error is the English the command line and the history
// say; Said and Quiet keep wsl-spawner's own words apart from what this
// program says of the call, for the screen's words.
type CallError struct {
	// Kind is ErrNotStopped or ErrNoResult.
	Kind error
	// Said is what wsl-spawner said of it, in its own words: its error, or
	// the first line it printed on stderr. "" when it said nothing.
	Said string
	// Quiet is why it said nothing (QuietTimeout, QuietSilent or
	// QuietSystem), "" when it did.
	Quiet string
	// Exit is its exit code, with QuietSilent.
	Exit int
	// System is the system's words, with QuietTimeout and QuietSystem.
	System string
}

// Error says the call's end in English: Kind's words, then wsl-spawner's,
// or what is known of an end it said nothing of.
func (e CallError) Error() string {
	switch e.Quiet {
	case QuietTimeout:
		return e.Kind.Error() + ": wsl-spawner ran out of the time a run gives it (" + e.System + ")"
	case QuietSilent:
		return fmt.Sprintf("%v: it printed nothing and exited %d", e.Kind, e.Exit)
	case QuietSystem:
		return e.Kind.Error() + ": " + e.System
	}
	return e.Kind.Error() + ": " + e.Said
}

// Unwrap gives Kind, for errors.Is.
func (e CallError) Unwrap() error { return e.Kind }

// Stopper stops one distribution through wsl-spawner and says each step it
// begins to on, in the goroutine that reads its output.
type Stopper interface {
	Stop(ctx context.Context, distro string, on func(Step)) (Result, error)
}

// Exe is wsl-spawner's program at Path.
type Exe struct {
	// Path is the program's full path.
	Path string
}

// findIn finds wsl-spawner: first on the PATH, as lookPath (exec.LookPath)
// finds it, then in each of folders, in order, as wsl-spawner.exe that is
// a file. A folder that is not a full path (made from a variable that was
// empty) is passed over: it would be looked for under whatever folder this
// program runs in. lookPath refuses a program found only in the current
// folder (exec.ErrDot), which is not an installed wsl-spawner.
func findIn(lookPath func(string) (string, error), isFile func(string) bool, folders ...string) (Exe, bool) {
	if p, err := lookPath("wsl-spawner"); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			return Exe{Path: abs}, true
		}
	}
	for _, f := range folders {
		if !filepath.IsAbs(f) {
			continue
		}
		if p := filepath.Join(f, "wsl-spawner.exe"); isFile(p) {
			return Exe{Path: p}, true
		}
	}
	return Exe{}, false
}

// installFolders are the folders wsl-spawner's installer puts it in, in the
// order Find looks after the PATH: its own per-user folder under
// %LOCALAPPDATA%\Programs, for a PATH this program started with before an
// install changed it; then an administrator's install under Program Files,
// which wsl-spawner's setup for a standard user makes, and whose folder a
// standard user's PATH may not hold.
func installFolders(localAppData, programFiles string) []string {
	return []string{filepath.Join(localAppData, "Programs", "wsl-spawner"), filepath.Join(programFiles, "wsl-spawner")}
}

// isFile reports whether path is a regular file.
func isFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}

// Args is the command line of a stop of distro: no question, its result
// as JSON, and each step as a line of JSON as it happens.
func Args(distro string) []string {
	return []string{"stop", "--yes", "--json", "--events", distro}
}

// Limits on what is kept of the program's output: its stop of one
// distribution prints a few kilobytes, and a program that printed without
// end must not fill this one's memory.
const (
	// maxLine is the longest line read; a longer one is passed over.
	maxLine = 1 << 20
	// maxErrOut is how much of what it printed on stderr is kept.
	maxErrOut = 64 << 10
)

// Stop asks wsl-spawner to stop distro, and reads how it went.
func (x Exe) Stop(ctx context.Context, distro string, on func(Step)) (Result, error) {
	cmd := exec.CommandContext(ctx, x.Path, Args(distro)...)
	// This program's environment, but the variable that names the
	// distribution a terminal runs in: wsl-spawner goes on in the
	// background, and says no result, when it names the one it stops.
	// This program's window is Windows' own, never inside the distribution
	// it stops (a run started inside one moves to a new window first), and
	// the variable came along with it from WSL.
	cmd.Env = withoutTerminalDistro(os.Environ())
	// A folder of Windows' own to run in, as wsl-spawner's own calls do: one
	// inside a distribution would be held by a program that stops it.
	if root := os.Getenv("SystemRoot"); root != "" && runtime.GOOS == "windows" {
		cmd.Dir = root
	}
	// No console window of its own on the person's screen.
	hideWindow(cmd)
	r := &reader{distro: distro, on: on}
	errOut := &capped{max: maxErrOut}
	cmd.Stdout, cmd.Stderr = r, errOut
	// Ended at its limit, it may leave something holding its output open;
	// the call returns a few seconds later all the same.
	cmd.WaitDelay = 5 * time.Second
	err := cmd.Run()
	// A last line that came without its line break counts too.
	r.flush()
	return r.outcome(ctx, err, errOut.String())
}

// withoutTerminalDistro is env without WSL_DISTRO_NAME, in any letter case
// (Windows' variable names ignore case).
func withoutTerminalDistro(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if strings.EqualFold(name, "WSL_DISTRO_NAME") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// eventLine is one --events line, the fields this program reads.
type eventLine struct {
	// Event is line (a step inside), terminate, shutdown or finished.
	Event  string `json:"event"`
	Distro string `json:"distro"`
	// Kind is a line's: plan, start, done or end.
	Kind  string `json:"kind"`
	Step  string `json:"step"`
	Hook  string `json:"hook"`
	Count int    `json:"count"`
	// Done is set on a terminate that ended.
	Done bool `json:"done"`
	// Result and Took are a finished distribution's.
	Result string  `json:"result"`
	Took   float64 `json:"took_seconds"`
	Err    string  `json:"error"`
}

// results is the last line: every distribution's result.
type results struct {
	Schema  int `json:"schema"`
	Results []struct {
		Name   string  `json:"name"`
		Result string  `json:"result"`
		Took   float64 `json:"took_seconds"`
		Err    string  `json:"error"`
	} `json:"results"`
	Err      string `json:"error"`
	Detached string `json:"detached_from"`
}

// reader reads wsl-spawner's output line by line, as it comes.
type reader struct {
	distro string
	on     func(Step)
	// buf holds what came after the last whole line.
	buf []byte
	// skipping is set while the rest of a line too long to keep is passed
	// over, up to its line break.
	skipping bool
	// began is set once a line says anything about the distribution: from
	// then on, something of it may have stopped.
	began bool
	// finished is the distribution's finished event, last is the results.
	finished *eventLine
	last     *results
}

// Write takes what the program printed and hands over each whole line.
func (r *reader) Write(p []byte) (int, error) {
	// All of it is taken, read or passed over: the program never waits on
	// this end, and an io.Writer says how much of p it took.
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			// No line break yet: keep it, unless the line is already too long.
			if !r.skipping {
				r.buf = append(r.buf, p...)
				if len(r.buf) > maxLine {
					r.buf, r.skipping = r.buf[:0], true
				}
			}
			break
		}
		if !r.skipping {
			r.buf = append(r.buf, p[:i]...)
			if len(r.buf) <= maxLine {
				r.line(r.buf)
			}
		}
		// The next line starts after this one's break.
		r.buf, r.skipping, p = r.buf[:0], false, p[i+1:]
	}
	return n, nil
}

// flush reads a last line that came without its line break.
func (r *reader) flush() {
	if !r.skipping && len(bytes.TrimSpace(r.buf)) > 0 {
		r.line(r.buf)
	}
	r.buf = nil
}

// line reads one line: an event, the results, or something else, which is
// passed over (a warning printed on stdout, say).
func (r *reader) line(b []byte) {
	b = bytes.TrimSpace(b)
	// The two kinds of line tell themselves apart by their first key's
	// name: an event has "event", the results "schema".
	var probe struct {
		Event  string `json:"event"`
		Schema int    `json:"schema"`
	}
	if json.Unmarshal(b, &probe) != nil {
		return
	}
	switch {
	case probe.Event != "":
		var e eventLine
		if json.Unmarshal(b, &e) != nil || !strings.EqualFold(e.Distro, r.distro) {
			return
		}
		r.began = true
		switch {
		case e.Event == "line" && e.Kind == "start":
			// A step inside begins.
			r.say(Step{Name: e.Step, Hook: e.Hook, Count: e.Count})
		case e.Event == "terminate" && !e.Done:
			// The stop inside is over: WSL ends what is left.
			r.say(Step{Name: StepTerminate})
		case e.Event == "finished":
			r.finished = &e
		}
	case probe.Schema != 0:
		var res results
		if json.Unmarshal(b, &res) == nil {
			r.last = &res
		}
	}
}

// say hands a step to on, if there is one.
func (r *reader) say(s Step) {
	if r.on != nil {
		r.on(s)
	}
}

// outcome reads how the call went from what the program printed and how
// it ended: runErr is cmd.Run's error, errOut what it printed on stderr.
func (r *reader) outcome(ctx context.Context, runErr error, errOut string) (Result, error) {
	// A call with no result began stopping the distribution, or stopped
	// nothing of it: whichever the lines said, its error is of that kind.
	kind := ErrNotStopped
	if r.began {
		kind = ErrNoResult
	}
	// Out of the run's time, it was killed: whatever it printed last is not
	// its end. Whether it stopped anything is all that is known.
	if ctx.Err() != nil {
		return Result{}, CallError{Kind: kind, Quiet: QuietTimeout, System: ctx.Err().Error()}
	}
	// A program that did not start, or whose exit is not known, stopped
	// nothing this call can tell of, unless it said so first: the system's
	// words say why, none of its own.
	var exited *exec.ExitError
	if runErr != nil && !errors.As(runErr, &exited) && !r.began {
		return Result{}, CallError{Kind: ErrNotStopped, Quiet: QuietSystem, System: runErr.Error()}
	}
	if r.last != nil {
		// It went on in the background, as when the variable that names the
		// terminal's distribution named this one.
		if r.last.Detached != "" {
			return Result{Detached: true}, nil
		}
		for _, d := range r.last.Results {
			if strings.EqualFold(d.Name, r.distro) {
				return Result{End: d.Result, Err: d.Err, Took: seconds(d.Took)}, nil
			}
		}
		switch {
		case r.began:
			// Steps were said, then no result for it: its end is not known.
		case r.last.Err != "":
			// Its error came before any step of this distribution's, in its
			// own words.
			return Result{}, CallError{Kind: ErrNotStopped, Said: r.last.Err}
		default:
			// Nothing to stop: it found the distribution not running.
			return Result{NotRunning: true}, nil
		}
	}
	// The results line is missing, but the distribution's finished event
	// came: that is its end.
	if r.finished != nil {
		return Result{End: r.finished.Result, Err: r.finished.Err, Took: seconds(r.finished.Took)}, nil
	}
	// Refused (exit 2: a name it does not have, or one it leaves be), failed
	// before it began (exit 1), or broke off after: its own words say why.
	if said := firstWords(errOut); said != "" {
		return Result{}, CallError{Kind: kind, Said: said}
	}
	// It said nothing on stderr: what is known of its end is said instead,
	// so the reason is never empty (a broken install, say).
	switch {
	case exited != nil:
		return Result{}, CallError{Kind: kind, Quiet: QuietSilent, Exit: exited.ExitCode()}
	case runErr != nil:
		// It began, then Windows did not say how it ended (its output held
		// open past the wait, say).
		return Result{}, CallError{Kind: kind, Quiet: QuietSystem, System: runErr.Error()}
	}
	return Result{}, CallError{Kind: kind, Quiet: QuietSilent, Exit: 0}
}

// firstWords is the first line the program printed on stderr, without the
// program's name it starts with: its error, then after "; " what to do.
func firstWords(errOut string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(strings.ReplaceAll(errOut, "\r", "")), "\n")
	return strings.TrimPrefix(strings.TrimSpace(line), "wsl-spawner: ")
}

// seconds turns a JSON duration into a time.Duration.
func seconds(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }

// capped is a buffer that keeps the first max bytes written to it and
// takes the rest without keeping it.
type capped struct {
	b   bytes.Buffer
	max int
}

// Write keeps what fits and takes all of it.
func (c *capped) Write(p []byte) (int, error) {
	if room := c.max - c.b.Len(); room > 0 {
		c.b.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

// String is what was kept.
func (c *capped) String() string { return c.b.String() }
