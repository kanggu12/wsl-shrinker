package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kanggu12/wsl-shrinker/internal/engine/disks"
	"github.com/kanggu12/wsl-shrinker/internal/engine/estimate"
	"github.com/kanggu12/wsl-shrinker/internal/machine/winapi"
	"github.com/kanggu12/wsl-shrinker/internal/settings/config"
)

// settingsIn points the settings and the history at dir, on Windows and off
// it, so no real file of the person running the tests is read.
func settingsIn(t *testing.T, dir string) {
	t.Helper()
	// Windows reads APPDATA and LOCALAPPDATA; elsewhere the XDG folders stand in.
	for _, v := range []string{"APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(v, dir)
	}
}

// runCLI runs one command line with the settings in a folder of the test's
// own.
func runCLI(t *testing.T, args ...string) (code int, out, errOut string) {
	t.Helper()
	settingsIn(t, t.TempDir())
	var o, e bytes.Buffer
	code = run(args, &o, &e)
	return code, o.String(), e.String()
}

func TestUsageErrorsSayWhatIsWrong(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		// A command that takes nothing names itself, not a missing command.
		{[]string{"list", "extra"}, `list takes no arguments`},
		{[]string{"history", "extra"}, `history takes no arguments`},
		// The commands that want names say which names.
		{[]string{"inside"}, `inside takes one distribution name`},
		{[]string{"shrink"}, `shrink takes the names of the disks`},
		// A word after config that is none of its commands.
		{[]string{"config", "bogus"}, `unknown command "bogus"`},
		// A theme that is not one is refused, like a language.
		{[]string{"--theme", "bogus", "--snapshot", "80x24"}, `--theme takes auto, dark or light`},
		// The screen's --clean takes one id per flag, and never none.
		{[]string{"--clean", "Ubuntu="}, `--clean takes NAME=ID`},
		{[]string{"--clean", "Ubuntu"}, `--clean takes NAME=ID`},
	} {
		code, _, errOut := runCLI(t, c.args...)
		if code != exitUsage || !strings.Contains(errOut, c.want) {
			t.Errorf("%v: exit %d, %q; want exit %d and %q", c.args, code, strings.TrimSpace(errOut), exitUsage, c.want)
		}
	}
}

func TestConfigAloneShowsItsHelp(t *testing.T) {
	// Without a subcommand, config lists them and succeeds.
	code, out, _ := runCLI(t, "config")
	if code != exitOK || !strings.Contains(out, "init") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
}

func TestHelpLeavesOutCompletion(t *testing.T) {
	// The shell completion command is cobra's, not this program's.
	_, out, _ := runCLI(t, "--help")
	if strings.Contains(out, "completion") {
		t.Fatalf("help lists completion:\n%s", out)
	}
}

func TestHelpNamesTheLanguageOrder(t *testing.T) {
	// The flag's text follows the order the code chooses in.
	_, out, _ := runCLI(t, "list", "--help")
	if !strings.Contains(out, "WSL_SHRINKER_LANG") {
		t.Fatalf("--lang does not name WSL_SHRINKER_LANG:\n%s", out)
	}
}

func TestShrinkWithoutTerminalWantsYes(t *testing.T) {
	// Nobody can answer the question: refuse before anything is read.
	old := stdinIsTerminal
	stdinIsTerminal = func() bool { return false }
	t.Cleanup(func() { stdinIsTerminal = old })
	code, out, errOut := runCLI(t, "shrink", "Ubuntu")
	if code != exitUsage || !strings.Contains(errOut, "--yes") {
		t.Fatalf("exit %d, %q", code, errOut)
	}
	// Standard output stays empty, for --json readers.
	if out != "" {
		t.Fatalf("stdout: %q", out)
	}
}

func TestDoctorSaysWhenThereIsNoSettingsFile(t *testing.T) {
	_, out, _ := runCLI(t, "doctor")
	if !strings.Contains(out, "ok    settings: defaults") || !strings.Contains(out, "config init") {
		t.Fatalf("no word of the defaults:\n%s", out)
	}
}

