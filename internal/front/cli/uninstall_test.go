package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeInstall is a PC with wsl-shrinker installed in a folder of the
// test's own, as the install script leaves it, with settings and a history
// in folders of the test's own too; and what uninstall asked of it. No
// test reaches the person's PATH or program: every call that would is
// this one's.
type fakeInstall struct {
	dir, exe, settings, state string
	// path is the user's PATH as stored, expand whether it expands.
	path   string
	expand bool
	// wrote is each PATH value written; pathErr fails the write.
	wrote   []string
	pathErr error
	// deleted is each (program, folder) the removal after the end got.
	deleted [][2]string
	// held is the files another program holds; others how many other
	// copies of the program run; running whether the journal's process
	// still runs.
	held    map[string]bool
	others  int
	running bool
}

// installed makes the PC, with the program in root/Programs/folder, and
// returns it.
func installed(t *testing.T, folder string) *fakeInstall {
	t.Helper()
	root := t.TempDir()
	// The settings and the history in folders of their own, on Windows and
	// off it: off it XDG's stand in, as in settingsIn.
	roaming, local := filepath.Join(root, "Roaming"), filepath.Join(root, "Local")
	for v, d := range map[string]string{"APPDATA": roaming, "XDG_CONFIG_HOME": roaming, "LOCALAPPDATA": local, "XDG_CACHE_HOME": local} {
		t.Setenv(v, d)
	}
	f := &fakeInstall{dir: filepath.Join(root, "Programs", folder), settings: filepath.Join(roaming, "wsl-shrinker"),
		state: filepath.Join(local, "wsl-shrinker"), expand: true, held: map[string]bool{}}
	f.exe = filepath.Join(f.dir, "wsl-shrinker.exe")
	// The program, its launcher, and a copy an install cut short.
	for _, p := range []string{f.exe, filepath.Join(f.dir, "wsl-shrinker"), filepath.Join(f.dir, "wsl-shrinker.exe.new"),
		filepath.Join(f.settings, "config.toml"), filepath.Join(f.state, "history.jsonl")} {
		write(t, p, "x")
	}
	// The PATH as an install leaves it: the folder among the person's own
	// entries, one written with a variable and one empty.
	f.path = `C:\Users\ann\bin;` + f.dir + `\;;%NO_SUCH_VARIABLE%\tools`
	// Every call uninstall makes of the PC, answered here.
	win, exe, get, set, use, others, del, alive := onWindows, executable, userPath, setUserPath, inUse, othersRunning, deleteAfterExit, journalAlive
	onWindows = true
	executable = func() (string, error) { return f.exe, nil }
	userPath = func() (string, bool, error) { return f.path, f.expand, nil }
	setUserPath = func(v string, expand bool) error {
		if f.pathErr != nil {
			return f.pathErr
		}
		f.wrote = append(f.wrote, v)
		f.path, f.expand = v, expand
		return nil
	}
	inUse = func(p string) bool { return f.held[filepath.Base(p)] }
	othersRunning = func(string) (int, error) { return f.others, nil }
	// As the real one does once the program has ended: its file, then the
	// folder if nothing else is in it.
	deleteAfterExit = func(exe, dir string) error {
		f.deleted = append(f.deleted, [2]string{exe, dir})
		_ = os.Remove(exe)
		if dir != "" {
			_ = os.Remove(dir)
		}
		return nil
	}
	journalAlive = func(int, string) bool { return f.running }
	t.Cleanup(func() {
		onWindows, executable, userPath, setUserPath, inUse, othersRunning, deleteAfterExit, journalAlive = win, exe, get, set, use, others, del, alive
	})
	return f
}

