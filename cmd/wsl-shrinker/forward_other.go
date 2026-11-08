//go:build !windows

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/charmbracelet/x/term"

	"github.com/kanggu12/wsl-shrinker/internal/app/remote"
	"github.com/kanggu12/wsl-shrinker/internal/front/cli"
	"github.com/kanggu12/wsl-shrinker/internal/front/i18n"
	"github.com/kanggu12/wsl-shrinker/internal/front/tui"
	"github.com/kanggu12/wsl-shrinker/internal/version"
)

// forward hands the command line to wsl-shrinker.exe when this Linux build
// runs inside a WSL distribution: the disks belong to Windows, so the work
// is done there. WSLENV carries this distribution's name across ("/w": from WSL to Windows only), so the
// Windows side knows which distribution its terminal runs in. It returns
// false to run here instead (the demo and its snapshots work anywhere).
func forward() bool {
	if os.Getenv("WSL_DISTRO_NAME") == "" || slices.ContainsFunc(os.Args[1:], func(a string) bool {
		return a == "--demo" || strings.HasPrefix(a, "--snapshot")
	}) {
		return false
	}
	exe := locate()
	if exe == "" {
		// Help and the version need nothing from Windows: they are answered
		// here, so a launcher without its .exe can still say what it is.
		if answersAlone(os.Args[1:]) {
			return false
		}
		cli.PrintError(os.Stderr, "wsl-shrinker.exe was not found next to this file or on the Windows PATH", "install it in PowerShell on Windows: "+installHint)
		exit(1)
		return true
	}
	// A .exe for another processor is never started: Windows would refuse
	// it with a dialog on the desktop, not a line here. Nothing above this
	// line starts it.
	if why := wrongBuild(exe); why != "" {
		if answersAlone(os.Args[1:]) {
			return false
		}
		cli.PrintError(os.Stderr, why, "install the build for this PC in PowerShell: "+installHint)
		exit(1)
		return true
	}
	// The distribution's name, and the language chosen in this shell.
	wslenv := appendWSLENV(os.Getenv("WSLENV"), "WSL_DISTRO_NAME/w")
	wslenv = appendWSLENV(wslenv, "WSL_SHRINKER_LANG/w")
	env := withWSLENV(os.Environ(), wslenv)
	// The screen, asked for alone in a terminal, runs here, in this Linux
	// terminal, and asks wsl-shrinker.exe for every answer through pipes:
	// keys, sizes and colors stay Linux's, and the Windows program needs no
	// console. WSL_SHRINKER_SCREEN=windows keeps the screen in the .exe.
	if s, ok := screenOnly(os.Args[1:]); ok && os.Getenv("WSL_SHRINKER_SCREEN") != "windows" && inTerminal() {
		if code, ran := screenHere(exe, env, s); ran {
			exit(code)
			return true
		}
		// It could not, or CLI mode prints instead: the .exe goes on.
	}
	// Replace this process: the terminal, the exit code and Ctrl+C all
	// belong to the Windows program from here on.
	err := execve(exe, append([]string{exe}, os.Args[1:]...), env)
	cli.PrintError(os.Stderr, "could not start "+exe+": "+err.Error(), "")
	exit(1)
	return true
}

// What forward reaches the PC through: tests replace them, to see what
// would have started.
var (
	// locate finds wsl-shrinker.exe.
	locate = findExe
	// execve replaces this process with the .exe.
	execve = syscall.Exec
	// exit ends the launcher, as main ends the program.
	exit = leave
	// watch starts keeping a crash of the screen here.
	watch = cli.WatchCrashes
	// inTerminal reports whether a person types into a terminal here.
	inTerminal = func() bool { return term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd()) }
	// openRemote meets the .exe through pipes, for the screen here.
	openRemote = remote.Open
	// runChild runs the .exe in this terminal and waits for it.
	runChild = func(c *exec.Cmd) error { return c.Run() }
)

// installHint is how wsl-shrinker is installed, in PowerShell on Windows:
// the installer picks the build for the PC's processor.
const installHint = "irm https://github.com/kanggu12/wsl-shrinker/releases/latest/download/install.ps1 | iex"

// screenArgs are what a command line asks of the screen: its language and
// colors, and whether it is the screen whatever the mode (wsl-shrinker tui).
type screenArgs struct {
	lang, theme string
	tui         bool
}

// screenOnly reports whether a command line asks for the screen and
// nothing else: no command, or tui, and only --lang or --theme with a value
// they take. Anything else goes to the .exe, which also says what is wrong
// with a value it does not take.
func screenOnly(args []string) (screenArgs, bool) {
	var s screenArgs
	if len(args) > 0 && args[0] == "tui" {
		s.tui, args = true, args[1:]
	}
	for i := 0; i < len(args); i++ {
		name, value, joined := strings.Cut(args[i], "=")
		if !joined {
			// The value is the next argument.
			if i+1 >= len(args) {
				return screenArgs{}, false
			}
			value = args[i+1]
			i++
		}
		switch {
		case name == "--lang" && (value == "en" || value == "ko"):
			s.lang = value
		case name == "--theme" && (value == "dark" || value == "light"):
			s.theme = value
		default:
			return screenArgs{}, false
		}
	}
	return s, true
}

