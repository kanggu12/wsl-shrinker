// Package app puts the parts together for this PC: the settings, WSL, the
// disk files, the history, and the backend the screen uses.
package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kanggu12/wsl-shrinker/internal/engine/disks"
	"github.com/kanggu12/wsl-shrinker/internal/engine/history"
	"github.com/kanggu12/wsl-shrinker/internal/engine/journal"
	"github.com/kanggu12/wsl-shrinker/internal/engine/shrink"
	"github.com/kanggu12/wsl-shrinker/internal/engine/survey"
	"github.com/kanggu12/wsl-shrinker/internal/machine/spawner"
	"github.com/kanggu12/wsl-shrinker/internal/machine/winapi"
	"github.com/kanggu12/wsl-shrinker/internal/machine/wsl"
	"github.com/kanggu12/wsl-shrinker/internal/settings/config"
	"github.com/kanggu12/wsl-shrinker/internal/version"
)

// App is the program's parts for this PC.
type App struct {
	Config     config.Config
	ConfigPath string
	WSL        wsl.System
	History    history.Store
	// Journal is the run in progress, kept on disk: a lock between two
	// runs, and what a run cut off left to start again.
	Journal journal.Store
	// TerminalIn says whether this program's terminal runs inside a
	// distribution, which a shrink of it would close; nil asks the system
	// (Hosts). Tests set it, as a test binary run from WSL looks started
	// from WSL.
	TerminalIn func(distro string) bool
	// StatFile reads a disk file's size and whether a program holds it; nil
	// asks Windows (winapi.Stat). Tests set it, to hold a file as WSL does
	// while it compacts.
	StatFile func(path string) (winapi.FileInfo, error)
	// Sleep waits between the tries of a start that failed; nil waits as
	// long as each try says. Tests set it, so a start that keeps failing
	// does not cost them the waits.
	Sleep func(time.Duration)
	// FindSpawner finds wsl-spawner for each run, which then stops a
	// running distribution through it: installed while the screen was
	// open, it counts from the next run on. nil finds none, as in a test,
	// whose made-up distributions the real one must never be asked to stop.
	FindSpawner func() (spawner.Stopper, bool)
	// info is WSL's answer about itself, asked once it answers: a WSL that
	// did not answer in time is asked again the next time.
	info      wsl.Info
	infoKnown bool
	infoMu    sync.Mutex
}

// New reads the settings and sets up the parts. A settings file that does
// not load is an error: running with defaults the person did not choose
// would be a surprise.
func New() (*App, error) {
	path := config.Path(winapi.AppData())
	c, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	dir := StateDir()
	return &App{Config: c, ConfigPath: path, WSL: wsl.New(),
		History: history.Open(dir), Journal: OpenJournal(dir, winapi.Running), FindSpawner: findSpawner}, nil
}

// findSpawner is the installed wsl-spawner, if any, as a Stopper.
func findSpawner() (spawner.Stopper, bool) {
	x, ok := spawner.Find()
	return x, ok
}

// StateDir is the folder of the program's own files: the journal, the
// history, a new window's receipts and the crash files.
func StateDir() string { return filepath.Join(winapi.LocalAppData(), "wsl-shrinker") }

// OpenJournal is the journal in dir, which asks alive whether the process
// that wrote it still runs, and Windows since when, so a process id given
// again is not taken for the run's own.
func OpenJournal(dir string, alive func(pid int, exe string) bool) journal.Store {
	j := journal.Open(dir, alive)
	j.Since = winapi.ProcessStart
	return j
}

// Info asks WSL about itself, once it answers. A WSL that did not answer in
// time comes back with NoAnswer and is asked again next time: what it can do
// is not known yet, which is not the same as a WSL that cannot compact.
func (a *App) Info(ctx context.Context) wsl.Info {
	a.infoMu.Lock()
	defer a.infoMu.Unlock()
	if !a.infoKnown {
		info, err := a.WSL.Info(ctx)
		if err != nil && (info.NoAnswer || ctx.Err() != nil) {
			return wsl.Info{NoAnswer: info.NoAnswer}
		}
		a.info, a.infoKnown = info, true
	}
	return a.info
}

