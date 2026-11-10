package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kanggu12/wsl-shrinker/internal/engine/journal"
	"github.com/kanggu12/wsl-shrinker/internal/machine/winapi"
	"github.com/kanggu12/wsl-shrinker/internal/machine/wsl"
	"github.com/kanggu12/wsl-shrinker/internal/settings/config"
)

// What uninstall reaches the PC through; tests replace each, so no test
// reads or changes the person's PATH, nor removes a program.
var (
	// onWindows says whether there is a Windows program to remove.
	onWindows = runtime.GOOS == "windows"
	// executable is this program's file.
	executable = os.Executable
	// userPath and setUserPath read and write the user's PATH as stored.
	userPath    = winapi.UserPath
	setUserPath = winapi.SetUserPath
	// inUse says whether another program holds a file.
	inUse = winapi.InUse
	// othersRunning counts the other processes that run a program file.
	othersRunning = winapi.OthersRunning
	// deleteAfterExit removes this program's file once it has ended, and
	// its folder.
	deleteAfterExit = winapi.DeleteAfterExit
	// journalAlive says whether the process that wrote the journal runs.
	journalAlive = winapi.Running
)

// programFiles are what an install puts in its folder (the program, and its
// launcher for WSL), then the copies an install cut short leaves beside
// them. Nothing else in the folder is the program's.
var programFiles = []string{"wsl-shrinker.exe", "wsl-shrinker", "wsl-shrinker.exe.new", "wsl-shrinker.new"}

// The files the program keeps, by folder. A file that matches none is not
// the program's, and keeps its folder.
var (
	settingsFiles = []*regexp.Regexp{regexp.MustCompile(`^config\.toml(\.new)?$`)}
	stateFiles    = []*regexp.Regexp{
		regexp.MustCompile(`^run\.json$`), regexp.MustCompile(`^run-.*\.tmp$`),
		regexp.MustCompile(`^history\.jsonl$`), regexp.MustCompile(`^history-.*\.tmp$`),
		regexp.MustCompile(`^handoff-[0-9a-f]+$`),
		regexp.MustCompile(`^crash-\d+-\d+(-shown)?\.txt$`),
	}
)

// uninstallCmd removes the program from this PC, after a question that
// says what goes and what stays.
func uninstallCmd() *cobra.Command {
	var purge, yes bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove wsl-shrinker from this PC",
		Long: "Removes wsl-shrinker.exe and its launcher for WSL from the folder they run from,\n" +
			"and that folder and its PATH entry when it is named wsl-shrinker and nothing else\n" +
			"is in it. The settings and the history stay unless --purge is given.\n\n" +
			"A run that was cut off is finished or forgotten first (wsl-shrinker recover), and\n" +
			"a wsl-shrinker installed with Scoop is removed with Scoop.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return uninstall(cmd, purge, yes)
		},
	}
	cmd.Flags().BoolVar(&purge, "purge", false, "remove the settings and the history too")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask")
	return cmd
}

// uninstall is the command. It reads no settings, so a settings file that
// does not load is no obstacle: it may be what the person is removing.
func uninstall(cmd *cobra.Command, purge, yes bool) error {
	// The Linux build hands this to the .exe inside WSL; elsewhere there is
	// no Windows program to remove.
	if !onWindows {
		return wsl.ErrNotWindows
	}
	// The question needs someone to answer it: without a terminal and
	// without --yes, stop before anything is looked at.
	if !yes && !stdinIsTerminal() && !relayed(0) {
		return hint(fmt.Errorf("%w: no terminal to ask in", errUsage), "add --yes to uninstall without the question")
	}
	exe, err := executable()
	if err != nil {
		return err
	}
	dir := filepath.Dir(exe)
	// The settings and the history are found through APPDATA and
	// LOCALAPPDATA. Without them their folders would be looked for, and
	// emptied, under the current folder.
	appData, localAppData := winapi.AppData(), winapi.LocalAppData()
	if !filepath.IsAbs(appData) || !filepath.IsAbs(localAppData) {
		return errors.New("APPDATA and LOCALAPPDATA must name folders: without them the settings and the history cannot be found")
	}
	settings := filepath.Dir(config.Path(appData))
	state := filepath.Join(localAppData, "wsl-shrinker")
	// Scoop keeps the program in a folder of its own and knows it: removed
	// behind its back, it would still list it. The settings and the
	// history are no business of Scoop's.
	scoop := byScoop(dir)
	if scoop && !purge {
		return hint(errors.New("this wsl-shrinker was installed with Scoop"), "scoop uninstall wsl-shrinker removes it; wsl-shrinker uninstall --purge removes the settings and the history first")
	}
	if err := uninstallBlocked(exe, dir, state, scoop); err != nil {
		return err
	}
	// The folder goes, and its PATH entry with it, only when it is the
	// install's own: named for the program, with nothing else in it. Scoop's
	// never is: its manifest is in it.
	own, why := ownFolder(dir)
	if !yes {
		// Whether the PATH has an entry for the folder, for the plan to say;
		// read only.
		inPath := false
		if value, _, err := userPath(); own && err == nil {
			_, n := pathWithout(value, dir, os.LookupEnv)
			inPath = n > 0
		}
		sayPlan(cmd.ErrOrStderr(), exe, dir, settings, state, scoop, own, inPath, why, purge)
		q := "Uninstall? [y/N] "
		if scoop {
			q = "Remove them? [y/N] "
		}
		if err := confirm(cmd, q, "not uninstalled"); err != nil {
			return err
		}
		// The question may have waited long: a run, another copy of the
		// program or a screen may have started meanwhile.
		if err := uninstallBlocked(exe, dir, state, scoop); err != nil {
			return err
		}
	}
	return uninstallNow(cmd.OutOrStdout(), exe, dir, settings, state, scoop, own, why, purge)
}

