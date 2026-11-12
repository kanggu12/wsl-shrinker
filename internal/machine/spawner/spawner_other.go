//go:build !windows

package spawner

import "os/exec"

// Find finds no wsl-spawner off Windows: it is a Windows program, and no
// distribution runs here to stop.
func Find() (Exe, bool) { return Exe{}, false }

// hideWindow has no window to hide off Windows.
func hideWindow(*exec.Cmd) {}
