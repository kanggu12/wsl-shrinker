package cli

import (
	"errors"
	"fmt"
	"io"
	"os"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/kanggu12/wsl-shrinker/internal/front/tui"
	"github.com/kanggu12/wsl-shrinker/internal/machine/winapi"
)

// look is how the command line draws on a terminal: marks, and colors that
// mean what they mean on the screen (teal gives back or done, indigo runs or
// is in use, pink is protected, amber is worth a look, red cannot or
// failed), in the terminal's own sixteen colors so they suit a light or a
// dark one. Piped, or with NO_COLOR set, it draws plain text a script can
// read, the same words without marks or colors.
type look struct{ on bool }

// lookFor is the look for writing to w: on for a terminal that takes colors,
// off for a pipe, a file, NO_COLOR, or a terminal that says it is dumb.
func lookFor(w io.Writer) look {
	f, ok := w.(*os.File)
	return look{on: ok && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb" && winapi.Colors(f.Fd())}
}

// paint colors s with one of the terminal's sixteen colors.
func (l look) paint(c, s string) string {
	if !l.on || s == "" {
		return s
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(c)).Render(s)
}

func (l look) gain(s string) string  { return l.paint("6", s) }
func (l look) live(s string) string  { return l.paint("4", s) }
func (l look) safe(s string) string  { return l.paint("5", s) }
func (l look) warn(s string) string  { return l.paint("3", s) }
func (l look) bad(s string) string   { return l.paint("1", s) }
func (l look) muted(s string) string { return l.paint("8", s) }

// done is s after a teal check mark on a terminal, plain otherwise.
func (l look) done(s string) string {
	if !l.on {
		return s
	}
	return l.gain("✓") + " " + s
}

// bold is s in bold.
func (l look) bold(s string) string {
	if !l.on {
		return s
	}
	return lipgloss.NewStyle().Bold(true).Render(s)
}

// hinted is an error with what to do about it. Plain, it reads as one line,
// the hint in brackets; on a terminal the hint goes on a line of its own.
type hinted struct {
	err  error
	hint string
}

func (h hinted) Error() string { return h.err.Error() + " (" + h.hint + ")" }

// Unwrap gives the error itself, for errors.Is.
func (h hinted) Unwrap() error { return h.err }

// hint adds what to do to an error.
func hint(err error, what string) error { return hinted{err, what} }

// printErr says why a command line ended, on errOut: "✗ what went wrong"
// and "→ what to do" on a terminal, one plain line otherwise.
func printErr(errOut io.Writer, err error) { printErrLook(lookFor(errOut), errOut, err) }

// printErrLook is printErr in a look given.
func printErrLook(l look, errOut io.Writer, err error) {
	var h hinted
	msg, what := err.Error(), ""
	if errors.As(err, &h) {
		msg, what = h.err.Error(), h.hint
	}
	// "usage: " marks a mistake in the command line; it is not said.
	msg = trimUsage(msg)
	if !l.on {
		fmt.Fprintln(errOut, "wsl-shrinker:", trimUsage(err.Error()))
		return
	}
	fmt.Fprintln(errOut, l.bad("✗")+" "+msg)
	if what != "" {
		fmt.Fprintln(errOut, "  "+l.muted("→ "+what))
	}
}

// PrintError says what went wrong and what to do (what may be ""), as every
// command does: for the launcher, which can fail before any command runs.
func PrintError(errOut io.Writer, msg, what string) {
	var err error = errors.New(msg)
	if what != "" {
		err = hint(err, what)
	}
	printErr(errOut, err)
}

// IssuesURL is where a bug is reported.
const IssuesURL = "https://github.com/kanggu12/wsl-shrinker/issues"

// screenBug says a screen that stopped on a bug did so, in the program's
// words, with where to report it, under where it happened: Bubble Tea
// prints that itself for the screen's own code, and a run's goroutine
// hands it over, written here to errOut. Any other error is as it was.
func screenBug(err error, errOut io.Writer) error {
	var bug *tui.BugError
	switch {
	case errors.As(err, &bug):
		errOut.Write(bug.Stack)
		return hint(fmt.Errorf("the screen stopped on a bug: %v", bug.Value), "please report it with the lines above: "+IssuesURL)
	case errors.Is(err, tea.ErrProgramPanic):
		return hint(errors.New("the screen stopped on a bug"), "please report it with the lines above: "+IssuesURL)
	}
	return err
}

// PrintScreenError says how a screen ended, when it ended on an error, as
// the commands say theirs: for the launcher, whose screen runs on the Linux
// side.
func PrintScreenError(errOut io.Writer, err error) {
	printErr(errOut, screenBug(err, errOut))
}