// recoverFirst is what to do about a run cut off before an uninstall: only
// this program starts again what that run stopped.
const recoverFirst = "wsl-shrinker recover finishes it, or wsl-shrinker recover --forget closes it as it is; then uninstall"

// uninstallBlocked says what stands in the way of an uninstall now: a run
// not ended, which holds a distribution and needs the program; and, for a
// program not Scoop's, a copy of it still running, or a screen open in WSL.
func uninstallBlocked(exe, dir, state string, scoop bool) error {
	j := journal.Open(state, journalAlive)
	j.Since = winapi.ProcessStart
	e, st, err := j.Read()
	if err != nil {
		return err
	}
	switch {
	case st == journal.Going:
		return hint(fmt.Errorf("%s is being shrunk by another wsl-shrinker", e.Name()), "let it finish, then uninstall")
	case st == journal.CutOff && e.Disk == "":
		return hint(errors.New("a run was cut off"), recoverFirst)
	case st == journal.CutOff:
		return hint(fmt.Errorf("the run of %s was cut off at %s", e.Disk, e.Step), recoverFirst)
	}
	if scoop {
		return nil
	}
	// A copy still running holds the program's file: it would stay behind,
	// and with it the folder. A list Windows does not give stops nothing:
	// the removal after the end tries for half a minute.
	if n, err := othersRunning(exe); err == nil && n > 0 {
		return hint(fmt.Errorf("another wsl-shrinker is running from %s", dir), "close it, then uninstall")
	}
	// A screen open in WSL runs the launcher, and WSL holds its file:
	// Windows would remove it, and the screen's next question to the
	// program would fail. It is closed first.
	for _, f := range programFiles[1:] {
		if p := filepath.Join(dir, f); exists(p) && inUse(p) {
			return hint(fmt.Errorf("%s is in use", p), "close the wsl-shrinker screens open in WSL, then uninstall")
		}
	}
	return nil
}