// write makes the file at p, and its folder, with body.
func write(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// there reports whether p exists.
func there(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// uninstallWith runs a command line, for the PC f made.
func uninstallWith(args ...string) (int, string, string) {
	var o, e bytes.Buffer
	code := run(args, &o, &e)
	return code, o.String(), e.String()
}

// untouched fails the test unless the program, its folder, the settings,
// the history and the PATH are as installed made them.
func (f *fakeInstall) untouched(t *testing.T) {
	t.Helper()
	for _, p := range []string{f.exe, filepath.Join(f.dir, "wsl-shrinker"), filepath.Join(f.settings, "config.toml"), filepath.Join(f.state, "history.jsonl")} {
		if !there(p) {
			t.Errorf("%s was removed", p)
		}
	}
	if len(f.wrote) != 0 || len(f.deleted) != 0 {
		t.Errorf("the PATH was written %q, removals asked %q", f.wrote, f.deleted)
	}
}

func TestUninstallRemovesWhatTheInstallMadeAndSaysWhatStays(t *testing.T) {
	f := installed(t, "wsl-shrinker")
	answering(t, "y\n")
	code, out, errOut := uninstallWith("uninstall")
	if code != exitOK {
		t.Fatalf("exit %d: %s%s", code, out, errOut)
	}
	// The plan first, one thing to a line, then the question.
	for _, want := range []string{
		"wsl-shrinker uninstall:\n",
		"  removes  " + f.exe + " and its launcher for WSL\n",
		"  removes  the folder " + f.dir + " and its entry in your PATH\n",
		"  keeps    the settings in " + f.settings + " (--purge removes them)\n",
		"  keeps    the history in " + f.state + " (--purge removes it)\n",
		"Uninstall? [y/N] ",
	} {
		if !strings.Contains(errOut, want) {
			t.Errorf("no %q in:\n%s", want, errOut)
		}
	}
	// The folder is gone, the program last, once it had ended.
	if there(f.dir) || len(f.deleted) != 1 || f.deleted[0] != [2]string{f.exe, f.dir} {
		t.Fatalf("folder there %v, removals %q", there(f.dir), f.deleted)
	}
	// The PATH lost its entry for the folder, and nothing else: the other
	// entries as they were written, the empty one too, the type kept.
	if want := `C:\Users\ann\bin;;%NO_SUCH_VARIABLE%\tools`; len(f.wrote) != 1 || f.path != want || !f.expand {
		t.Fatalf("PATH written %q, expand %v; want %q", f.wrote, f.expand, want)
	}
	// The settings and the history stay, and where they are is said.
	if !there(filepath.Join(f.settings, "config.toml")) || !there(filepath.Join(f.state, "history.jsonl")) {
		t.Fatal("the settings or the history went without --purge")
	}
	want := "uninstalled wsl-shrinker from " + f.dir + "\n" +
		"note: took it out of your PATH: terminals opened from now on no longer find it\n" +
		"note: kept the settings in " + f.settings + " and the history in " + f.state + ": delete those folders to remove them too\n"
	if out != want {
		t.Fatalf("said:\n%s\nwant:\n%s", out, want)
	}
}

func TestUninstallAnsweredNoChangesNothing(t *testing.T) {
	f := installed(t, "wsl-shrinker")
	answering(t, "n\n")
	code, out, errOut := uninstallWith("uninstall")
	if code != exitFail || out != "" || !strings.Contains(errOut, "not uninstalled") {
		t.Fatalf("exit %d: %q %q", code, out, errOut)
	}
	f.untouched(t)
	if !there(filepath.Join(f.dir, "wsl-shrinker.exe.new")) {
		t.Error("the copy beside the program went")
	}
}

func TestUninstallWithoutATerminalWantsYes(t *testing.T) {
	f := installed(t, "wsl-shrinker")
	noTerminal(t)
	code, _, errOut := uninstallWith("uninstall")
	if code != exitUsage || !strings.Contains(errOut, "no terminal to ask in") || !strings.Contains(errOut, "add --yes to uninstall without the question") {
		t.Fatalf("exit %d: %q", code, errOut)
	}
	f.untouched(t)
	// -y asks nothing and removes it.
	if code, _, errOut := uninstallWith("uninstall", "-y"); code != exitOK || strings.Contains(errOut, "[y/N]") || there(f.dir) {
		t.Fatalf("-y: exit %d, %q, folder there %v", code, errOut, there(f.dir))
	}
}

func TestUninstallPurgeRemovesOnlyTheProgramsFiles(t *testing.T) {
	f := installed(t, "wsl-shrinker")
	noTerminal(t)
	// Every file the program keeps, and two it does not: a note of the
	// person's beside the settings, a folder beside the history.
	for _, n := range []string{"config.toml.new", "notes.txt"} {
		write(t, filepath.Join(f.settings, n), "x")
	}
	for _, n := range []string{"run-1.tmp", "history-2.tmp", "handoff-0a1b2c", "crash-12-34.txt", "crash-12-34-shown.txt", filepath.Join("mine", "keep.txt")} {
		write(t, filepath.Join(f.state, n), "x")
	}
	// This run's own crash file is held until it ends, which removes it.
	own := filepath.Join(f.state, "crash-99-1.txt")
	write(t, own, "")
	old := ownCrash
	ownCrash = own
	t.Cleanup(func() { ownCrash = old })
	code, out, errOut := uninstallWith("uninstall", "--purge", "--yes")
	if code != exitOK {
		t.Fatalf("exit %d: %s%s", code, out, errOut)
	}
	left := func(dir string) []string {
		var names []string
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			names = append(names, e.Name())
		}
		return names
	}
	if got := left(f.settings); !slices.Equal(got, []string{"notes.txt"}) {
		t.Errorf("left in the settings: %q", got)
	}
	if got := left(f.state); !slices.Equal(got, []string{"crash-99-1.txt", "mine"}) {
		t.Errorf("left in the history's folder: %q", got)
	}
	// What stays, and why, in one line.
	if !strings.Contains(out, "note: removed the settings and the history; kept "+f.settings+" and "+f.state+": other files are in it\n") || strings.Contains(out, "delete those folders") {
		t.Errorf("said:\n%s", out)
	}
	// With nothing of the person's there, both folders go, and only this
	// run's crash file is left for its end.
	g := installed(t, "wsl-shrinker")
	write(t, filepath.Join(g.state, "crash-99-1.txt"), "")
	ownCrash = filepath.Join(g.state, "crash-99-1.txt")
	if code, out, errOut := uninstallWith("uninstall", "--purge", "--yes"); code != exitOK || !strings.Contains(out, "note: removed the settings and the history\n") {
		t.Fatalf("exit %d: %s%s", code, out, errOut)
	}
	if there(g.settings) || !slices.Equal(left(g.state), []string{"crash-99-1.txt"}) {
		t.Errorf("settings there %v, history's folder holds %q", there(g.settings), left(g.state))
	}
}

func TestUninstallKeepsAFolderThatIsNotTheInstallsOwn(t *testing.T) {
	for _, c := range []struct{ name, folder, other, why string }{
		// A folder of the person's, on the PATH for other programs too.
		{"a tools folder", "tools", "other.exe", "other files are in it"},
		// The zip unpacked by hand: its readme and license beside the program.
		{"an unpacked zip", "wsl-shrinker", "README.txt", "other files are in it"},
		// A folder named otherwise, with nothing else in it.
		{"another name", "bin", "", "it is not named wsl-shrinker"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := installed(t, c.folder)
			noTerminal(t)
			if c.other != "" {
				write(t, filepath.Join(f.dir, c.other), "x")
			}
			code, out, errOut := uninstallWith("uninstall", "--yes")
			if code != exitOK {
				t.Fatalf("exit %d: %s%s", code, out, errOut)
			}
			// The program's files go; the folder, its other files and the
			// PATH stay.
			if !there(f.dir) || there(filepath.Join(f.dir, "wsl-shrinker")) || len(f.wrote) != 0 {
				t.Fatalf("folder there %v, PATH written %q", there(f.dir), f.wrote)
			}
			if c.other != "" && !there(filepath.Join(f.dir, c.other)) {
				t.Fatalf("%s went", c.other)
			}
			if len(f.deleted) != 1 || f.deleted[0] != [2]string{f.exe, ""} {
				t.Fatalf("removals %q", f.deleted)
			}
			if !strings.Contains(out, "note: kept "+f.dir+" and any PATH entry for it: "+c.why+"\n") || strings.Contains(out, "PATH: terminals") {
				t.Errorf("said:\n%s", out)
			}
			// Asked, the plan says the same before anything goes.
			g := installed(t, c.folder)
			if c.other != "" {
				write(t, filepath.Join(g.dir, c.other), "x")
			}
			answering(t, "n\n")
			if _, _, errOut := uninstallWith("uninstall"); !strings.Contains(errOut, "  keeps    the folder "+g.dir+": "+c.why+"\n") {
				t.Errorf("plan:\n%s", errOut)
			}
		})
	}
}

