package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestExitCodes(t *testing.T) {
	// The table in docs/commands.md: 0 done, 1 failed, 2 wrong usage.
	for _, c := range []struct {
		args []string
		want int
	}{
		{[]string{"version"}, exitOK},
		{[]string{"--snapshot", "80x24"}, exitOK},
		{[]string{"nonsense"}, exitUsage},
		{[]string{"shrink"}, exitUsage},
		{[]string{"inside"}, exitUsage},
		{[]string{"list", "extra"}, exitUsage},
		{[]string{"list", "--bogus"}, exitUsage},
		{[]string{"--snapshot", "8x2"}, exitUsage},
		{[]string{"--lang", "fr", "version"}, exitUsage},
	} {
		var out, errOut bytes.Buffer
		if got := run(c.args, &out, &errOut); got != c.want {
			t.Errorf("%v: exit %d, want %d (%s)", c.args, got, c.want, strings.TrimSpace(errOut.String()))
		}
	}
}

func TestSnapshotPrintsTheDemo(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"--snapshot", "100x24", "--lang", "ko"}, &out, &errOut); code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "Ubuntu-24.04") || !strings.Contains(out.String(), "돌려받을 수 있음") {
		t.Fatalf("no Korean demo screen:\n%s", out.String())
	}
}