// DisksEnv is what disk discovery reads from.
func (a *App) DisksEnv() disks.Env {
	stat := a.StatFile
	if stat == nil {
		stat = winapi.Stat
	}
	return disks.Env{WSL: a.WSL, Stat: stat, Open: disks.OpenFile,
		LocalAppData: winapi.LocalAppData(), AppData: winapi.AppData(), Extra: a.Config.ExtraDisks}
}

// ShrinkEnv is what a run uses.
func (a *App) shrinkEnv(ctx context.Context) shrink.Env {
	// A WSL that did not answer is not refused for an old one: the run's
	// own calls to it say what is wrong, in WSL's words or as no answer.
	info := a.Info(ctx)
	env := shrink.Env{Disks: a.DisksEnv(), CompactFile: winapi.CompactFile, CanCompact: info.CanCompact || info.NoAnswer, Protected: a.Config.Protected,
		Sleep: a.Sleep}
	// Looked for at each run, not once: it may have been installed or
	// removed since this program started.
	if a.FindSpawner != nil {
		if s, ok := a.FindSpawner(); ok {
			env.Spawner = s
		}
	}
	return env
}

// Shrink runs one disk and records it in the history. The journal holds the
// run from its start to its end: another run waits, and a run cut off on
// the way leaves what it stopped for Recover.
func (a *App) Shrink(ctx context.Context, d disks.Disk, opt shrink.Options, emit func(shrink.Event)) shrink.Result {
	exe, _ := os.Executable()
	j := journal.Entry{PID: os.Getpid(), Exe: filepath.Base(exe), Started: timeNow(), Version: version.String(),
		Disk: d.Name, Distro: d.Distro, Step: string(shrink.StepCheck)}
	if err := a.Journal.Begin(j); err != nil {
		// Refused before anything ran: said as the check's failure, with a
		// code the screen has words for.
		e := shrink.Event{Step: shrink.StepCheck, Status: shrink.Failed, Detail: err.Error(), At: timeNow()}
		if errors.Is(err, journal.ErrBusy) {
			e.Code = shrink.CodeBusy
		}
		if emit != nil {
			emit(e)
		}
		res := shrink.Result{Disk: d.Name, Before: d.Estimate, Events: []shrink.Event{e}, Err: err.Error(), FailedAt: shrink.StepCheck}
		res.Left = shrink.Untouched(d)
		return res
	}
	// The journal follows the steps that matter for a run cut off.
	f := &follower{j: j, update: a.Journal.Update}
	follow := func(e shrink.Event) {
		f.see(e)
		if emit != nil {
			emit(e)
		}
	}
	res := shrink.Run(ctx, a.shrinkEnv(ctx), d, opt, follow)
	// Run returns only once it settled what it stopped: nothing is left to
	// recover. The journal goes last, after the history line, so the
	// history is written under the same lock as the run.
	defer func() { _ = a.Journal.End() }()
	e := history.Entry{At: timeNow(), Disk: res.Disk, Freed: res.Freed, Took: res.Took.Seconds(), Err: res.Err,
		Version: version.String(), FailedAt: string(res.FailedAt), Compacted: res.Compacted, Left: string(res.Left)}
	// The steps, for what happened: a few kB a run, 500 runs at most.
	if b, err := json.Marshal(res.Events); err == nil {
		e.Events = b
	}
	if res.Before != nil {
		e.Before = res.Before.FileSize
	}
	if res.File != nil {
		e.After = res.File.OnDisk
	}
	for _, it := range opt.Clean {
		e.Cleaned += it.Bytes
	}
	// A run stopped by its first check changed nothing: it is not history.
	// A history that cannot be written is said, not swallowed: the run's
	// fronts show the note.
	if res.FailedAt != shrink.StepCheck {
		if err := a.History.Add(e); err != nil {
			res.Notes["history"] = err.Error()
		}
	}
	return res
}

// Pending is a run that has not ended: one going on in another process, or
// one cut off.
func (a *App) Pending() (journal.Entry, journal.State) {
	e, st, err := a.Journal.Read()
	if err != nil {
		return journal.Entry{}, journal.None
	}
	return e, st
}

