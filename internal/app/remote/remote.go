// Package remote is the screen's backend when the screen runs on the Linux
// side of WSL: the terminal there is a Linux one, so the screen draws and
// reads keys natively, and every answer about the PC comes from
// wsl-shrinker.exe, asked through WSL's interop with plain pipes. The
// Windows program needs no console of its own this way.
package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kanggu12/wsl-shrinker/internal/app"
	"github.com/kanggu12/wsl-shrinker/internal/engine/disks"
	"github.com/kanggu12/wsl-shrinker/internal/engine/history"
	"github.com/kanggu12/wsl-shrinker/internal/engine/journal"
	"github.com/kanggu12/wsl-shrinker/internal/engine/shrink"
	"github.com/kanggu12/wsl-shrinker/internal/engine/survey"
	"github.com/kanggu12/wsl-shrinker/internal/machine/wsl"
)

// schema is the version of the backend's JSON this side reads.
const schema = 1

// Runner runs wsl-shrinker.exe with args, writing what it prints on stdout
// to out as it comes. Its error says why the program failed, in its words.
type Runner func(ctx context.Context, args []string, out io.Writer) error

// Exe is the Runner for a wsl-shrinker.exe at path, started with env (where
// WSLENV carries this distribution's name and the language across).
func Exe(path string, env []string) Runner {
	return func(ctx context.Context, args []string, out io.Writer) error {
		// A test never starts it: a stand-in .exe would put Windows' dialog
		// for a program it cannot run on the person's screen. Tests answer
		// with a Runner of their own.
		if testing.Testing() {
			return errors.New("a test starts no program; give the backend a Runner of its own")
		}
		cmd := exec.CommandContext(ctx, path, args...)
		cmd.Env, cmd.Stdout = env, out
		var errOut bytes.Buffer
		cmd.Stderr = &errOut
		if err := cmd.Run(); err != nil {
			// The program's own words, without its name in front.
			if msg := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(errOut.String()), "wsl-shrinker:")); msg != "" {
				return errors.New(msg)
			}
			return err
		}
		return nil
	}
}

// Settings are what the screen needs from the settings file, with the
// language chosen already.
type Settings struct {
	Language     string   `json:"language"`
	Theme        string   `json:"theme"`
	MinGainBytes int64    `json:"min_gain_bytes"`
	Protect      []string `json:"protect"`
	// Mode is what a bare start opens: "tui", the screen, or "cli".
	Mode string `json:"mode"`
	// First is a PC with no settings file yet: setup comes first.
	First bool `json:"first"`
	// LanguageSetting is the language as set, "auto" too; AutoLanguage
	// what auto picks on Windows; MinGainGB the size worth shrinking; Path
	// the settings file. Setup drawn here starts from them.
	LanguageSetting string  `json:"language_setting"`
	AutoLanguage    string  `json:"auto_language"`
	MinGainGB       float64 `json:"min_gain_gb"`
	Path            string  `json:"path"`
}

// Backend answers the screen through wsl-shrinker.exe.
type Backend struct {
	run Runner
	// distro is the distribution this terminal runs in: a shrink of it
	// moves to a new window, as stopping it closes this terminal.
	distro   string
	settings Settings
}

// ErrOtherBuild is a wsl-shrinker.exe that is not this build: the screen
// then runs in it, as before, rather than talk to it in a way it may not
// know.
var ErrOtherBuild = errors.New("wsl-shrinker.exe is another build")

// Open meets the program through run: it must be this build (version), and
// its settings are read once, for the screen's colors, language and rules.
func Open(ctx context.Context, run Runner, distro, version string) (*Backend, error) {
	b := &Backend{run: run, distro: distro}
	var hello struct {
		Schema  int    `json:"schema"`
		Version string `json:"version"`
	}
	if err := b.ask(ctx, &hello, "backend", "hello"); err != nil {
		// An older build has no backend: it answers with its usage error.
		return nil, fmt.Errorf("%w: %v", ErrOtherBuild, err)
	}
	if hello.Schema != schema || hello.Version != version {
		return nil, fmt.Errorf("%w: %s, this is %s", ErrOtherBuild, hello.Version, version)
	}
	if err := b.ask(ctx, &b.settings, "backend", "settings"); err != nil {
		return nil, err
	}
	return b, nil
}

