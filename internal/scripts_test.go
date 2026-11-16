package internal

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTheInstallScriptIsASCII(t *testing.T) {
	// `irm ... | iex` in Windows PowerShell 5.1 decodes a download that names
	// no character set as Latin-1, and GitHub names none for a release
	// asset: a character outside ASCII would reach the script as two or three
	// others, in a message or in the code itself.
	root, _ := moduleRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "scripts", "install.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range bytes.Split(b, []byte("\n")) {
		for _, c := range line {
			if c > 0x7f {
				t.Errorf("scripts/install.ps1:%d holds a character outside ASCII: %q", i+1, line)
				break
			}
		}
	}
}

func TestTheInstallScriptUnpacksWithDotNet(t *testing.T) {
	// Expand-Archive is whichever Archive module comes first on
	// PSModulePath. Windows PowerShell started from WSL or cmd in a
	// PowerShell 7 window inherits 7's path, and 7's Expand-Archive calls an
	// ExtractToDirectory that .NET Framework does not have: the install
	// stopped there. The script unpacks with .NET's own zip support.
	root, _ := moduleRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "scripts", "install.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(string(b), "\n") {
		// Only the code: a comment may name the cmdlet, as the script's own
		// does to say why it is not used.
		code, _, _ := strings.Cut(line, "#")
		if strings.Contains(code, "Expand-Archive") {
			t.Errorf("scripts/install.ps1:%d unpacks with Expand-Archive: %s", i+1, strings.TrimSpace(line))
		}
	}
	if !strings.Contains(string(b), "[IO.Compression.ZipFile]::ExtractToDirectory(") {
		t.Error("scripts/install.ps1 does not unpack with [IO.Compression.ZipFile]::ExtractToDirectory")
	}
}
