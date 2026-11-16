//go:build !windows

package main

import (
	"strings"
	"testing"

	"github.com/kanggu12/wsl-shrinker/internal/version"
)

// TestTheInstallHintNamesItsOwnRelease: the launcher, finding no
// wsl-shrinker.exe, tells to run the Windows installer of its own release, by the
// name scripts/dist.sh gives it; a source build, of no release, points at
// the releases instead.
func TestTheInstallHintNamesItsOwnRelease(t *testing.T) {
	was := version.Version
	defer func() { version.Version = was }()
	version.Version = "v1.2.3"
	if got, want := installHint(), "irm https://github.com/kanggu12/wsl-shrinker/releases/download/v1.2.3/wsl-shrinker-1.2.3-windows-installer.ps1 | iex"; got != want {
		t.Errorf("installHint() = %q, want %q", got, want)
	}
	// A pre-release's installer is named with its whole version too.
	version.Version = "v1.3.0-rc.1"
	if got := installHint(); !strings.Contains(got, "/download/v1.3.0-rc.1/wsl-shrinker-1.3.0-rc.1-windows-installer.ps1 ") {
		t.Errorf("a pre-release's hint does not name its installer: %q", got)
	}
	version.Version = ""
	if got := installHint(); strings.HasPrefix(got, "irm") || !strings.Contains(got, "/releases/latest") {
		t.Errorf("a source build's hint does not point at the releases: %q", got)
	}
}