func TestUninstallWaitsForARunNotEnded(t *testing.T) {
	for _, c := range []struct {
		name    string
		journal string
		running bool
		want    string
		fix     string
	}{
		{"going", `{"pid":7,"exe":"wsl-shrinker.exe","disk":"Dev","distro":"Dev","step":"compact","stopped":true}`, true,
			"Dev is being shrunk by another wsl-shrinker", "let it finish, then uninstall"},
		{"cut off", `{"pid":7,"exe":"wsl-shrinker.exe","disk":"Dev","distro":"Dev","step":"compact","stopped":true}`, false,
			"the run of Dev was cut off at compact", "wsl-shrinker recover finishes it, or wsl-shrinker recover --forget closes it as it is; then uninstall"},
		{"unreadable", `{"pid":7,"disk":"De`, false,
			"a run was cut off", "wsl-shrinker recover finishes it"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := installed(t, "wsl-shrinker")
			noTerminal(t)
			j := filepath.Join(f.state, "run.json")
			write(t, j, c.journal)
			if c.name == "unreadable" {
				// Old enough not to be a run just starting.
				if err := os.Chtimes(j, oldTime, oldTime); err != nil {
					t.Fatal(err)
				}
			}
			f.running = c.running
			for _, args := range [][]string{{"uninstall", "--yes"}, {"uninstall", "--purge", "--yes"}} {
				code, _, errOut := uninstallWith(args...)
				if code != exitFail || !strings.Contains(errOut, c.want) || !strings.Contains(errOut, c.fix) {
					t.Fatalf("%v: exit %d, %q", args, code, errOut)
				}
			}
			f.untouched(t)
			if !there(j) {
				t.Fatal("the journal went")
			}
		})
	}
}