// uninstallNow removes what the plan said, in the order that leaves the
// least behind should a step fail: the PATH entry first, the one step
// Windows may refuse, while all of the program is there to try again; then
// the files beside the program, the settings and the history, and last the
// program itself, which goes once it has ended.
func uninstallNow(out io.Writer, exe, dir, settings, state string, scoop, own bool, why string, purge bool) error {
	var notes []string
	// What the settings and the history folders hold before anything goes.
	kept := filled(settings, state)
	if own {
		// The PATH as stored, its other entries kept as they are written.
		value, expand, err := userPath()
		if err == nil {
			if rest, n := pathWithout(value, dir, os.LookupEnv); n > 0 {
				if err = setUserPath(rest, expand); err == nil {
					notes = append(notes, "took it out of your PATH: terminals opened from now on no longer find it")
				}
			}
		}
		if err != nil {
			return hint(fmt.Errorf("could not take %s out of your PATH: %w", dir, err), "nothing was removed; wsl-shrinker uninstall tries again")
		}
	}
	if !scoop {
		// The launcher and the copies: the program's own file is in use by
		// this process until it ends.
		for _, f := range programFiles[1:] {
			// Only the program's files: a folder by such a name is the
			// person's.
			p := filepath.Join(dir, f)
			if st, err := os.Lstat(p); err != nil || !st.Mode().IsRegular() {
				continue
			}
			if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
				return hint(err, exe+" uninstall tries again")
			}
		}
	}
	// The settings and the history: removed with --purge, file by file,
	// or said where they stay.
	var left []string
	if purge {
		// This run's own crash file is held until it ends, which then
		// removes it, and the folder with it once it is empty.
		left = append(purgeDir(settings, settingsFiles, ""), purgeDir(state, stateFiles, ownCrash)...)
	}
	purged := ""
	switch {
	case len(kept) == 0:
		// Nothing was there: nothing to say.
	case purge && len(left) == 0:
		purged = "removed the settings and the history"
	case purge:
		purged = "removed the settings and the history; kept " + strings.Join(left, " and ") + ": other files are in it"
	case len(kept) == 1:
		purged = "kept " + kept[0] + ": delete that folder to remove it too"
	default:
		purged = "kept " + strings.Join(kept, " and ") + ": delete those folders to remove them too"
	}
	if scoop {
		head := purged
		if head == "" {
			head = "found no settings or history to remove"
		}
		sayDone(out, head+"\nnote: scoop uninstall wsl-shrinker removes the program")
		return nil
	}
	// The program itself, once it has ended; and its folder, when it is
	// the install's own.
	head, folder := "uninstalled wsl-shrinker from "+dir, ""
	if own {
		folder = dir
	}
	// Windows removes no folder a terminal is in: looked at before the
	// removal starts.
	held := own && inside(dir)
	if err := deleteAfterExit(exe, folder); err != nil {
		head += ", but for the program itself"
		left := exe
		if own {
			left = dir
		}
		notes = append(notes, "remove "+left+" by hand once this program has ended: "+err.Error())
	}
	switch {
	case !own:
		notes = append(notes, "kept "+dir+" and any PATH entry for it: "+why)
	case held:
		notes = append(notes, "the folder stays while this terminal is in it: remove "+dir+" once you have left it")
	}
	if purged != "" {
		notes = append(notes, purged)
	}
	for i := range notes {
		notes[i] = "note: " + notes[i]
	}
	sayDone(out, strings.Join(append([]string{head}, notes...), "\n"))
	return nil
}

// inside reports whether the current folder is dir or a folder in it.
func inside(dir string) bool {
	wd, err := os.Getwd()
	if err != nil {
		return false
	}
	wd, dir = strings.ToLower(filepath.Clean(wd)), strings.ToLower(filepath.Clean(dir))
	return wd == dir || strings.HasPrefix(wd, dir+string(filepath.Separator))
}

// sayPlan says what an uninstall removes and what it keeps, one thing to a
// line, before the question. inPath says whether the PATH has an entry for
// an install's own folder.
func sayPlan(w io.Writer, exe, dir, settings, state string, scoop, own, inPath bool, why string, purge bool) {
	l := lookFor(w)
	line := func(verb, what string) { fmt.Fprintf(w, "  %s  %s\n", l.muted(verb), what) }
	fmt.Fprintln(w, "wsl-shrinker uninstall:")
	switch {
	case scoop:
		line("keeps  ", "the program: scoop uninstall wsl-shrinker removes it")
	case own && inPath:
		line("removes", exe+" and its launcher for WSL")
		line("removes", "the folder "+dir+" and its entry in your PATH")
	case own:
		line("removes", exe+" and its launcher for WSL")
		line("removes", "the folder "+dir)
	default:
		line("removes", exe+" and its launcher for WSL")
		line("keeps  ", "the folder "+dir+": "+why)
	}
	// A folder with nothing in it is not named: this run makes the
	// history's for its own crash file, which it removes as it ends.
	for _, f := range []struct{ what, dir, it string }{{"the settings in ", settings, "them"}, {"the history in ", state, "it"}} {
		switch {
		case !holds(f.dir):
		case purge:
			line("removes", f.what+f.dir)
		default:
			line("keeps  ", f.what+f.dir+" (--purge removes "+f.it+")")
		}
	}
}

// ownFolder reports whether dir is a folder an install made: holding
// nothing but the program's files, and named for the program. If it is
// not, it says why the folder stays.
func ownFolder(dir string) (bool, string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err.Error()
	}
	for _, e := range entries {
		// A folder by a program file's name is not the program's.
		mine := false
		for _, f := range programFiles {
			mine = mine || e.Type().IsRegular() && strings.EqualFold(e.Name(), f)
		}
		if !mine {
			return false, "other files are in it"
		}
	}
	// A folder of the person's own may hold the program alone, as a bin
	// folder on the PATH for it.
	if !strings.EqualFold(filepath.Base(dir), "wsl-shrinker") {
		return false, "it is not named wsl-shrinker"
	}
	return true, ""
}

