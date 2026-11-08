package internal

import (
	"bytes"
	"os"
	"path/filepath"
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