func TestUninstallLeavesAScoopInstallToScoop(t *testing.T) {
	f := installed(t, filepath.Join("scoop", "apps", "wsl-shrinker", "current"))
	write(t, filepath.Join(f.dir, "scoop-manifest.json"), "{}")
	noTerminal(t)
	code, _, errOut := uninstallWith("uninstall", "--yes")
	if code != exitFail || !strings.Contains(errOut, "this wsl-shrinker was installed with Scoop") ||
		!strings.Contains(errOut, "scoop uninstall wsl-shrinker removes it; wsl-shrinker uninstall --purge removes the settings and the history first") {
		t.Fatalf("exit %d: %q", code, errOut)
	}
	f.untouched(t)
	// --purge takes what Scoop does not: the settings and the history.
	code, out, errOut := uninstallWith("uninstall", "--purge", "--yes")
	if code != exitOK || there(f.settings) || there(filepath.Join(f.state, "history.jsonl")) {
		t.Fatalf("exit %d: %s%s", code, out, errOut)
	}
	if !there(f.exe) || !there(filepath.Join(f.dir, "wsl-shrinker")) || len(f.wrote) != 0 || len(f.deleted) != 0 {
		t.Fatalf("Scoop's files or the PATH were touched: removals %q, PATH %q", f.deleted, f.wrote)
	}
	if want := "removed the settings and the history\nnote: scoop uninstall wsl-shrinker removes the program\n"; out != want {
		t.Fatalf("said:\n%s\nwant:\n%s", out, want)
	}
	// Asked, the plan says what stays, and the question is about the rest.
	answering(t, "n\n")
	_, _, errOut = uninstallWith("uninstall", "--purge")
	if !strings.Contains(errOut, "  keeps    the program: scoop uninstall wsl-shrinker removes it\n") || !strings.Contains(errOut, "Remove them? [y/N] ") {
		t.Fatalf("plan:\n%s", errOut)
	}
	// A folder that only sits where Scoop's would, with no manifest in it,
	// is not Scoop's.
	g := installed(t, filepath.Join("apps", "wsl-shrinker", "bin"))
	noTerminal(t)
	if code, out, errOut := uninstallWith("uninstall", "--yes"); code != exitOK || strings.Contains(errOut, "Scoop") || len(g.deleted) != 1 {
		t.Fatalf("exit %d: %s%s", code, out, errOut)
	}
}