// Recover finishes a run that was cut off: its distribution started again
// if the run had stopped it, writable, with the containers it stopped. A
// compaction WSL is still running is left to end first. It says in a line
// what it did.
func (a *App) Recover(ctx context.Context) (string, error) {
	e, st, err := a.Journal.Read()
	if err != nil {
		return "", err
	}
	switch st {
	case journal.None:
		return "no run was cut off", nil
	case journal.Going:
		return "", fmt.Errorf("%s is being shrunk by another wsl-shrinker; let it finish", e.Name())
	}
	msg := "nothing was stopped"
	switch {
	case e.Disk == "" && e.Distro == "":
		// A journal that did not parse: what its run had stopped is not
		// known, so nothing can be started again. Said, not guessed.
		msg = "the journal of the run that was cut off cannot be read: what it had stopped is not known, so nothing was started again, and the run is closed"
	case e.Distro == "" && e.Disk != "":
		// A file's program (Docker Desktop) is the person's to start.
		msg = e.Disk + " is a file: start the program that uses it again"
	case e.Distro != "":
		// The disk as it is now. WSL ends a compaction it began on its own;
		// until then the disk is held and the distribution down, whether the
		// run had stopped it or found it stopped.
		var d *disks.Disk
		ds, err := disks.Find(ctx, a.DisksEnv())
		for i := range ds {
			if strings.EqualFold(ds[i].Name, e.Disk) {
				d = &ds[i]
			}
		}
		if err == nil && d == nil {
			// Unregistered since: nothing is left to start, and a journal
			// kept for it would hold every later run back.
			if err := a.Journal.End(); err != nil {
				return "", err
			}
			return e.Disk + " is no longer registered in WSL; the run that was cut off is closed", nil
		}
		if d != nil && d.File.Held && !d.Running {
			// The journal stays, for another try once WSL is done. Another
			// program may hold the disk as well, for as long as it likes:
			// the run stays cut off, and Forget is the way out of it too.
			return "", StillCutOff{fmt.Errorf("WSL is still compacting %s, or another program holds its disk; try again when it is done", e.Disk)}
		}
		// Cut off once the compaction began, before the disk was read
		// again: it is read now, and a disk that does not read cleanly stays
		// stopped, as a run leaves it. The journal goes: what is left is the
		// person's to look into.
		if d != nil && (e.Step == string(shrink.StepCompact) || e.Step == string(shrink.StepVerify)) && (d.Estimate == nil || d.Estimate.FSErrors) {
			if err := a.Journal.End(); err != nil {
				return "", err
			}
			return "", fmt.Errorf("%s was cut off after its compaction began, and its disk does not read cleanly; it stays stopped until you know why (see Troubleshooting)", e.Disk)
		}
		if !e.Stopped {
			break
		}
		env := a.shrinkEnv(ctx)
		// A wsl-spawner stop the run asked for goes on when its window
		// closes: nothing is started again under it. The run stays cut off,
		// for another try once that stop has ended the distribution.
		if busy, err := shrink.SpawnerStopping(ctx, env, e.Distro); err != nil || busy {
			if err == nil {
				err = fmt.Errorf("wsl-spawner is still stopping %s; try again once it is done", e.Distro)
			}
			return "", StillCutOff{err}
		}
		ev, err := shrink.StartAgain(ctx, env, e.Distro, e.Containers)
		if err != nil {
			// The journal stays, for another try.
			return "", StillCutOff{err}
		}
		msg = e.Disk + ": " + ev.Detail
		// Started again with its containers, it runs as long as WSL keeps it
		// running: a second line says so. The screen shows the first line.
		if shrink.Idle(ev.DistroStarted, ev.Count-ev.NotRestarted) {
			msg += "\nnote: " + shrink.IdleNote(e.Distro)
		}
	}
	if err := a.Journal.End(); err != nil {
		return "", err
	}
	return msg, nil
}

// StillCutOff is why Recover could not finish a run: it could not start
// again what the run had stopped, or the disk is still held. The run stays
// cut off, for another try, and holds every later run back until one works
// or Forget closes it.
type StillCutOff struct{ Err error }

