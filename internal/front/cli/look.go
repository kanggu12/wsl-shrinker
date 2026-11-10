package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/kanggu12/wsl-shrinker/internal/front/i18n"
	"github.com/kanggu12/wsl-shrinker/internal/front/tui"
	"github.com/kanggu12/wsl-shrinker/internal/front/ui"
	"github.com/kanggu12/wsl-shrinker/internal/machine/winapi"
)

// markSet decides the marks for this run. WSL_SHRINKER_SYMBOLS (unicode,
// plain or ascii), when set, says. Otherwise the Windows console's own window
// gets plain marks, since its fonts lack the others, and all ASCII on an East
// Asian code page, where it draws ambiguous characters two cells wide; any
// other terminal gets the marks themselves.
func markSet(setting string, classic, eastAsian bool) ui.MarkSet {
	switch setting {
	case "unicode":
		return ui.MarksUnicode
	case "plain":
		return ui.MarksPlain
	case "ascii":
		return ui.MarksASCII
	}
	switch {
	case classic && eastAsian:
		return ui.MarksASCII
	case classic:
		return ui.MarksPlain
	}
	return ui.MarksUnicode
}

// consoleInWSL guesses, inside WSL, whether the terminal is the Windows
// console's own window, which the Linux side cannot ask: WSL started from the
// Start menu or a shell on Windows 10 runs there, while Windows Terminal,
// VS Code, tmux, zellij and SSH each say they are there in the environment.
// A guess that is wrong costs plain marks in a terminal that had fonts for
// the others; one that is missed, boxes.
func consoleInWSL(getenv func(string) string) bool {
	if runtime.GOOS == "windows" || getenv("WSL_DISTRO_NAME") == "" {
		return false
	}
	for _, v := range []string{"WT_SESSION", "TERM_PROGRAM", "TMUX", "ZELLIJ", "SSH_CONNECTION", "SSH_TTY"} {
		if getenv(v) != "" {
			return false
		}
	}
	return true
}

// SetMarks sets the marks this process draws, on the screen, in setup and
// in the commands, for the terminal it runs in. main calls it first.
func SetMarks() {
	classic := winapi.ClassicConsole() || (stdoutIsTerminal() && consoleInWSL(os.Getenv))
	ui.SetMarks(markSet(os.Getenv("WSL_SHRINKER_SYMBOLS"), classic, winapi.ConsoleEastAsian()))
	// Every text the screen measures takes them before it is measured.
	i18n.SetRewrite(ui.Words)
}

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
	// A terminal WSL's launcher writes this program's pipes to takes colors
	// as the console would.
	tty := ok && (winapi.Colors(f.Fd()) || relayedFile(f))
	return look{on: tty && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"}
}

// relayedFile reports whether f is stdout or stderr at a terminal WSL's
// launcher relays (relayed).
func relayedFile(f *os.File) bool {
	return f == os.Stdout && relayed(1) || f == os.Stderr && relayed(2)
}

// paint colors s with one of the terminal's sixteen colors. The marks in
// it are the console's plain ones where its font has none of them.
func (l look) paint(c, s string) string {
	// Colors or not, a mark the console has no font for is swapped.
	s = ui.Words(s)
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

// cat is the paint for a disk's category, the screen's colors in the
// terminal's own: worth it teal, in use blue, protected magenta, cannot
// red, and little grey.
func (l look) cat(c ui.Category) func(string) string {
	switch c {
	case ui.CatGain:
		return l.gain
	case ui.CatLive:
		return l.live
	case ui.CatSafe:
		return l.safe
	case ui.CatSmall:
		return l.muted
	}
	return l.bad
}

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