func TestUninstallRefusesWhatWouldStopHalfWay(t *testing.T) {
	// Another copy of the program running holds its file.
	f := installed(t, "wsl-shrinker")
	noTerminal(t)
	f.others = 1
	code, _, errOut := uninstallWith("uninstall", "--yes")
	if code != exitFail || !strings.Contains(errOut, "another wsl-shrinker is running from "+f.dir) || !strings.Contains(errOut, "close it, then uninstall") {
		t.Fatalf("exit %d: %q", code, errOut)
	}
	f.untouched(t)
	// A screen open in WSL holds the launcher.
	g := installed(t, "wsl-shrinker")
	g.held["wsl-shrinker"] = true
	code, _, errOut = uninstallWith("uninstall", "--yes")
	if code != exitFail || !strings.Contains(errOut, filepath.Join(g.dir, "wsl-shrinker")+" is in use") || !strings.Contains(errOut, "close the wsl-shrinker screens open in WSL") {
		t.Fatalf("exit %d: %q", code, errOut)
	}
	g.untouched(t)
}

func TestUninstallKeepsTheProgramWhenThePathCannotBeWritten(t *testing.T) {
	f := installed(t, "wsl-shrinker")
	noTerminal(t)
	f.pathErr = errors.New("access is denied")
	code, _, errOut := uninstallWith("uninstall", "--yes")
	if code != exitFail || !strings.Contains(errOut, "could not take "+f.dir+" out of your PATH: access is denied") || !strings.Contains(errOut, "wsl-shrinker uninstall tries again") {
		t.Fatalf("exit %d: %q", code, errOut)
	}
	// The program stays, to try again with, and nothing beside it went:
	// the PATH comes first.
	if !there(f.exe) || !there(filepath.Join(f.dir, "wsl-shrinker")) || len(f.deleted) != 0 {
		t.Fatalf("something went: removals %q, launcher there %v", f.deleted, there(filepath.Join(f.dir, "wsl-shrinker")))
	}
	// Tried again, it goes.
	f.pathErr = nil
	if code, _, errOut := uninstallWith("uninstall", "--yes"); code != exitOK || there(f.dir) {
		t.Fatalf("again: exit %d, %q", code, errOut)
	}
}

func TestUninstallNeedsNoSettingsThatLoad(t *testing.T) {
	// The settings file is what the person may be removing: one that does
	// not load does not stop it.
	f := installed(t, "wsl-shrinker")
	noTerminal(t)
	write(t, filepath.Join(f.settings, "config.toml"), "mode = [broken")
	if code, out, errOut := uninstallWith("uninstall", "--purge", "--yes"); code != exitOK || there(f.settings) || there(f.dir) {
		t.Fatalf("exit %d: %s%s", code, out, errOut)
	}
}

func TestUninstallIsForTheWindowsProgram(t *testing.T) {
	f := installed(t, "wsl-shrinker")
	onWindows = false
	code, _, errOut := uninstallWith("uninstall", "--yes")
	if code != exitFail || !strings.Contains(errOut, "wsl-shrinker runs on Windows") {
		t.Fatalf("exit %d: %q", code, errOut)
	}
	f.untouched(t)
}