func TestDoctorFixForAnOldWSL(t *testing.T) {
	// With distributions on the PC, WSL is there: the fix is the update.
	if got := wslFix(true); !strings.Contains(got, "wsl --update") {
		t.Fatalf("%q", got)
	}
	// Without any, it is the install.
	if got := wslFix(false); !strings.Contains(got, "wsl --install") {
		t.Fatalf("%q", got)
	}
}

func TestDoctorCountsOneProblem(t *testing.T) {
	dir := t.TempDir()
	// A settings file with a key no version knows.
	p := filepath.Join(dir, "wsl-shrinker", "config.toml")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("bogus = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	settingsIn(t, dir)
	var o, e bytes.Buffer
	if code := run([]string{"doctor"}, &o, &e); code != exitFail {
		t.Fatalf("exit %d", code)
	}
	if got := strings.TrimSpace(e.String()); got != "wsl-shrinker: 1 problem" {
		t.Fatalf("%q", got)
	}
}

func TestDoctorNamesWhatStopsADisk(t *testing.T) {
	// Errors the kernel recorded are a problem a shrink refuses for; a file
	// from the settings that another program has open is a note, and not
	// blamed on Docker Desktop.
	d := disks.Disk{Name: "data.vhdx", Kind: disks.KindFile, File: winapi.FileInfo{Held: true},
		Estimate: &estimate.Result{FileSize: 5 << 30, FSErrors: true}}
	var lines []string
	line := func(ok bool, what, fix string) { lines = append(lines, fmt.Sprint(ok, " ", what, " / ", fix)) }
	note := func(what string) { lines = append(lines, "note "+what) }
	doctorDisk(d, true, false, line, note)
	out := strings.Join(lines, "\n")
	if !strings.Contains(out, "false data.vhdx: the filesystem records errors / check it with e2fsck") {
		t.Errorf("no problem for the errors:\n%s", out)
	}
	if !strings.Contains(out, "note data.vhdx: in use by another program") || strings.Contains(out, "Docker Desktop") {
		t.Errorf("the held file:\n%s", out)
	}
}

func TestCutOffRunIsReportedThenRecovered(t *testing.T) {
	// A run of a file was cut off at its compaction (its process is gone):
	// doctor names it with the fix, shrink refuses until it is finished,
	// and recover finishes it.
	dir := t.TempDir()
	settingsIn(t, dir)
	j := filepath.Join(dir, "wsl-shrinker", "run.json")
	if err := os.MkdirAll(filepath.Dir(j), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(j, []byte(`{"pid":999999999,"exe":"wsl-shrinker.exe","disk":"data.vhdx","step":"compact"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var o, e bytes.Buffer
	if code := run([]string{"doctor"}, &o, &e); code != exitFail || !strings.Contains(o.String(), "FAIL  the run of data.vhdx was cut off at compact") || !strings.Contains(o.String(), "wsl-shrinker recover") {
		t.Fatalf("doctor exit %d:\n%s", code, o.String())
	}
	o.Reset()
	e.Reset()
	if code := run([]string{"shrink", "data.vhdx", "--yes"}, &o, &e); code != exitFail || !strings.Contains(e.String(), "cut off at compact (wsl-shrinker recover finishes it)") {
		t.Fatalf("shrink exit %d: %s", code, e.String())
	}
	o.Reset()
	e.Reset()
	if code := run([]string{"recover"}, &o, &e); code != exitOK || !strings.Contains(o.String(), "data.vhdx is a file") {
		t.Fatalf("recover exit %d: %s %s", code, o.String(), e.String())
	}
	if _, err := os.Stat(j); !os.IsNotExist(err) {
		t.Fatalf("the journal stayed: %v", err)
	}
}

func TestJSONStartsWithItsSchema(t *testing.T) {
	// Every --json output is an object with the schema's number first, and
	// an empty list is [], not null.
	code, out, _ := runCLI(t, "history", "--json")
	if code != exitOK {
		t.Fatalf("exit %d", code)
	}
	var got struct {
		Schema int               `json:"schema"`
		Runs   []json.RawMessage `json:"runs"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.Schema != 1 || got.Runs == nil || len(got.Runs) != 0 {
		t.Fatalf("%v %+v\n%s", err, got, out)
	}
	if !strings.HasPrefix(strings.TrimSpace(out), "{\n  \"schema\": 1,") {
		t.Fatalf("the schema is not first:\n%s", out)
	}
}

func TestShrinkAsksAtATerminalWSLRelays(t *testing.T) {
	// The launcher in WSL says a person is at its terminal: the question is
	// asked there, though the .exe only sees pipes.
	oldIn, oldRelay := stdinIsTerminal, relayed
	stdinIsTerminal, relayed = func() bool { return false }, func(fd int) bool { return fd == 0 }
	t.Cleanup(func() { stdinIsTerminal, relayed = oldIn, oldRelay })
	_, _, errOut := runCLI(t, "shrink", "Ubuntu")
	if strings.Contains(errOut, "no terminal to ask in") {
		t.Fatalf("refused a relayed terminal: %q", errOut)
	}
}

func TestATypoSaysTheCommandMeant(t *testing.T) {
	// The command meant, when one is near; the help, when none is.
	for args, want := range map[string]string{"lst": "did you mean: wsl-shrinker list", "config pth": "did you mean: wsl-shrinker config path",
		"zzzz": "wsl-shrinker --help lists the commands", "list extra": "wsl-shrinker list --help",
		// help NAME, for a name that is no command, says so too.
		"help bogus": "wsl-shrinker --help lists the commands", "help lst": "did you mean: wsl-shrinker help list",
		"help config pth": "did you mean: wsl-shrinker help config path"} {
		code, _, errOut := runCLI(t, strings.Fields(args)...)
		if code != exitUsage || !strings.Contains(errOut, want) {
			t.Errorf("%s: exit %d, %q", args, code, errOut)
		}
	}
}

func TestSetupPutsRightAValueTheFileHasWrong(t *testing.T) {
	// A wrong value keeps every command from starting: each says which
	// setup command puts it right, and that command does, keeping the rest
	// of the file as it was written.
	settingsIn(t, t.TempDir())
	p := config.Path(winapi.AppData())
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(s string) {
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runHere := func(args ...string) (int, string, string) {
		var o, e bytes.Buffer
		code := run(args, &o, &e)
		return code, o.String(), e.String()
	}
	write("# mine\nlanguage = \"fr\"\nmode = \"cli\"\n")
	if code, _, errOut := runHere("history"); code != exitFail || !strings.Contains(errOut, "language must be auto, en or ko") ||
		!strings.Contains(errOut, "wsl-shrinker setup --language auto puts it right") {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	// Protecting adds to the list the file has, which cannot be read yet.
	if code, _, errOut := runHere("setup", "--protect", "Dev"); code != exitFail || !strings.Contains(errOut, "language must be") {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if code, out, errOut := runHere("setup", "--language", "auto"); code != exitOK || !strings.Contains(out, "saved: ") {
		t.Fatalf("exit %d: %s%s", code, out, errOut)
	}
	if b, _ := os.ReadFile(p); !strings.Contains(string(b), "# mine") || !strings.Contains(string(b), `language = "auto"`) || !strings.Contains(string(b), `mode = "cli"`) {
		t.Fatalf("%s", b)
	}
	// Two values wrong: putting one right says the other, with its own
	// command, and writes nothing.
	write("language = \"fr\"\ntheme = \"blue\"\n")
	if code, _, errOut := runHere("setup", "--language", "en"); code != exitFail || !strings.Contains(errOut, "theme must be auto, dark or light") ||
		!strings.Contains(errOut, "wsl-shrinker setup --theme auto puts it right") {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if b, _ := os.ReadFile(p); strings.Contains(string(b), `language = "en"`) {
		t.Fatalf("written: %s", b)
	}
}

func TestVersionAndConfigHelpLeaveOutTheLanguage(t *testing.T) {
	// --lang changes nothing they print; the commands that use it list it.
	for args, want := range map[string]bool{"version --help": false, "config path --help": false, "config --help": false, "list --help": true} {
		_, out, _ := runCLI(t, strings.Fields(args)...)
		if strings.Contains(out, "--lang") != want {
			t.Errorf("%s: --lang listed %v:\n%s", args, !want, out)
		}
	}
	// help alone is the program's help.
	if code, out, _ := runCLI(t, "help"); code != exitOK || !strings.Contains(out, "Available Commands") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
}
