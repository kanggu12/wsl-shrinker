package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/kanggu12/wsl-shrinker/internal/app"
	"github.com/kanggu12/wsl-shrinker/internal/engine/disks"
	"github.com/kanggu12/wsl-shrinker/internal/engine/shrink"
	"github.com/kanggu12/wsl-shrinker/internal/front/i18n"
	"github.com/kanggu12/wsl-shrinker/internal/front/tui"
	"github.com/kanggu12/wsl-shrinker/internal/settings/config"
)

// inTerminal makes the test's command line look started in a terminal, with
// the two screens replaced: opened reports which ran, and setup answers
// with choice (saved or not). Everything is put back after the test.
func inTerminal(t *testing.T, choice tui.SetupChoice, saved bool) *[]string {
	t.Helper()
	opened := &[]string{}
	in, out, screen, setup := stdinIsTerminal, stdoutIsTerminal, openScreen, openSetup
	stdinIsTerminal = func() bool { return true }
	stdoutIsTerminal = func() bool { return true }
	openScreen = func(context.Context, tui.Backend, tui.Options) error {
		*opened = append(*opened, "screen")
		return nil
	}
	openSetup = func(_ context.Context, start tui.SetupStart) (tui.SetupChoice, bool, error) {
		*opened = append(*opened, "setup")
		if start.First {
			*opened = append(*opened, "first")
		}
		return choice, saved, nil
	}
	t.Cleanup(func() {
		stdinIsTerminal, stdoutIsTerminal, openScreen, openSetup = in, out, screen, setup
		// A choice of language in a test is not the next test's.
		i18n.Set(i18n.English)
	})
	return opened
}

