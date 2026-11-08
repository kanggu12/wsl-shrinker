package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// write puts a settings file in a temporary folder.
func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDefaultsAndExample(t *testing.T) {
	// No file: the defaults.
	c, err := Load(filepath.Join(t.TempDir(), "none.toml"))
	if err != nil || c.Language != "auto" || c.MinGainGB != 1 {
		t.Fatalf("%+v %v", c, err)
	}
	// The example file loads to the same defaults.
	c, err = Load(write(t, Example))
	if err != nil || c.Theme != "auto" || len(c.Protect) != 0 {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestValues(t *testing.T) {
	c, err := Load(write(t, "language = \"ko\"\nprotect = [\"Work\"]\nextra_disks = ['D:\\VMs\\data.vhdx']\n"))
	if err != nil || c.Language != "ko" || !c.Protected("work") || c.Protected("other") || len(c.ExtraDisks) != 1 {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestRejects(t *testing.T) {
	for _, body := range []string{
		"language = \"fr\"",
		"theme = \"blue\"",
		"min_gain_gb = -1",
		"extra_disks = [\"C:\\\\x.vhd\"]",
		"protcet = [\"typo\"]",
		"language = ",
	} {
		if _, err := Load(write(t, body)); err == nil || !strings.Contains(err.Error(), "config.toml") {
			t.Errorf("%q: %v", body, err)
		}
	}
}

func TestRejectsOddNumbersAndPaths(t *testing.T) {
	// A gain that is not a number, or too large to count in bytes, would
	// mark every disk; a disk path must be a full Windows path, as the
	// program runs on Windows from any folder (a relative path would change
	// with it, and a WSL path is not one Windows opens).
	for _, body := range []string{
		"min_gain_gb = nan",
		"min_gain_gb = 1e30",
		`extra_disks = ['data\x.vhdx']`,
		`extra_disks = ['/mnt/c/x.vhdx']`,
		`extra_disks = ['C:x.vhdx']`,
	} {
		if _, err := Load(write(t, body)); err == nil || !strings.Contains(err.Error(), "config.toml") {
			t.Errorf("%q: %v", body, err)
		}
	}
	// Full paths with either slash, and network paths, are fine.
	for _, body := range []string{`extra_disks = ['C:/VMs/x.vhdx', 'D:\x.vhdx', '\\server\share\x.vhdx']`, "min_gain_gb = 0.5"} {
		if _, err := Load(write(t, body)); err != nil {
			t.Errorf("%q: %v", body, err)
		}
	}
}