// Error is the reason itself.
func (e StillCutOff) Error() string { return e.Err.Error() }

// Unwrap gives the reason, for errors.Is.
func (e StillCutOff) Unwrap() error { return e.Err }

// Forget closes a run that was cut off as it is, starting nothing again:
// the way out when Recover cannot finish it. It returns the run it closed,
// for the fronts to say how to start by hand what that run had stopped, and
// the state it found: None when no run was cut off, with nothing closed. A
// run going on in another process is not this one's to close. expect is
// the run the person was shown and said yes to, if any: another run cut
// off since is not forgotten in its place (ErrRunChanged).
func (a *App) Forget(expect *journal.Entry) (journal.Entry, journal.State, error) {
	e, st, err := a.Journal.Read()
	if err != nil {
		return e, st, err
	}
	switch {
	case st == journal.None:
		return e, st, nil
	case st == journal.Going:
		return e, st, fmt.Errorf("%s is being shrunk by another wsl-shrinker; let it finish", e.Name())
	case expect != nil && !e.Same(*expect):
		return e, st, ErrRunChanged
	}
	return e, st, a.Journal.End()
}

// ErrNothingToForget is the screen's Forget finding no run cut off: the
// one it asked about ended meanwhile, finished by another wsl-shrinker.
var ErrNothingToForget = errors.New("the run that was cut off had ended already; nothing was forgotten")

// ErrRunChanged is Forget refusing a run cut off other than the one the
// person was asked about: that one ended meanwhile, and this one started
// and was cut off since.
var ErrRunChanged = errors.New("the run that was cut off is not the one shown any more; nothing was forgotten")

// follower keeps the journal in step with a run: the step reached, that
// the distribution must run again from the stop on, and the containers the
// stop step stopped. An update the file refuses (another program has it
// open a moment longer than Update waits) is tried again with the next
// event, so the journal says as soon as it can what a run cut off must
// start again.
type follower struct {
	j      journal.Entry
	update func(journal.Entry) error
	// behind is set while the journal lacks a change it refused.
	behind bool
}

// see takes one event of the run.
func (f *follower) see(e shrink.Event) {
	changed := false
	// The step reached, and from the stop on, that the distribution must
	// run again.
	if e.Status == shrink.Running && e.Step != shrink.StepCheck {
		if f.j.Step != string(e.Step) {
			f.j.Step, changed = string(e.Step), true
		}
		if e.Step == shrink.StepStop && !f.j.Stopped {
			f.j.Stopped, changed = true, true
		}
	}
	// The containers, as soon as the stop step names them: before they
	// stop, so a run cut off while they stop starts them again.
	if e.Step == shrink.StepStop && len(e.Containers) > 0 && !slices.Equal(f.j.Containers, e.Containers) {
		f.j.Containers, changed = e.Containers, true
	}
	if changed || f.behind {
		f.behind = f.update(f.j) != nil
	}
}

// Backend is the screen's view of the App.
type Backend struct {
	A *App
	// Reopened is set in the window a run moved to from inside WSL.
	Reopened bool
}

// Info is WSL's version.
func (b Backend) Info(ctx context.Context) wsl.Info { return b.A.Info(ctx) }

// Disks finds the disks.
func (b Backend) Disks(ctx context.Context) ([]disks.Disk, error) {
	return disks.Find(ctx, b.A.DisksEnv())
}

// Survey looks inside a distribution.
func (b Backend) Survey(ctx context.Context, distro string) (survey.Result, error) {
	return survey.Run(ctx, b.A.WSL, distro)
}

// Shrink runs a disk.
func (b Backend) Shrink(ctx context.Context, d disks.Disk, opt shrink.Options, emit func(shrink.Event)) shrink.Result {
	return b.A.Shrink(ctx, d, opt, emit)
}

// History is the past runs.
func (b Backend) History() []history.Entry {
	all, _ := b.A.History.All()
	return all
}

// Pending is a run that has not ended, for the screen to say.
func (b Backend) Pending() (journal.Entry, journal.State) { return b.A.Pending() }

// Recover finishes a run that was cut off.
func (b Backend) Recover(ctx context.Context) (string, error) { return b.A.Recover(ctx) }

