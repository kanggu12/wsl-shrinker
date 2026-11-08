//go:build !windows

package winapi

import (
	"os"
	"path/filepath"
	"strconv"

	"github.com/charmbracelet/x/term"
)

// Stat answers with the file's length only: off Windows there is nothing
// else to ask, and the tests use it on plain files.
func Stat(path string) (FileInfo, error) {
	st, err := os.Stat(path)
	if err != nil {
		return FileInfo{}, err
	}
	return FileInfo{Size: st.Size(), OnDisk: st.Size()}, nil
}

// FromWSL is false off Windows.
func FromWSL() bool { return false }

// Reopen cannot open a Windows terminal off Windows.
func Reopen([]string) error { return ErrNotWindows }

// Korean follows LANG off Windows, for the demo and the screenshots.
func Korean() bool {
	l := os.Getenv("LANG")
	return len(l) >= 2 && l[:2] == "ko"
}

// LocalAppData stands in with the user's cache folder off Windows.
func LocalAppData() string {
	d, _ := os.UserCacheDir()
	return filepath.Clean(d)
}

// AppData stands in with the user's config folder off Windows.
func AppData() string {
	d, _ := os.UserConfigDir()
	return filepath.Clean(d)
}

// CompactFile needs Windows' virtual disk service.
func CompactFile(string) error { return ErrNotWindows }

// Running reports whether process pid runs the program named exe, from
// /proc: off Windows only the tests and the demo run, but the journal's
// check works the same.
func Running(pid int, exe string) bool {
	target, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
	return err == nil && filepath.Base(target) == exe
}

// Colors reports whether fd is a terminal: off Windows every terminal takes
// escape-code colors.
func Colors(fd uintptr) bool { return term.IsTerminal(fd) }

// RestoreConsole has nothing to put back off Windows.
func RestoreConsole() {}

// ClassicConsole is false off Windows: a Linux terminal draws with fonts of
// its own choosing.
func ClassicConsole() bool { return false }

// OldConsoleHost is false off Windows: no Windows console draws there.
func OldConsoleHost() bool { return false }

// ConsoleEastAsian is false off Windows, which has no console code page.
func ConsoleEastAsian() bool { return false }