// withSettings gives a its settings file's path in the test's folder,
// written with body unless body is "".
func withSettings(t *testing.T, a *app.App, body string) string {
	t.Helper()
	a.ConfigPath = filepath.Join(t.TempDir(), "wsl-shrinker", "config.toml")
	if body != "" {
		if err := os.MkdirAll(filepath.Dir(a.ConfigPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(a.ConfigPath, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		c, err := config.Load(a.ConfigPath)
		if err != nil {
			t.Fatal(err)
		}
		a.Config = c
	}
	return a.ConfigPath
}

func TestTheFirstStartOpensSetupThenGoesOnAsChosen(t *testing.T) {
	// No settings file: setup comes first, and what it chose decides what
	// comes next, here CLI mode's overview.
	a, _ := flowApp(t)
	path := withSettings(t, a, "")
	opened := inTerminal(t, tui.SetupChoice{Language: "en", Theme: "dark", Mode: "cli", MinGainGB: 2}, true)
	code, out, errOut := shrinkWith(a)
	if code != exitOK || strings.Join(*opened, " ") != "setup first" {
		t.Fatalf("exit %d, opened %v: %s\n%s", code, *opened, errOut, out)
	}
	c, err := config.Load(path)
	if err != nil || c.Mode != "cli" || c.Theme != "dark" || c.MinGainGB != 2 {
		t.Fatalf("%+v %v", c, err)
	}
	// The overview: the disk, how much it gives back, the commands with its name.
	for _, want := range []string{"saved: " + path, "Dev", "wsl-shrinker shrink Dev", "wsl-shrinker inside Dev", "wsl-shrinker tui"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
	// The second start: the settings are there, setup does not come back.
	*opened = nil
	if code, out, _ := shrinkWith(a); code != exitOK || len(*opened) != 0 || !strings.Contains(out, "wsl-shrinker shrink") {
		t.Fatalf("exit %d, opened %v:\n%s", code, *opened, out)
	}
}

func TestLeavingTheFirstSetupKeepsTheDefaults(t *testing.T) {
	// Esc on the first start writes the defaults down, so setup does not
	// come back at every start, and the screen opens, as TUI is the default.
	a, _ := flowApp(t)
	path := withSettings(t, a, "")
	opened := inTerminal(t, tui.SetupChoice{}, false)
	if code, out, errOut := shrinkWith(a); code != exitOK || strings.Join(*opened, " ") != "setup first screen" {
		t.Fatalf("exit %d, opened %v: %s\n%s", code, *opened, errOut, out)
	}
	c, err := config.Load(path)
	if err != nil || c.Mode != "tui" || c.Language != "auto" || c.MinGainGB != 1 {
		t.Fatalf("%+v %v", c, err)
	}
	// The file is the commented example with the values in it, not bare keys.
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "# ") || !strings.Contains(string(b), `mode = "tui"`) {
		t.Fatalf("%s", b)
	}
}

func TestEachModeOpensItsOwnAndTuiAlwaysTheScreen(t *testing.T) {
	a, _ := flowApp(t)
	withSettings(t, a, "mode = \"cli\"\n")
	opened := inTerminal(t, tui.SetupChoice{}, false)
	// CLI mode prints; nothing opens.
	if code, out, _ := shrinkWith(a); code != exitOK || len(*opened) != 0 || !strings.Contains(out, "wsl-shrinker shrink Dev") {
		t.Fatalf("exit %d, opened %v:\n%s", code, *opened, out)
	}
	// tui opens the screen whatever the mode.
	if code, _, errOut := shrinkWith(a, "tui"); code != exitOK || strings.Join(*opened, " ") != "screen" {
		t.Fatalf("exit %d, opened %v: %s", code, *opened, errOut)
	}
	// TUI mode opens the screen.
	*opened = nil
	a.Config.Mode = "tui"
	if code, _, _ := shrinkWith(a); code != exitOK || strings.Join(*opened, " ") != "screen" {
		t.Fatalf("exit %d, opened %v", code, *opened)
	}
	// The window a run moved to goes on with the run, on the screen, in
	// CLI mode too.
	*opened = nil
	a.Config.Mode = "cli"
	if code, _, _ := shrinkWith(a, "--reopened", "--run", "Dev"); code != exitOK || strings.Join(*opened, " ") != "screen" {
		t.Fatalf("exit %d, opened %v", code, *opened)
	}
}

func TestWithoutATerminalNothingIsAskedOrOpened(t *testing.T) {
	// A pipe gets the list, whatever the mode, and no setup on a first start.
	a, _ := flowApp(t)
	// go test in a folder may hand the test binary the terminal itself.
	in, out := stdinIsTerminal, stdoutIsTerminal
	stdinIsTerminal = func() bool { return false }
	stdoutIsTerminal = func() bool { return false }
	t.Cleanup(func() { stdinIsTerminal, stdoutIsTerminal = in, out })
	path := withSettings(t, a, "")
	if code, out, _ := shrinkWith(a); code != exitOK || !strings.HasPrefix(out, "DISK") || exists(path) {
		t.Fatalf("exit %d, settings written %v:\n%s", code, exists(path), out)
	}
	// setup and tui say what to do instead.
	code, _, errOut := shrinkWith(a, "setup")
	if code != exitUsage || !strings.Contains(errOut, "setup needs a terminal for its screen (or give the choices as flags: wsl-shrinker setup --mode cli)") {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	code, _, errOut = shrinkWith(a, "tui")
	if code != exitUsage || !strings.Contains(errOut, "the screen needs a terminal (wsl-shrinker list prints the disks without one)") {
		t.Fatalf("exit %d: %s", code, errOut)
	}
}

func TestSetupTakesItsChoicesAsFlags(t *testing.T) {
	// Flags are the choices: no screen, and what they leave out stays.
	a, _ := flowApp(t)
	path := withSettings(t, a, "# mine\ntheme = \"light\"\nprotect = [\"Old\"]\n")
	code, out, errOut := shrinkWith(a, "setup", "--mode", "cli", "--min-gain", "5", "--protect", "Work", "--language", "ko")
	if code != exitOK || !strings.Contains(out, "saved: "+path) {
		t.Fatalf("exit %d: %s\n%s", code, errOut, out)
	}
	c, err := config.Load(path)
	if err != nil || c.Mode != "cli" || c.MinGainGB != 5 || c.Language != "ko" || c.Theme != "light" || strings.Join(c.Protect, ",") != "Old,Work" {
		t.Fatalf("%+v %v", c, err)
	}
	b, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(b), "# mine\n") {
		t.Fatalf("the file was not kept as written:\n%s", b)
	}
	// --unprotect takes a disk off by name in any case.
	a.Config = c
	if code, _, errOut := shrinkWith(a, "setup", "--unprotect", "old"); code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if c, _ := config.Load(path); strings.Join(c.Protect, ",") != "Work" {
		t.Fatalf("%v", c.Protect)
	}
	// A value the program would refuse is not written.
	if code, _, errOut := shrinkWith(a, "setup", "--mode", "gui"); code != exitUsage || !strings.Contains(errOut, "--mode takes tui or cli") {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if c, err := config.Load(path); err != nil || c.Mode != "cli" {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestTheInstallerKeepsSettingsThatExist(t *testing.T) {
	// An upgrade opens no screen: the settings stay as they are.
	a, _ := flowApp(t)
	path := withSettings(t, a, "mode = \"cli\"\n")
	opened := inTerminal(t, tui.SetupChoice{}, false)
	if code, out, _ := shrinkWith(a, "setup", "--installer"); code != exitOK || len(*opened) != 0 || !strings.Contains(out, "settings kept: "+path) {
		t.Fatalf("exit %d, opened %v:\n%s", code, *opened, out)
	}
	// setup itself, with settings there, is not a first start: esc changes
	// nothing.
	if code, out, _ := shrinkWith(a, "setup"); code != exitOK || strings.Join(*opened, " ") != "setup" || !strings.Contains(out, "nothing changed") {
		t.Fatalf("exit %d, opened %v:\n%s", code, *opened, out)
	}
}

func TestProtectListAddsAndDropsByName(t *testing.T) {
	got := protectList([]string{"Old", "Work"}, []string{"work", "New", "New"}, []string{"OLD"})
	if strings.Join(got, ",") != "Work,New" {
		t.Fatalf("%v", got)
	}
}

func TestErrorsSayWhatToDo(t *testing.T) {
	// On a terminal: the problem after a red cross, what to do under it.
	err := hint(errors.Join(errUsage, errors.New("no disk named \"x\"")), "see wsl-shrinker list")
	var b bytes.Buffer
	printErrLook(look{on: true}, &b, hint(errors.New("Dev cannot be shrunk now: the disk is protected in the settings"),
		"to shrink it after all: wsl-shrinker setup --unprotect Dev"))
	lines := strings.Split(ansi.Strip(b.String()), "\n")
	if lines[0] != "✗ Dev cannot be shrunk now: the disk is protected in the settings" ||
		lines[1] != "  → to shrink it after all: wsl-shrinker setup --unprotect Dev" {
		t.Fatalf("%q", lines)
	}
	if !strings.Contains(b.String(), "\x1b[") {
		t.Fatalf("no color: %q", b.String())
	}
	// Piped: one plain line a script can read, the hint in brackets.
	b.Reset()
	printErrLook(look{}, &b, hint(errors.New("no terminal to ask in"), "add --yes to shrink without the question"))
	if b.String() != "wsl-shrinker: no terminal to ask in (add --yes to shrink without the question)\n" {
		t.Fatalf("%q", b.String())
	}
	// The hint keeps what the error is, for the exit code.
	if exitFor(err) != exitUsage {
		t.Fatalf("exit %d", exitFor(err))
	}
}

func TestTheOverviewOfTheMadeUpPC(t *testing.T) {
	// CLI mode's picture: the screen's colors and words, and the disk that
	// gives back most in the commands.
	var o, e bytes.Buffer
	if code := run([]string{"--snapshot", "100x24", "--cli", "--lang", "en"}, &o, &e); code != exitOK {
		t.Fatalf("exit %d: %s", code, e.String())
	}
	plain := ansi.Strip(o.String())
	for _, want := range []string{"can compact", "DISK", "Ubuntu-24.04", "protected", "worth shrinking", "wsl-shrinker shrink Ubuntu-24.04", "wsl-shrinker setup"} {
		if !strings.Contains(plain, want) {
			t.Errorf("no %q in:\n%s", want, plain)
		}
	}
	// No line is wider than the width asked for.
	for _, l := range strings.Split(plain, "\n") {
		if ansi.StringWidth(l) > 100 {
			t.Errorf("%d cells: %q", ansi.StringWidth(l), l)
		}
	}
	// Narrow, in Korean too: the sizes whole, every line within the width,
	// nothing trailing.
	for _, lang := range []string{"en", "ko"} {
		for _, w := range []int{40, 60} {
			o.Reset()
			if code := run([]string{"--snapshot", fmt.Sprintf("%dx24", w), "--cli", "--lang", lang}, &o, &e); code != exitOK {
				t.Fatalf("exit %d: %s", code, e.String())
			}
			narrow := ansi.Strip(o.String())
			if !regexp.MustCompile(`Ubun\S*\s.*\s64\.0 GB`).MatchString(narrow) || strings.Contains(narrow, "64.0 G…") {
				t.Errorf("%s %d: what Ubuntu gives back was cut:\n%s", lang, w, narrow)
			}
			for _, l := range strings.Split(narrow, "\n") {
				if ansi.StringWidth(l) > w-1 || strings.HasSuffix(l, " ") {
					t.Errorf("%s %d: %q", lang, w, l)
				}
			}
		}
	}
	i18n.Set(i18n.English)
}

func TestCtrlCOnTheFirstSetupStopsEverything(t *testing.T) {
	// Ctrl+C is out: no settings written, nothing opened after it, and the
	// next start asks again.
	a, _ := flowApp(t)
	path := withSettings(t, a, "")
	opened := inTerminal(t, tui.SetupChoice{}, false)
	openSetup = func(context.Context, tui.SetupStart) (tui.SetupChoice, bool, error) {
		*opened = append(*opened, "setup")
		return tui.SetupChoice{}, false, tui.ErrSetupCancelled
	}
	code, out, errOut := shrinkWith(a)
	if code != exitFail || strings.Join(*opened, " ") != "setup" || exists(path) || !strings.Contains(errOut, "setup cancelled; nothing was written") {
		t.Fatalf("exit %d, opened %v, written %v: %s\n%s", code, *opened, exists(path), errOut, out)
	}
}

func TestLangOnTheCommandLineOutlastsTheSetupScreen(t *testing.T) {
	// The setup screen shows the language being chosen; once it is saved,
	// --lang still decides this start's words.
	a, _ := flowApp(t)
	withSettings(t, a, "")
	inTerminal(t, tui.SetupChoice{}, false)
	openSetup = func(context.Context, tui.SetupStart) (tui.SetupChoice, bool, error) {
		// As the real screen leaves it after Korean was chosen and saved.
		i18n.Set(i18n.Korean)
		return tui.SetupChoice{Language: "ko", Theme: "auto", Mode: "cli", MinGainGB: 1}, true, nil
	}
	code, out, _ := shrinkWith(a, "--lang", "en")
	if code != exitOK || !strings.Contains(out, "every command") || strings.Contains(out, "모든 명령") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
}

func TestSetupSavesOnlyWhatChanged(t *testing.T) {
	// A file written by hand keeps what the screen did not change, its own
	// way of writing a list included.
	a, _ := flowApp(t)
	hand := "# mine\nprotect = [\n  \"Work\",\n]\nmode = \"tui\"\n"
	path := withSettings(t, a, hand)
	inTerminal(t, tui.SetupChoice{Language: "auto", Theme: "auto", Mode: "cli", MinGainGB: 1, Protect: []string{"Work"}}, true)
	if code, _, errOut := shrinkWith(a, "setup"); code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if b, _ := os.ReadFile(path); string(b) != "# mine\nprotect = [\n  \"Work\",\n]\nmode = \"cli\"\n" {
		t.Fatalf("%q", b)
	}
	// Saving with nothing changed writes nothing.
	a.Config.Mode = "cli"
	if code, out, _ := shrinkWith(a, "setup"); code != exitOK || !strings.Contains(out, "nothing changed") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
}

func TestSetupFlagsAreCheckedAndAFileThatCannotBeWrittenIsNoUsageMistake(t *testing.T) {
	a, _ := flowApp(t)
	withSettings(t, a, "")
	for args, want := range map[string]string{
		"setup --mode gui":         "--mode takes tui or cli",
		"setup --language fr":      "--language takes auto, en or ko",
		"setup --min-gain NaN":     "--min-gain takes a number of GB, 0 or more",
		"setup --min-gain -1":      "--min-gain takes a number of GB, 0 or more",
		"setup --min-gain 2000000": "min_gain_gb",
	} {
		code, _, errOut := shrinkWith(a, strings.Fields(args)...)
		if code != exitUsage || !strings.Contains(errOut, want) || strings.Contains(errOut, ".new") {
			t.Errorf("%s: exit %d: %s", args, code, errOut)
		}
	}
	// An empty name names no disk.
	if code, _, errOut := shrinkWith(a, "setup", "--protect", " "); code != exitUsage || !strings.Contains(errOut, "take a disk's name") {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	// --lang with choices given as flags is the language kept, as the
	// screen would keep it.
	path := a.ConfigPath
	if code, _, errOut := shrinkWith(a, "setup", "--lang", "ko", "--mode", "cli"); code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if c, err := config.Load(path); err != nil || c.Language != "ko" || c.Mode != "cli" {
		t.Fatalf("%+v %v", c, err)
	}
	// A folder that cannot be made: the file's trouble, exit 1, not 2.
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	a.ConfigPath = filepath.Join(blocker, "wsl-shrinker", "config.toml")
	if code, _, errOut := shrinkWith(a, "setup", "--mode", "cli"); code != exitFail {
		t.Fatalf("exit %d: %s", code, errOut)
	}
}

func TestCommandsToTypeQuoteANameWithSpaces(t *testing.T) {
	// As the hints put it.
	err := blockHint(errors.New("x"), shrink.BlockProtected, disks.Disk{Name: "Docker Desktop data"})
	if !strings.Contains(err.Error(), "setup --unprotect 'Docker Desktop data'") {
		t.Fatalf("%v", err)
	}
}

func TestTheOverviewSuggestsNoShrinkThatCannotRun(t *testing.T) {
	// The only disk is protected: no shrink to suggest, the rest stays.
	a, _ := flowApp(t)
	withSettings(t, a, "mode = \"cli\"\nprotect = [\"Dev\"]\n")
	inTerminal(t, tui.SetupChoice{}, false)
	code, out, _ := shrinkWith(a)
	if code != exitOK || strings.Contains(out, "wsl-shrinker shrink") || !strings.Contains(out, "wsl-shrinker tui") || !strings.Contains(out, "protected") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
}

func TestAScreenThatStopsOnABugSaysWhereToReportIt(t *testing.T) {
	// Bubble Tea reports a panic as a kill, having printed where it was;
	// the program says it was a bug.
	var stack, b bytes.Buffer
	err := screenBug(fmt.Errorf("%w: %w", tea.ErrProgramKilled, tea.ErrProgramPanic), &stack)
	printErrLook(look{}, &b, err)
	if !strings.Contains(b.String(), "the screen stopped on a bug (please report it with the lines above: "+IssuesURL+")") || stack.Len() != 0 {
		t.Fatalf("%q %q", b.String(), stack.String())
	}
	// A bug in a run's goroutine comes with where it was: written above.
	b.Reset()
	err = screenBug(&tui.BugError{Value: "index out of range", Stack: []byte("goroutine 7 [running]:\n")}, &stack)
	printErrLook(look{}, &b, err)
	if stack.String() != "goroutine 7 [running]:\n" || !strings.Contains(b.String(), "the screen stopped on a bug: index out of range (please report it") {
		t.Fatalf("%q %q", b.String(), stack.String())
	}
	// Anything else is as it was.
	if e := errors.New("x"); screenBug(e, &stack) != e {
		t.Fatal("changed another error")
	}
}

// panicReader is a reader with a bug in it.
type panicReader struct{}

func (panicReader) Read([]byte) (int, error) { panic("a reader with a bug") }

func TestABugWhileAskingSaysWhereItWas(t *testing.T) {
	// The question's read runs in a goroutine of its own: a bug there is
	// the command's error, with where it was, not a bare Go panic.
	var out bytes.Buffer
	ok, err := ask(context.Background(), panicReader{}, &out, "Shrink? [y/N] ")
	if ok || err == nil || !strings.Contains(err.Error(), "stopped on a bug: a reader with a bug (please report it") {
		t.Fatalf("%v %v", ok, err)
	}
	if !strings.Contains(out.String(), "panicReader") {
		t.Fatalf("no stack in %q", out.String())
	}
	// An answer is read as before.
	if ok, err := ask(context.Background(), strings.NewReader("yes\n"), &out, "Shrink? [y/N] "); !ok || err != nil {
		t.Fatalf("%v %v", ok, err)
	}
}
