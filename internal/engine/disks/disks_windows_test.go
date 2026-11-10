//go:build windows

package disks

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDockersFolderOnANetworkShareIsNotRead(t *testing.T) {
	// Docker's settings naming a share: reading the disk there would send
	// the person's Windows credentials to that host at every listing.
	appData := t.TempDir()
	if err := os.MkdirAll(filepath.Join(appData, "Docker"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Z: is a drive Windows maps to a share, as `net use` makes one.
	old := remoteDrive
	remoteDrive = func(root string) bool { return root == `Z:\` }
	t.Cleanup(func() { remoteDrive = old })
	for content, want := range map[string]bool{
		`{"CustomWslDistroDir": "\\\\host\\share\\DockerDesktopWSL"}`: false,
		`{"CustomWslDistroDir": "\\\\?\\UNC\\host\\share\\x"}`:        false,
		`{"CustomWslDistroDir": "Z:\\DockerDesktopWSL"}`:              false,
		`{"CustomWslDistroDir": "D:\\DockerDesktopWSL"}`:              true,
		// The same drive, written as a Windows device path.
		`{"CustomWslDistroDir": "\\\\?\\D:\\DockerDesktopWSL"}`: true,
		`{"CustomWslDistroDir": "\\\\.\\D:\\DockerDesktopWSL"}`: true,
	} {
		if err := os.WriteFile(filepath.Join(appData, "Docker", "settings-store.json"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := movedDockerDisk(appData) != ""; got != want {
			t.Errorf("%s: used %v, want %v", content, got, want)
		}
	}
}

func TestTheSystemDriveIsNotAShare(t *testing.T) {
	// Windows' own answer for the drive it runs from, never a share.
	if d := os.Getenv("SystemDrive"); d == "" || remoteDrive(d+`\`) {
		t.Fatalf("the system drive %q is said to be a share", d)
	}
}
