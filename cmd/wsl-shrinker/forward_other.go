//go:build !windows

package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"

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
	// So does the choice of plain marks, for a WSL that runs in the Windows
	// console's own window.
	wslenv = appendWSLENV(wslenv, "WSL_SHRINKER_SYMBOLS/w")
	// env is what the .exe answers the questions of a screen here with,
	// through pipes; started in this terminal, it gets here too.
	env := withWSLENV(os.Environ(), wslenv)
	here := relayEnv(env, wslenv, terminals(), termWidth())
	// Setup, asked for alone in a terminal, is drawn here too, for the
	// same reason as the screen: the .exe may have no console to draw on.
	if lang, ok := setupOnly(os.Args[1:]); ok && os.Getenv("WSL_SHRINKER_SCREEN") != "windows" && inTerminal() {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		b, err := openRemote(ctx, remote.Exe(exe, env), os.Getenv("WSL_DISTRO_NAME"), version.String())
		if err == nil {
			code := setupHere(ctx, b, exe, here, lang)
			stop()
			exit(code)
			return true
		}
		stop()
		// Another build: setup goes on in the .exe, which says what it can.
	}
	// The screen, asked for alone in a terminal, runs here, in this Linux
	// terminal, and asks wsl-shrinker.exe for every answer through pipes:
	// keys, sizes and colors stay Linux's, and the Windows program needs no
	// console. WSL_SHRINKER_SCREEN=windows keeps the screen in the .exe.
	if s, ok := screenOnly(os.Args[1:]); ok && os.Getenv("WSL_SHRINKER_SCREEN") != "windows" && inTerminal() {
		if code, ran := screenHere(exe, env, here, s); ran {
			exit(code)
			return true
		}
		// It could not, or CLI mode prints instead: the .exe goes on.
	}
	// Replace this process: the terminal, the exit code and Ctrl+C all
	// belong to the Windows program from here on.
	err := execve(exe, append([]string{exe}, os.Args[1:]...), here)
	cli.PrintError(os.Stderr, "could not start "+exe+": "+err.Error(), startHint(err))
	exit(1)
	return true
}

// What forward reaches the PC through: tests replace them, to see what
// would have started.
var (
	// locate finds wsl-shrinker.exe.
	locate = findExe
	// execve replaces this process with the .exe. A test never starts it:
	// a stand-in .exe would put Windows' dialog for a program it cannot run
	// on the person's screen.
	execve = func(argv0 string, argv, env []string) error {
		if testing.Testing() {
			return errNoProgramInTests
		}
		return syscall.Exec(argv0, argv, env)
	}
	// exit ends the launcher, as main ends the program.
	exit = leave
	// watch starts keeping a crash of the screen here.
	watch = cli.WatchCrashes
	// inTerminal reports whether a person types into a terminal here.
	inTerminal = func() bool { return term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd()) }
	// terminals says which of stdin, stdout and stderr are a terminal here:
	// "0", "1" and "2", in that order.
	terminals = func() string {
		fds := ""
		for i, f := range []*os.File{os.Stdin, os.Stdout, os.Stderr} {
			if term.IsTerminal(f.Fd()) {
				fds += strconv.Itoa(i)
			}
		}
		return fds
	}
	// openRemote meets the .exe through pipes, for the screen here.
	openRemote = remote.Open
	// runChild runs the .exe in this terminal and waits for it; never in a
	// test, as execve.
	runChild = func(c *exec.Cmd) error {
		if testing.Testing() {
			return errNoProgramInTests
		}
		return c.Run()
	}
	// setupScreen draws setup in this terminal.
	setupScreen = tui.RunSetup
)

// errNoProgramInTests is what starting the .exe gives in a test, whatever
// path it took to get there: tests give stand-ins for what would start.
var errNoProgramInTests = errors.New("a test starts no program; give it a stand-in")

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

