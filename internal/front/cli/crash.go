package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"

	"github.com/kanggu12/wsl-shrinker/internal/machine/winapi"
)

// A crash no recover can catch (a fatal error such as a deadlock, or a
// panic past every recover) ends the program with Go's own words, which
// cannot be changed as it happens. So the Go runtime also writes them into
// a file of the run's own, crash-PID-START.txt next to the history, and a
// later start says where they are: the run's process id and the moment it
// started name it, so a later run given the same id never takes it over. A
// run that ends cleanly removes its file. A crash, once said, is renamed
// crash-PID-START-shown.txt, which is also how one run, and only one, gets
// to say it.

// crashOutput is the runtime's setting for that file; tests replace it.
var crashOutput = debug.SetCrashOutput

// crashFile and shownFile match the two kinds of file, with the process id
// of the run that wrote it.
var (
	crashFile = regexp.MustCompile(`^crash-(\d+)-\d+\.txt$`)
	shownFile = regexp.MustCompile(`^crash-\d+-\d+-shown\.txt$`)
)

// keptCrashes is how many crashes of each kind wait at most, not yet said
// (as in runs whose stderr no person reads) or said: past that, the oldest
// go.
const keptCrashes = 10

// crashDir is where the crash files go: next to the history.
func crashDir() string { return filepath.Join(winapi.LocalAppData(), "wsl-shrinker") }

// alive reports whether a process of this program runs with the id pid.
func alive(pid int) bool {
	exe, _ := os.Executable()
	return winapi.Running(pid, filepath.Base(exe))
}

// WatchCrashes has a crash of this run kept for a later start, and returns
// what to do as the run ends: remove this run's file and, when a person
// reads stderr, say where an earlier run's crash was kept, once.
func WatchCrashes() (end func()) {
	person := term.IsTerminal(os.Stderr.Fd()) || relayed(2)
	return watchCrashes(crashDir(), os.Getpid(), alive, person, lookFor(os.Stderr), os.Stderr)
}

// ownCrash is this run's crash file, once watchCrashes made it.
var ownCrash string

// watchCrashes is WatchCrashes in dir, for the run pid; person says whether
// someone reads errOut, in the look l.
func watchCrashes(dir string, pid int, alive func(int) bool, person bool, l look, errOut io.Writer) func() {
	crashed := crashScan(dir, pid, alive, "")
	// This run's own, which the runtime writes only if it crashes.
	own := filepath.Join(dir, fmt.Sprintf("crash-%d-%d.txt", pid, time.Now().UnixNano()))
	ownCrash = own
	watching := false
	if os.MkdirAll(dir, 0o755) == nil {
		if f, err := os.Create(own); err == nil {
			watching = crashOutput(f, debug.CrashOptions{}) == nil
			// The runtime keeps a copy of its own.
			f.Close()
		}
	}
	return func() {
		// A clean end: the runtime lets go of the file, which then goes.
		if watching {
			_ = crashOutput(nil, debug.CrashOptions{})
			_ = os.Remove(own)
			// The folder goes too once nothing is left in it: inside WSL it
			// holds crash files alone, and uninstall --purge leaves this
			// run's own to this moment. Every writer makes it again.
			_ = os.Remove(dir)
		}
		// A script's stderr leaves them for a later start in a terminal.
		if person {
			sayCrashes(errOut, l, claimCrashes(crashed))
		}
	}
}

// crashScan looks at dir as the run pid starts. It removes what is not
// needed (the empty file of a run that ended without removing it, killed
// or replaced by the .exe; crashes said past the newest keptCrashes) and
// returns the crashes of earlier runs not said yet, oldest first, the
// newest keptCrashes of them. A run still going keeps its file.
func crashScan(dir string, pid int, alive func(int) bool, own string) []string {
	entries, _ := os.ReadDir(dir)
	var crashed, shown []string
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		// This run's own file is not an earlier run's.
		if p == own {
			continue
		}
		if shownFile.MatchString(e.Name()) {
			shown = append(shown, p)
			continue
		}
		m := crashFile.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		// One with this run's id, other than its own, is an earlier run's,
		// given the same id. Any other live run keeps its.
		other, err := strconv.Atoi(m[1])
		if err != nil || (other != pid && alive(other)) {
			continue
		}
		if info, err := e.Info(); err == nil && info.Size() > 0 {
			crashed = append(crashed, p)
		} else {
			_ = os.Remove(p)
		}
	}
	// The said ones wait only to be read: the newest few.
	newest(shown, keptCrashes)
	return newest(crashed, keptCrashes)
}

// newest keeps the newest n of files, oldest first, and removes the rest.
func newest(files []string, n int) []string {
	slices.SortFunc(files, func(a, b string) int { return modTime(a).Compare(modTime(b)) })
	for len(files) > n {
		_ = os.Remove(files[0])
		files = files[1:]
	}
	return files
}

// claimCrashes marks crashes as said by renaming each to its -shown name:
// a rename happens once, so a crash another run took first is not this
// one's to say. It returns the files this run took, oldest first.
func claimCrashes(crashed []string) []string {
	var shown []string
	for _, p := range crashed {
		to := strings.TrimSuffix(p, ".txt") + "-shown.txt"
		if os.Rename(p, to) == nil {
			shown = append(shown, to)
		}
	}
	return shown
}

// sayCrashes says where crashes of earlier runs were kept, the newest
// first: two lines whatever their number.
func sayCrashes(errOut io.Writer, l look, shown []string) {
	if len(shown) == 0 {
		return
	}
	what := "an earlier run stopped on a bug; what it printed is in " + shown[len(shown)-1]
	if n := len(shown) - 1; n > 0 {
		what += fmt.Sprintf(", and %d more beside it", n)
	}
	fmt.Fprintln(errOut, l.warn("!")+" "+what)
	fmt.Fprintln(errOut, "  "+l.muted("→ please report it with that file: "+IssuesURL))
}

// SayCrashes says where crashes another program kept were: the launcher's
// screen asks wsl-shrinker.exe for its own as it ends.
func SayCrashes(errOut io.Writer, shown []string) { sayCrashes(errOut, lookFor(errOut), shown) }

// ClaimCrashes takes the crashes this PC's runs kept and did not say yet,
// as watchCrashes does at a run's end: for the launcher's screen, through
// the backend.
func ClaimCrashes() []string {
	return claimCrashes(crashScan(crashDir(), os.Getpid(), alive, ownCrash))
}

// modTime is when a file was last written.
func modTime(p string) time.Time {
	info, err := os.Stat(p)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}
