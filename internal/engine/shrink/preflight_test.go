package shrink

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/kanggu12/wsl-shrinker/internal/engine/disks"
	"github.com/kanggu12/wsl-shrinker/internal/engine/estimate"
	"github.com/kanggu12/wsl-shrinker/internal/machine/winapi"
)

func TestProtectedDiskIsRefusedByTheEngine(t *testing.T) {
	// Protection holds whatever front asked for the run: the engine refuses
	// a protected disk at its check, before anything changes.
	w := newWorld(t)
	w.env.Protected = func(name string) bool { return strings.EqualFold(name, "dev") }
	r := Run(context.Background(), w.env, w.disk(), Options{}, nil)
	if statuses(r)[StepCheck] != Failed || !strings.Contains(r.Err, "protected") || r.FailedAt != StepCheck {
		t.Fatalf("%s %v", r.Err, statuses(r))
	}
	for _, c := range w.fake.CallList() {
		if !strings.HasPrefix(c, "run:") {
			t.Fatalf("changed something: %v", w.fake.CallList())
		}
	}
}

func TestPreflightListsEveryBlockMostPressingFirst(t *testing.T) {
	e := &estimate.Result{FileSize: 1, FSErrors: true}
	all := disks.Disk{Name: "Dev", Distro: "Dev", File: winapi.FileInfo{Encrypted: true}, Estimate: e}
	// Protected first, then what is wrong with the file, then WSL.
	if got := Preflight(all, false, true); !slices.Equal(got, []Block{BlockProtected, BlockEncrypted, BlockFSErrors, BlockNoCompact}) {
		t.Fatalf("got %v", got)
	}
	// A disk not read says only that.
	if got := Preflight(disks.Disk{Name: "x", Err: "boom"}, true, false); !slices.Equal(got, []Block{BlockUnreadable}) {
		t.Fatalf("got %v", got)
	}
	// A held file blocks; a held distribution is just running.
	held := disks.Disk{Name: "Docker Desktop data", Kind: disks.KindDocker, File: winapi.FileInfo{Held: true}, Estimate: &estimate.Result{}}
	if got := Preflight(held, true, false); !slices.Equal(got, []Block{BlockHeld}) || !strings.Contains(BlockHeld.Reason(held), "Docker Desktop") {
		t.Fatalf("got %v", got)
	}
	distro := disks.Disk{Name: "Dev", Distro: "Dev", File: winapi.FileInfo{Held: true}, Estimate: &estimate.Result{}}
	if got := Preflight(distro, true, false); len(got) != 0 {
		t.Fatalf("a running distribution is blocked: %v", got)
	}
}
