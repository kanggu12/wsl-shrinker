package cli

import (
	"strings"
	"testing"

	"github.com/kanggu12/wsl-shrinker/internal/engine/shrink"
)

func TestARunLeftToAnotherStopIsSaidAsSuch(t *testing.T) {
	// The line under a failed run says another wsl-spawner stop ends the
	// distribution, not that it runs as it did.
	s := leftWords(shrink.Result{Disk: "Dev", FailedAt: shrink.StepStop, Left: shrink.LeftOtherStop})
	if !strings.Contains(s, "another wsl-spawner stop is ending Dev") || strings.Contains(s, "runs as it did") {
		t.Fatalf("%q", s)
	}
}