// screenHere runs the screen in this terminal, over wsl-shrinker.exe. It
// reports false, having shown nothing, when the .exe is another build or
// cannot answer, or when CLI mode prints the disks instead: the .exe goes on
// then. The first start opens setup first, in the .exe, which writes the
// settings.
func screenHere(exe string, env []string, s screenArgs) (int, bool) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	open := func() (*remote.Backend, error) {
		return openRemote(ctx, remote.Exe(exe, env), os.Getenv("WSL_DISTRO_NAME"), version.String())
	}
	b, err := open()
	if err != nil {
		return 0, false
	}
	setup, here := startAs(s, b.Settings())
	if setup {
		if code := setupFirst(exe, env, s.lang); code != 0 {
			return code, true
		}
		// Ctrl+C reached this process too: it stops here, as setup did.
		if ctx.Err() != nil {
			return 1, true
		}
		// What was chosen decides what comes next.
		if b, err = open(); err != nil {
			return 0, false
		}
		_, here = startAs(s, b.Settings())
	}
	if !here {
		return 0, false
	}
	// The language and colors as the Windows side chose them from the
	// settings, unless the command line said.
	lang, theme := s.lang, s.theme
	if lang == "" {
		lang = b.Settings().Language
	}
	i18n.Set(i18n.Choose(lang, false))
	if theme == "" {
		theme = b.Settings().Theme
	}
	// The screen runs here: a crash of it is kept on this side. Every other
	// way goes on in the .exe, which keeps its own.
	finish = watch()
	err = tui.Run(ctx, b, tui.Options{Theme: theme})
	// Crashes wsl-shrinker.exe kept answering this screen, or another
	// earlier, said here, where a person reads: its stderr never reaches one.
	if term.IsTerminal(os.Stderr.Fd()) {
		cli.SayCrashes(os.Stderr, b.Crashes(context.Background()))
	}
	if err != nil {
		cli.PrintScreenError(os.Stderr, err)
		return 1, true
	}
	return 0, true
}

// startAs says what a start asked for by s does with the settings st: setup
// first, on a first start, and whether the screen then runs here. CLI mode
// does not: the .exe prints the disks. wsl-shrinker tui is the screen,
// whatever the settings say, and opens no setup.
func startAs(s screenArgs, st remote.Settings) (setup, here bool) {
	if s.tui {
		return false, true
	}
	return st.First, st.Mode != "cli"
}

// setupFirst runs setup in the .exe, in this terminal, and returns its exit
// code. Ctrl+C reaches it through the terminal; this process waits.
func setupFirst(exe string, env []string, lang string) int {
	args := []string{"setup"}
	if lang != "" {
		args = append(args, "--lang", lang)
	}
	c := exec.Command(exe, args...)
	c.Stdin, c.Stdout, c.Stderr, c.Env = os.Stdin, os.Stdout, os.Stderr, env
	err := runChild(c)
	var exited *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exited) && exited.ExitCode() > 0:
		// It said why itself.
		return exited.ExitCode()
	case errors.As(err, &exited):
		// Ended by a signal, Ctrl+C or a closed terminal: a plain failure,
		// not the 255 an exit code of -1 would become.
		return 1
	}
	cli.PrintError(os.Stderr, "could not start "+exe+": "+err.Error(), "")
	return 1
}

// answersAlone reports whether a command line asks only for help or the
// version, which this build can answer without Windows.
func answersAlone(args []string) bool {
	if len(args) > 0 && (args[0] == "version" || args[0] == "help") {
		return true
	}
	return slices.ContainsFunc(args, func(a string) bool { return a == "--help" || a == "-h" })
}

// findExe looks next to this file first, then on the PATH, which in WSL
// includes Windows' own.
func findExe() string {
	if self, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(self), "wsl-shrinker.exe")
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	if p, err := exec.LookPath("wsl-shrinker.exe"); err == nil {
		return p
	}
	return ""
}

// withWSLENV returns env with WSLENV set to v. The entry is replaced, not
// added: with two, the first one counts, and that is the old value.
func withWSLENV(env []string, v string) []string {
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		if !strings.HasPrefix(e, "WSLENV=") {
			out = append(out, e)
		}
	}
	return append(out, "WSLENV="+v)
}

// appendWSLENV adds an entry to WSLENV's colon-separated list once.
func appendWSLENV(cur, entry string) string {
	for _, e := range strings.Split(cur, ":") {
		if e == entry {
			return cur
		}
	}
	if cur == "" {
		return entry
	}
	return cur + ":" + entry
}
