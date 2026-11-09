package shrink

import (
	"context"
	"strings"
	"testing"
)

// wsl.exe merges stderr into the output Run returns (wslExe: Stdout and
// Stderr into one buffer). A distro with an unmapped drive on PATH gets a
// UtilTranslatePathList warning on every launch. The root is read-only.
func TestReadOnlyWithWarningLine(t *testing.T) {
	w := newWorld(t)
	inner := w.fake.Script
	w.fake.Script = func(a, b, s string) (string, error) {
		if strings.Contains(s, "/proc/mounts") {
			return "<3>WSL (12) ERROR: UtilTranslatePathList:2866: Failed to translate Z:\\tools\nro,relatime\n", nil
		}
		return inner(a, b, s)
	}
	ro, err := readOnly(context.Background(), w.env, "Dev")
	t.Logf("readOnly=%v err=%v", ro, err)
	if !ro {
		t.Errorf("a read-only root behind a warning line reads as writable")
	}
}