func TestPathWithoutDropsTheFolderAsWindowsReadsIt(t *testing.T) {
	env := map[string]string{"LOCALAPPDATA": `C:\Users\ann\AppData\Local`, "EMPTY": ""}
	lookup := func(k string) (string, bool) {
		v, ok := env[strings.ToUpper(k)]
		return v, ok
	}
	dir := `C:\Users\ann\AppData\Local\Programs\wsl-shrinker`
	for _, c := range []struct {
		value, want string
		n           int
	}{
		// Written with a variable, in another case, quoted, with a slash at
		// the end: each is the folder.
		{`%LOCALAPPDATA%\Programs\wsl-shrinker;C:\Windows`, `C:\Windows`, 1},
		{`C:\Windows;c:\users\ANN\appdata\local\programs\WSL-SHRINKER\`, `C:\Windows`, 1},
		{`"C:\Users\ann\AppData\Local\Programs\wsl-shrinker";C:\Windows`, `C:\Windows`, 1},
		{`%localappdata%\Programs\wsl-shrinker;` + dir + `;C:\Windows`, `C:\Windows`, 2},
		// Everything else as it was written: variables, empty entries, and
		// a folder that only starts like it.
		{`%EMPTY%;;C:\x;` + dir + `-old;` + dir, `%EMPTY%;;C:\x;` + dir + `-old`, 1},
		// Not there: the value as it is, not written again.
		{`C:\Windows;%NOPE%\Programs\wsl-shrinker`, `C:\Windows;%NOPE%\Programs\wsl-shrinker`, 0},
		{``, ``, 0},
		// The folder alone: an empty PATH.
		{dir, ``, 1},
	} {
		got, n := pathWithout(c.value, dir, lookup)
		if got != c.want || n != c.n {
			t.Errorf("%q: %q, %d; want %q, %d", c.value, got, n, c.want, c.n)
		}
	}
}

func TestExpandVarsAsWindowsDoes(t *testing.T) {
	lookup := func(k string) (string, bool) {
		v, ok := map[string]string{"A": "1", "B": "2"}[k]
		return v, ok
	}
	for in, want := range map[string]string{
		`%A%\x\%B%`: `1\x\2`, `%A%%B%`: `12`,
		// A name with no value stays as written.
		`%NOPE%\x`: `%NOPE%\x`, `100%`: `100%`, `%%`: `%%`, `a%b`: `a%b`,
	} {
		if got := expandVars(in, lookup); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

// oldTime is an hour before the tests ran: a journal that old is no run
// just starting.
var oldTime = time.Now().Add(-time.Hour)

func TestUninstallNamesOnlyFoldersWithSomethingInThem(t *testing.T) {
	// No settings saved and no run yet: only this run's crash file is in
	// the history's folder, which it removes as it ends.
	f := installed(t, "wsl-shrinker")
	for _, p := range []string{filepath.Join(f.settings, "config.toml"), filepath.Join(f.state, "history.jsonl")} {
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	own := filepath.Join(f.state, "crash-99-1.txt")
	write(t, own, "")
	old := ownCrash
	ownCrash = own
	t.Cleanup(func() { ownCrash = old })
	answering(t, "y\n")
	code, out, errOut := uninstallWith("uninstall")
	if code != exitOK || strings.Contains(errOut, "the settings in") || strings.Contains(errOut, "the history in") {
		t.Fatalf("exit %d, plan:\n%s", code, errOut)
	}
	if strings.Contains(out, "kept the") || strings.Contains(out, "removed the settings") {
		t.Fatalf("said:\n%s", out)
	}
	// --purge has nothing to say either.
	g := installed(t, "wsl-shrinker")
	for _, p := range []string{filepath.Join(g.settings, "config.toml"), filepath.Join(g.state, "history.jsonl")} {
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	noTerminal(t)
	if code, out, errOut := uninstallWith("uninstall", "--purge", "--yes"); code != exitOK || strings.Contains(out, "removed the settings") {
		t.Fatalf("exit %d: %s%s", code, out, errOut)
	}
	// The settings alone: one folder, said as one.
	h := installed(t, "wsl-shrinker")
	if err := os.Remove(filepath.Join(h.state, "history.jsonl")); err != nil {
		t.Fatal(err)
	}
	if _, out, _ := uninstallWith("uninstall", "--yes"); !strings.Contains(out, "note: kept the settings in "+h.settings+": delete that folder to remove it too\n") {
		t.Fatalf("said:\n%s", out)
	}
}

func TestUninstallKeepsThePathsType(t *testing.T) {
	// A PATH stored plain (REG_SZ) stays plain: written back as one Windows
	// expands, its text would mean something else wherever it holds a %.
	f := installed(t, "wsl-shrinker")
	noTerminal(t)
	f.expand = false
	if code, out, errOut := uninstallWith("uninstall", "--yes"); code != exitOK || len(f.wrote) != 1 || f.expand {
		t.Fatalf("exit %d, written %q, expand %v: %s%s", code, f.wrote, f.expand, out, errOut)
	}
	// Not on the PATH at all: nothing is written, and nothing said of it.
	g := installed(t, "wsl-shrinker")
	g.path = `C:\Windows`
	if code, out, _ := uninstallWith("uninstall", "--yes"); code != exitOK || len(g.wrote) != 0 || strings.Contains(out, "PATH") {
		t.Fatalf("exit %d, written %q:\n%s", code, g.wrote, out)
	}
}

func TestUninstallLooksAgainAfterTheQuestion(t *testing.T) {
	// While the question waited, another copy of the program started: the
	// answer finds it, and nothing goes.
	f := installed(t, "wsl-shrinker")
	calls := 0
	othersRunning = func(string) (int, error) {
		calls++
		if calls > 1 {
			return 1, nil
		}
		return 0, nil
	}
	answering(t, "y\n")
	code, _, errOut := uninstallWith("uninstall")
	if code != exitFail || calls != 2 || !strings.Contains(errOut, "another wsl-shrinker is running from") {
		t.Fatalf("exit %d after %d looks: %q", code, calls, errOut)
	}
	f.untouched(t)
	// A run cut off meanwhile too.
	g := installed(t, "wsl-shrinker")
	answer := filepath.Join(g.state, "run.json")
	inUse = func(p string) bool {
		// The plan's look is the first; the run is cut off before the second.
		if !there(answer) {
			write(t, answer, `{"pid":7,"exe":"wsl-shrinker.exe","disk":"Dev","distro":"Dev","step":"stop","stopped":true}`)
			return false
		}
		return false
	}
	answering(t, "y\n")
	code, _, errOut = uninstallWith("uninstall", "--purge")
	if code != exitFail || !strings.Contains(errOut, "the run of Dev was cut off at stop") || !there(answer) {
		t.Fatalf("exit %d: %q; journal there %v", code, errOut, there(answer))
	}
	g.untouched(t)
}

func TestUninstallSaysARunStartingWithoutAnEmptyName(t *testing.T) {
	// A journal that does not parse yet is a run starting.
	f := installed(t, "wsl-shrinker")
	noTerminal(t)
	write(t, filepath.Join(f.state, "run.json"), `{"pid":7,"disk":"De`)
	code, _, errOut := uninstallWith("uninstall", "--yes")
	if code != exitFail || !strings.Contains(errOut, "a disk is being shrunk by another wsl-shrinker") {
		t.Fatalf("exit %d: %q", code, errOut)
	}
	f.untouched(t)
}

func TestUninstallWantsTheSettingsFoldersNamed(t *testing.T) {
	// Without APPDATA and LOCALAPPDATA (and off Windows, XDG's and HOME),
	// the folders would be looked for under the current one: refused.
	f := installed(t, "wsl-shrinker")
	noTerminal(t)
	for _, v := range []string{"APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "HOME"} {
		t.Setenv(v, "")
	}
	code, _, errOut := uninstallWith("uninstall", "--purge", "--yes")
	if code != exitFail || !strings.Contains(errOut, "APPDATA and LOCALAPPDATA must name folders") {
		t.Fatalf("exit %d: %q", code, errOut)
	}
	f.untouched(t)
}

func TestUninstallPlanSaysThePathOnlyWhenItHasAnEntry(t *testing.T) {
	f := installed(t, "wsl-shrinker")
	f.path = `C:\Windows`
	answering(t, "n\n")
	_, _, errOut := uninstallWith("uninstall")
	if !strings.Contains(errOut, "  removes  the folder "+f.dir+"\n") || strings.Contains(errOut, "entry in your PATH") {
		t.Fatalf("plan:\n%s", errOut)
	}
}

func TestUninstallSaysWhenTheProgramItselfStays(t *testing.T) {
	for _, own := range []bool{true, false} {
		folder := "wsl-shrinker"
		if !own {
			folder = "tools"
		}
		f := installed(t, folder)
		noTerminal(t)
		deleteAfterExit = func(string, string) error { return errors.New(`cannot remove "C:\\a%b" through cmd.exe`) }
		code, out, errOut := uninstallWith("uninstall", "--yes")
		left := f.exe
		if own {
			left = f.dir
		}
		if code != exitOK || !strings.HasPrefix(out, "uninstalled wsl-shrinker from "+f.dir+", but for the program itself\n") ||
			!strings.Contains(out, "note: remove "+left+" by hand once this program has ended: ") {
			t.Fatalf("own %v: exit %d:\n%s%s", own, code, out, errOut)
		}
		// Why a folder that is not the install's own stays is said either way.
		if !own && !strings.Contains(out, "note: kept "+f.dir+" and any PATH entry for it: it is not named wsl-shrinker") {
			t.Fatalf("not own:\n%s", out)
		}
	}
}

func TestUninstallSaysTheFolderStaysWhileATerminalIsInIt(t *testing.T) {
	f := installed(t, "wsl-shrinker")
	noTerminal(t)
	// By hand rather than t.Chdir, which cannot return to a folder on a
	// network path, as a test binary run from WSL starts in.
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(f.dir); err != nil {
		t.Fatal(err)
	}
	// Back where the test started, or at least out of the folder, before
	// the folder is removed: Windows removes no folder a process is in.
	t.Cleanup(func() {
		if os.Chdir(old) != nil {
			_ = os.Chdir(os.TempDir())
		}
	})
	// The fake removal cannot take a folder this process is in either.
	code, out, errOut := uninstallWith("uninstall", "--yes")
	if code != exitOK || !strings.Contains(out, "note: the folder stays while this terminal is in it: remove "+f.dir+" once you have left it\n") {
		t.Fatalf("exit %d:\n%s%s", code, out, errOut)
	}
}

func TestPathWithoutKeepsAQuotedEntryWhole(t *testing.T) {
	lookup := func(string) (string, bool) { return "", false }
	dir := `C:\Users\ann\AppData\Local\Programs\wsl-shrinker`
	// A quoted entry may hold a semicolon: it is one entry, not the folder.
	value := `"C:\a;` + dir + `";C:\x;` + dir
	got, n := pathWithout(value, dir, lookup)
	if want := `"C:\a;` + dir + `";C:\x`; got != want || n != 1 {
		t.Fatalf("%q, %d; want %q, 1", got, n, want)
	}
}

func TestUninstallTakesNoFolderByAProgramFilesNameForTheProgram(t *testing.T) {
	// A folder named like a program file is the person's: the folder that
	// holds it is not the install's own, and stays.
	f := installed(t, "wsl-shrinker")
	noTerminal(t)
	if err := os.Remove(filepath.Join(f.dir, "wsl-shrinker.exe.new")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(f.dir, "wsl-shrinker.new", "mine.txt"), "x")
	code, out, errOut := uninstallWith("uninstall", "--yes")
	if code != exitOK || !there(filepath.Join(f.dir, "wsl-shrinker.new", "mine.txt")) || len(f.wrote) != 0 ||
		!strings.Contains(out, "kept "+f.dir+" and any PATH entry for it: other files are in it") {
		t.Fatalf("exit %d:\n%s%s", code, out, errOut)
	}
}

func TestUninstallSaysWhenScoopsInstallHasNothingToPurge(t *testing.T) {
	f := installed(t, filepath.Join("scoop", "apps", "wsl-shrinker", "current"))
	write(t, filepath.Join(f.dir, "scoop-manifest.json"), "{}")
	for _, p := range []string{filepath.Join(f.settings, "config.toml"), filepath.Join(f.state, "history.jsonl")} {
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	noTerminal(t)
	code, out, errOut := uninstallWith("uninstall", "--purge", "--yes")
	if want := "found no settings or history to remove\nnote: scoop uninstall wsl-shrinker removes the program\n"; code != exitOK || out != want {
		t.Fatalf("exit %d:\n%s%s\nwant:\n%s", code, out, errOut, want)
	}
}

func TestUninstallKnowsScoopsFilesOldAndNew(t *testing.T) {
	// Scoop writes scoop-manifest.json and scoop-install.json beside the
	// app; an older Scoop wrote manifest.json and install.json. Any of them
	// in apps\wsl-shrinker\VERSION is Scoop's install.
	for _, name := range []string{"scoop-manifest.json", "scoop-install.json", "manifest.json", "install.json"} {
		f := installed(t, filepath.Join("scoop", "apps", "wsl-shrinker", "0.6.0"))
		write(t, filepath.Join(f.dir, name), "{}")
		noTerminal(t)
		code, _, errOut := uninstallWith("uninstall", "--yes")
		if code != exitFail || !strings.Contains(errOut, "this wsl-shrinker was installed with Scoop") {
			t.Fatalf("%s: exit %d, %q", name, code, errOut)
		}
		f.untouched(t)
	}
}
