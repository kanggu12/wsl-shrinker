//go:build windows

package spawner

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/kanggu12/wsl-shrinker/internal/machine/winapi"
)

// Find finds wsl-spawner: on the PATH, as its installer and Scoop put it
// there, or in its installer's own folder under %LOCALAPPDATA%\Programs,
// for a PATH this program started with before the install changed it. A
// test never finds it: the program on this PC would stop a real
// distribution named as a test's made-up one.
func Find() (Exe, bool) {
	if testing.Testing() {
		return Exe{}, false
	}
	// LookPath refuses a program found only in the current folder
	// (exec.ErrDot): one there is not an installed wsl-spawner. Any other
	// failure (none on the PATH) goes on to the installer's folder.
	if p, err := exec.LookPath("wsl-spawner"); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			return Exe{Path: abs}, true
		}
	}
	// The installer's folder, %LOCALAPPDATA%\Programs\wsl-spawner, unless
	// WSL_SPAWNER_BIN_DIR put it elsewhere (then it is on the PATH too).
	p := filepath.Join(winapi.LocalAppData(), "Programs", "wsl-spawner", "wsl-spawner.exe")
	if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
		return Exe{Path: p}, true
	}
	return Exe{}, false
}

// hideWindow keeps wsl-spawner from opening a console window of its own.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}