// holds reports whether dir has anything in it but this run's own crash
// file.
func holds(dir string) bool {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if filepath.Join(dir, e.Name()) != ownCrash {
			return true
		}
	}
	return false
}

// filled is those of the settings and the history folders that hold
// something, named as the person reads them.
func filled(settings, state string) []string {
	var out []string
	if holds(settings) {
		out = append(out, "the settings in "+settings)
	}
	if holds(state) {
		out = append(out, "the history in "+state)
	}
	return out
}

// byScoop reports whether Scoop installed the program in dir: Scoop keeps
// an app in apps\NAME\VERSION (or apps\NAME\current, the link to it), with
// the manifest it installed from and its own record beside the program.
func byScoop(dir string) bool {
	app, apps := filepath.Dir(dir), filepath.Dir(filepath.Dir(dir))
	if !strings.EqualFold(filepath.Base(app), "wsl-shrinker") || !strings.EqualFold(filepath.Base(apps), "apps") {
		return false
	}
	// Scoop names them scoop-manifest.json and scoop-install.json; an
	// install by an older Scoop has manifest.json and install.json.
	for _, f := range scoopFiles {
		if exists(filepath.Join(dir, f)) {
			return true
		}
	}
	return false
}

// scoopFiles are the files Scoop writes beside an app it installed.
var scoopFiles = []string{"scoop-manifest.json", "scoop-install.json", "manifest.json", "install.json"}

// purgeDir removes the files in dir whose names one of names matches, but
// keep, then dir itself once nothing is left in it. It returns dir when
// something else stays there, and nothing when the folder is gone or goes
// with keep.
func purgeDir(dir string, names []*regexp.Regexp, keep string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	other := false
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		mine := false
		for _, re := range names {
			mine = mine || re.MatchString(e.Name())
		}
		switch {
		case p == keep:
			// Removed by the run that holds it, as it ends.
		case !mine:
			other = true
		default:
			if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
				other = true
			}
		}
	}
	// Never RemoveAll: only an empty folder goes.
	_ = os.Remove(dir)
	if other {
		return []string{dir}
	}
	return nil
}

// pathWithout is the PATH value without its entries for dir, and how many
// went. An entry is compared as Windows reads it: its %NAME% variables
// expanded, quotes and a trailing backslash dropped, without regard to
// case. Every other entry is kept as it is written, empty ones too.
func pathWithout(value, dir string, lookup func(string) (string, bool)) (string, int) {
	want := pathKey(dir, lookup)
	var kept []string
	n := 0
	for _, entry := range splitPath(value) {
		if entry != "" && pathKey(entry, lookup) == want {
			n++
			continue
		}
		kept = append(kept, entry)
	}
	if n == 0 {
		return value, 0
	}
	return strings.Join(kept, ";"), n
}

// splitPath splits a PATH value at its semicolons, but for those inside
// quotes: a quoted entry may hold one.
func splitPath(value string) []string {
	var out []string
	start, quoted := 0, false
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case '"':
			quoted = !quoted
		case ';':
			if !quoted {
				out = append(out, value[start:i])
				start = i + 1
			}
		}
	}
	return append(out, value[start:])
}

// pathKey is a PATH entry as compared: expanded, unquoted, without its
// trailing backslashes, in lower case.
func pathKey(entry string, lookup func(string) (string, bool)) string {
	s := strings.Trim(strings.TrimSpace(expandVars(entry, lookup)), `"`)
	return strings.ToLower(strings.TrimRight(s, `\/`))
}

// expandVars replaces each %NAME% in s with the variable's value, as
// Windows expands a PATH entry: a name that has no value stays as it is
// written, percent signs and all.
func expandVars(s string, lookup func(string) (string, bool)) string {
	var b strings.Builder
	for {
		i := strings.IndexByte(s, '%')
		if i < 0 {
			break
		}
		j := strings.IndexByte(s[i+1:], '%')
		if j < 0 {
			break
		}
		name := s[i+1 : i+1+j]
		if v, ok := lookup(name); ok && name != "" {
			b.WriteString(s[:i] + v)
			s = s[i+j+2:]
			continue
		}
		// Not a variable: the first % is text, and the second may open one.
		b.WriteString(s[:i+1])
		s = s[i+1:]
	}
	return b.String() + s
}
