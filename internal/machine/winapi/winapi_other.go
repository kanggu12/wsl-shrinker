//go:build !windows

package winapi

import (
	"os"
	"path/filepath"
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
