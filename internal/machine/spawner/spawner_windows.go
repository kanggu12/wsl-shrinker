//go:build windows

package spawner

import (
	"os/exec"
	"syscall"
	"testing"

	"github.com/kanggu12/wsl-shrinker/internal/machine/winapi"
)

// Find finds wsl-spawner as findIn says, in the folders installFolders
// names for this user and this PC. A test never finds it: the program on
// this PC would stop a real distribution named as a test's made-up one.
func Find() (Exe, bool) {
	if testing.Testing() {
		return Exe{}, false
	}
	return findIn(exec.LookPath, isFile, installFolders(winapi.LocalAppData(), winapi.ProgramFiles())...)
}

// hideWindow keeps wsl-spawner from opening a console window of its own.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}