// Settings are the settings read when it opened.
func (b *Backend) Settings() Settings { return b.settings }

// ask runs one question and reads its JSON answer into v.
func (b *Backend) ask(ctx context.Context, v any, args ...string) error {
	var out bytes.Buffer
	if err := b.run(ctx, args, &out); err != nil {
		return err
	}
	return json.Unmarshal(out.Bytes(), v)
}

// Info is WSL's version and whether it can compact; NoAnswer when it does
// not say, as on Windows.
func (b *Backend) Info(ctx context.Context) wsl.Info {
	var info struct {
		Version    string `json:"version"`
		CanCompact bool   `json:"can_compact"`
		NoAnswer   bool   `json:"no_answer"`
	}
	// No answer is not knowing what WSL can do, not a WSL that cannot.
	if err := b.ask(ctx, &info, "backend", "info"); err != nil {
		return wsl.Info{NoAnswer: true}
	}
	return wsl.Info{Version: info.Version, CanCompact: info.CanCompact, NoAnswer: info.NoAnswer}
}

// Crashes takes the crashes wsl-shrinker.exe kept and did not say yet, and
// returns where they are, to be said on this side: the program's stderr
// reaches no one through the pipes. None when it does not answer.
func (b *Backend) Crashes(ctx context.Context) []string {
	var got struct {
		Shown []string `json:"shown"`
	}
	if err := b.ask(ctx, &got, "backend", "crashes"); err != nil {
		return nil
	}
	return got.Shown
}

// Disks lists the disks with their estimates.
func (b *Backend) Disks(ctx context.Context) ([]disks.Disk, error) {
	var got struct {
		Disks []disks.Disk `json:"disks"`
	}
	if err := b.ask(ctx, &got, "list", "--json"); err != nil {
		return nil, err
	}
	return got.Disks, nil
}

// Survey looks inside a running distribution. The items come without their
// commands (they are never sent): a shrink names them by id, and the
// Windows side builds the commands from a look of its own.
func (b *Backend) Survey(ctx context.Context, distro string) (survey.Result, error) {
	var got struct {
		survey.Result
	}
	err := b.ask(ctx, &got, "inside", distro, "--json")
	return got.Result, err
}

// Shrink runs one disk on the Windows side and passes its events on as
// they come. A run is not stopped with ctx: once begun, the Windows side
// finishes it, whatever happens to this screen.
func (b *Backend) Shrink(ctx context.Context, d disks.Disk, opt shrink.Options, emit func(shrink.Event)) shrink.Result {
	args := []string{"backend", "shrink", "--disk", d.Name}
	for _, it := range opt.Clean {
		args = append(args, "--clean", d.Name+"="+it.ID)
	}
	var res *shrink.Result
	var last shrink.Step
	lines := &lineWriter{line: func(l []byte) {
		var got struct {
			Event  *shrink.Event  `json:"event"`
			Result *shrink.Result `json:"result"`
		}
		if json.Unmarshal(l, &got) != nil {
			return
		}
		if got.Event != nil {
			last = got.Event.Step
			if emit != nil {
				emit(*got.Event)
			}
		}
		if got.Result != nil {
			res = got.Result
		}
	}}
	err := b.run(context.WithoutCancel(ctx), args, lines)
	lines.flush()
	if res != nil {
		return *res
	}
	// No result: refused before the run, or the program ended on the way.
	r := shrink.Result{Disk: d.Name, Before: d.Estimate, FailedAt: shrink.StepCheck, Left: untouched(d)}
	if last != "" {
		// It ended mid-run: what it left is not known here, and its journal
		// says what to start again.
		r.FailedAt, r.Left = last, shrink.LeftDown
	}
	r.Err = "the run ended without a result"
	if err != nil {
		r.Err = err.Error()
	}
	return r
}