// screenHere runs the screen in this terminal, over wsl-shrinker.exe, which
// answers through pipes started with env. It reports false, having shown
// nothing, when the .exe is another build or cannot answer, or when CLI mode
// prints the disks instead: the .exe goes on then. A first start draws setup
// here first; the .exe, started in this terminal with tty, writes what was
// chosen.
func screenHere(exe string, env, tty []string, s screenArgs) (int, bool) {
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
		if code := setupHere(ctx, b, exe, tty, s.lang); code != 0 {
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

// setupHere draws setup in this terminal, from the settings and disks the
// .exe answered b with, and saves what was chosen through the .exe's own
// flags, started with env. lang, from --lang, is the language it starts in.
// It returns the exit code, as setup in the .exe would.
func setupHere(ctx context.Context, b *remote.Backend, exe string, env []string, lang string) int {
	st := b.Settings()
	ds, _ := b.Disks(ctx)
	start := tui.SetupStart{Language: cmp.Or(lang, st.LanguageSetting, "auto"), Theme: cmp.Or(st.Theme, "auto"),
		Mode: cmp.Or(st.Mode, "tui"), MinGainGB: st.MinGainGB, Protect: st.Protect, Disks: ds,
		AutoLanguage: i18n.Choose(st.AutoLanguage, false), Path: st.Path, First: st.First}
	choice, saved, err := setupScreen(ctx, start)
	if err != nil {
		// Ctrl+C says it wrote nothing; a bug says it is one.
		cli.PrintScreenError(os.Stderr, err)
		return 1
	}
	if !saved {
		if !st.First {
			fmt.Println("nothing changed")
			return 0
		}
		// A first start left with esc keeps the defaults, written down, so
		// setup does not come back at every start.
		choice = tui.SetupChoice{Language: start.Language, Theme: start.Theme, Mode: start.Mode, MinGainGB: start.MinGainGB, Protect: start.Protect}
	}
	args := setupArgs(choice, st)
	if args == nil {
		fmt.Println("nothing changed")
		return 0
	}
	return runExe(exe, env, args)
}

// setupArgs are the .exe's setup flags that write choice over the settings
// st: the values that changed, so a file written by hand keeps the rest as
// it is, and the disks to protect and to protect no more. With no settings
// file yet, every value, so all of them are there next time. Nil when
// nothing changed.
func setupArgs(choice tui.SetupChoice, st remote.Settings) []string {
	var args []string
	put := func(flag, value string, changed bool) {
		if changed || st.First {
			args = append(args, flag, value)
		}
	}
	put("--language", choice.Language, choice.Language != cmp.Or(st.LanguageSetting, "auto"))
	put("--theme", choice.Theme, choice.Theme != cmp.Or(st.Theme, "auto"))
	put("--mode", choice.Mode, choice.Mode != cmp.Or(st.Mode, "tui"))
	put("--min-gain", strconv.FormatFloat(choice.MinGainGB, 'f', -1, 64), choice.MinGainGB != st.MinGainGB)
	// A disk is the same disk in any case, as in the settings.
	has := func(list []string, name string) bool {
		return slices.ContainsFunc(list, func(n string) bool { return strings.EqualFold(n, name) })
	}
	for _, p := range choice.Protect {
		if !has(st.Protect, p) {
			args = append(args, "--protect", p)
		}
	}
	for _, n := range st.Protect {
		if !has(choice.Protect, n) {
			args = append(args, "--unprotect", n)
		}
	}
	if args == nil {
		return nil
	}
	return append([]string{"setup"}, args...)
}

// setupOnly reports whether a command line is setup's screen and nothing
// else: setup, with at most --lang and a language it takes, before or after
// it. With a choice given as a flag setup opens no screen: the .exe takes
// that, as it takes any mistake, in its own words.
func setupOnly(args []string) (lang string, ok bool) {
	langs := []string{"auto", "en", "ko"}
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "setup" && !ok:
			ok = true
		case a == "--lang" && i+1 < len(args) && slices.Contains(langs, args[i+1]):
			lang = args[i+1]
			i++
		case strings.HasPrefix(a, "--lang=") && slices.Contains(langs, strings.TrimPrefix(a, "--lang=")):
			lang = strings.TrimPrefix(a, "--lang=")
		default:
			return "", false
		}
	}
	return lang, ok
}

// relayEnv is env for the .exe started in this terminal: fds says which of
// its ends a person is at ("0" stdin, "1" stdout, "2" stderr) and goes
// across as WSL_SHRINKER_TTY, with the terminal's width. Started from WSL,
// the .exe may reach this terminal only through pipes (tmux, zellij, a
// terminal whose console is gone); told so, it asks shrink's question,
// colors its lines and lays out CLI mode's table as at a console. With no
// end at a terminal, env as it is.
func relayEnv(env []string, wslenv, fds string, width int) []string {
	if fds == "" {
		return env
	}
	wslenv = appendWSLENV(appendWSLENV(wslenv, "WSL_SHRINKER_TTY/w"), "WSL_SHRINKER_COLUMNS/w")
	env = withVar(withVar(env, "WSL_SHRINKER_TTY", fds), "WSL_SHRINKER_COLUMNS", strconv.Itoa(width))
	return withWSLENV(env, wslenv)
}

// runExe runs the .exe with args in this terminal and returns its exit
// code. Ctrl+C reaches it through the terminal; this process waits.
func runExe(exe string, env []string, args []string) int {
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
	cli.PrintError(os.Stderr, "could not start "+exe+": "+err.Error(), startHint(err))
	return 1
}

// withVar is env with name set to value, replacing what it had.
func withVar(env []string, name, value string) []string {
	out := slices.DeleteFunc(slices.Clone(env), func(kv string) bool { return strings.HasPrefix(kv, name+"=") })
	return append(out, name+"="+value)
}

// termWidth is this terminal's width, 80 when it does not say.
func termWidth() int {
	if w, _, err := term.GetSize(os.Stdout.Fd()); err == nil && w > 0 {
		return w
	}
	return 80
}

// startHint says what to do when wsl-shrinker.exe could not be started. A
// distribution with WSL's interop turned off runs no Windows program: the
// kernel finds no handler for the file and says "exec format error".
func startHint(err error) string {
	if errors.Is(err, syscall.ENOEXEC) {
		return "this distribution runs no Windows programs: set enabled=true under [interop] in /etc/wsl.conf, then run wsl --shutdown in Windows"
	}
	return ""
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