// Forget closes the run that was cut off the screen showed, starting
// nothing again.
func (b Backend) Forget(_ context.Context, run journal.Entry) error {
	_, st, err := b.A.Forget(&run)
	if err == nil && st == journal.None {
		return ErrNothingToForget
	}
	return err
}

// Protected follows the settings.
func (b Backend) Protected(name string) bool { return b.A.Config.Protected(name) }

// MinGain follows the settings.
func (b Backend) MinGain() int64 { return int64(b.A.Config.MinGainGB * (1 << 30)) }

// Hosts reports whether this program's terminal runs inside distro. The
// window a run moved to never moves again.
func (b Backend) Hosts(distro string) bool { return !b.Reopened && b.A.Hosts(distro) }

// Hosts reports whether this program's terminal runs inside distro.
func (a *App) Hosts(distro string) bool {
	if a.TerminalIn != nil {
		return a.TerminalIn(distro)
	}
	return Hosts(distro)
}

// Hosts reports whether this process was started from inside distro. Started
// from WSL, the launcher there passes its distribution's name through
// WSLENV; without it, any distribution may be this terminal's.
func Hosts(distro string) bool {
	if !winapi.FromWSL() {
		return false
	}
	here := os.Getenv("WSL_DISTRO_NAME")
	return here == "" || strings.EqualFold(here, distro)
}

// Reopen continues in a new window, and waits until it has started.
func (b Backend) Reopen(args []string) error { return Reopen(args) }

// HandoffArgs is the command line that continues a run in a new window: the
// disks in their order, each with one --clean per item it resolved (an id
// is a path, which may hold a comma, so ids are never joined). lang is the
// language to keep, "" to let the new window choose as usual. The screen
// and the command line both hand over through it.
func HandoffArgs(lang string, queue []disks.Disk, chosen map[string][]survey.Item) []string {
	args := []string{"--reopened"}
	if lang != "" {
		args = append(args, "--lang", lang)
	}
	for _, d := range queue {
		// The disk by the name it was found under.
		args = append(args, "--run", d.Name)
		for _, it := range chosen[d.Name] {
			args = append(args, "--clean", d.Name+"="+it.ID)
		}
	}
	return args
}

// handoffWait is how long Reopen waits for the new window to say it has
// started: a cold start of the terminal takes a few seconds.
var handoffWait = 15 * time.Second

// openWindow starts the new window; tests stand in for Windows.
var openWindow = winapi.Reopen

// Reopen starts this program again in a new window with args, and waits for
// that window to leave its receipt (Received): a window that did not open
// is an error said here, in the terminal the person is looking at, rather
// than a quiet end.
func Reopen(args []string) error {
	token, err := randomToken()
	if err != nil {
		return err
	}
	marker := receiptPath(token)
	// A receipt left by a window that started after an earlier wait gave
	// up goes now: none lingers past the next move.
	dropStaleReceipts(filepath.Dir(marker), time.Now().Add(-2*handoffWait))
	if err := openWindow(append(args, "--handoff", token)); err != nil {
		return err
	}
	for deadline := time.Now().Add(handoffWait); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if _, err := os.Stat(marker); err == nil {
			_ = os.Remove(marker)
			return nil
		}
	}
	return errors.New("the new window did not start; run wsl-shrinker from PowerShell instead")
}

// Received leaves the receipt Reopen waits for: the new window calls it as
// soon as it starts.
func Received(token string) error {
	p := receiptPath(token)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, nil, 0o644)
}

// dropStaleReceipts removes the receipts in dir written before before.
func dropStaleReceipts(dir string, before time.Time) {
	old, _ := filepath.Glob(filepath.Join(dir, "handoff-*"))
	for _, p := range old {
		if st, err := os.Stat(p); err == nil && st.ModTime().Before(before) {
			_ = os.Remove(p)
		}
	}
}

// receiptPath is where the receipt for token goes, next to the history.
func receiptPath(token string) string {
	return filepath.Join(StateDir(), "handoff-"+token)
}

// randomToken names one handoff, so two never take each other's receipt.
func randomToken() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