// untouched is how a run refused before it began leaves a disk.
func untouched(d disks.Disk) shrink.Left {
	switch {
	case d.Distro == "":
		return shrink.LeftFile
	case d.Running:
		return shrink.LeftRunning
	}
	return shrink.LeftStopped
}

// History is the past runs, oldest first; none when they cannot be read.
func (b *Backend) History() []history.Entry {
	var got struct {
		Runs []history.Entry `json:"runs"`
	}
	if err := b.ask(context.Background(), &got, "history", "--json"); err != nil {
		return nil
	}
	return got.Runs
}

// Protected follows the settings, by name in any case.
func (b *Backend) Protected(name string) bool {
	for _, p := range b.settings.Protect {
		if strings.EqualFold(p, name) {
			return true
		}
	}
	return false
}

// MinGain follows the settings.
func (b *Backend) MinGain() int64 { return b.settings.MinGainBytes }

// Hosts reports whether this terminal runs inside distro: on this side the
// name is known for sure.
func (b *Backend) Hosts(distro string) bool { return strings.EqualFold(b.distro, distro) }

// Reopen continues a run in a new window on the Windows side, and returns
// once that window said it started.
func (b *Backend) Reopen(args []string) error {
	return b.run(context.Background(), append([]string{"backend", "reopen", "--"}, args...), io.Discard)
}

// Pending is a run that has not ended, from the Windows side's journal;
// none when it cannot be asked.
func (b *Backend) Pending() (journal.Entry, journal.State) {
	var got struct {
		State string        `json:"state"`
		Run   journal.Entry `json:"run"`
	}
	if err := b.ask(context.Background(), &got, "backend", "pending"); err != nil {
		return journal.Entry{}, journal.None
	}
	switch got.State {
	case "going":
		return got.Run, journal.Going
	case "cutoff":
		return got.Run, journal.CutOff
	}
	return journal.Entry{}, journal.None
}

// Recover finishes a run that was cut off, and says what it did. A start
// that failed comes back as app.StillCutOff, as it does on Windows: the
// screen offers to forget the run after one.
func (b *Backend) Recover(ctx context.Context) (string, error) {
	var got struct {
		Did         string `json:"did"`
		Error       string `json:"error"`
		StillCutOff bool   `json:"still_cut_off"`
	}
	if err := b.ask(ctx, &got, "backend", "recover"); err != nil {
		return "", err
	}
	switch {
	case got.StillCutOff:
		return "", app.StillCutOff{Err: errors.New(got.Error)}
	case got.Error != "":
		return "", errors.New(got.Error)
	}
	return got.Did, nil
}

// Forget closes the run that was cut off the screen showed, on the
// Windows side, which refuses another run cut off since. Each value is
// one argument with its flag, so an empty one survives the trip to
// Windows.
func (b *Backend) Forget(ctx context.Context, run journal.Entry) error {
	err := b.run(ctx, []string{"backend", "forget", "--pid=" + strconv.Itoa(run.PID),
		"--started=" + run.Started.Format(time.RFC3339Nano), "--disk=" + run.Disk}, io.Discard)
	// The same build says it in the same words: a run that ended meanwhile
	// comes back as itself, for the screen to tell it apart.
	if err != nil && strings.Contains(err.Error(), app.ErrNothingToForget.Error()) {
		return app.ErrNothingToForget
	}
	return err
}

// lineWriter calls line for every whole line written to it.
type lineWriter struct {
	buf  []byte
	line func([]byte)
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		w.line(w.buf[:i])
		w.buf = w.buf[i+1:]
	}
}

// flush passes on a last line that had no newline.
func (w *lineWriter) flush() {
	if len(bytes.TrimSpace(w.buf)) > 0 {
		w.line(w.buf)
	}
	w.buf = nil
}
