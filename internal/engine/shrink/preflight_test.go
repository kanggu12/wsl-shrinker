package shrink

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/kanggu12/wsl-shrinker/internal/engine/disks"
	"github.com/kanggu12/wsl-shrinker/internal/engine/estimate"
	"github.com/kanggu12/wsl-shrinker/internal/machine/winapi"
	"github.com/kanggu12/wsl-shrinker/internal/testutil/vhdxtest"
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
	e := &estimate.Result{FileSize: 1, FSErrors: true, Fixed: true}
	all := disks.Disk{Name: "Dev", Distro: "Dev", File: winapi.FileInfo{Encrypted: true}, Estimate: e}
	// Protected first, then what is wrong with the file, then WSL.
	if got := Preflight(all, false, true); !slices.Equal(got, []Block{BlockProtected, BlockEncrypted, BlockFSErrors, BlockFixed, BlockNoCompact}) {
		t.Fatalf("got %v", got)
	}
	// A fixed-size disk a program holds says it is fixed-size first: quitting
	// the program would give a disk that still cannot shrink.
	both := disks.Disk{Name: "Docker Desktop data", Kind: disks.KindDocker, File: winapi.FileInfo{Held: true}, Estimate: &estimate.Result{Fixed: true}}
	if got := Preflight(both, true, false); !slices.Equal(got, []Block{BlockFixed, BlockHeld}) {
		t.Fatalf("got %v", got)
	}
	// Its reason says what the disk is and why that ends it, in the words
	// the command line prints.
	if r := BlockFixed.Reason(both); !strings.Contains(r, "fixed-size") || !strings.Contains(r, "only disks that grow") {
		t.Fatalf("reason %q", r)
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

func TestDockerDesktopsOwnDistributionsAreHeldWhileTheyRun(t *testing.T) {
	// Docker Desktop runs docker-desktop and docker-desktop-data itself:
	// while they run it holds them, as it holds its data disk. Stopped,
	// they are distributions like any other.
	for _, name := range []string{"docker-desktop", "Docker-Desktop-Data"} {
		d := disks.Disk{Name: name, Distro: name, Running: true, File: winapi.FileInfo{Held: true}, Estimate: &estimate.Result{}}
		if got := Preflight(d, true, false); !slices.Equal(got, []Block{BlockHeld}) || !strings.Contains(BlockHeld.Reason(d), "Docker Desktop") {
			t.Fatalf("%s running: %v, %q", name, got, BlockHeld.Reason(d))
		}
		d.Running, d.File.Held = false, false
		if got := Preflight(d, true, false); len(got) != 0 {
			t.Fatalf("%s stopped: %v", name, got)
		}
	}
	// A distribution of the person's own with a like name is theirs.
	own := disks.Disk{Name: "docker-desktop-dev", Distro: "docker-desktop-dev", Running: true, File: winapi.FileInfo{Held: true}, Estimate: &estimate.Result{}}
	if got := Preflight(own, true, false); len(got) != 0 {
		t.Fatalf("own distribution: %v", got)
	}
}

func TestOtherProgramsDistributionsAreHeldWhileTheyRun(t *testing.T) {
	// Rancher Desktop and Podman run distributions of their own, as Docker
	// Desktop does: while they run, the program has them, and the reason
	// names what lets them go. Stopped, they shrink like any other.
	podman := `C:\Users\ann\.local\share\containers\podman\machine\wsl\wsldist\podman-machine-default\ext4.vhdx`
	for _, c := range []struct {
		name, path string
		owner      Owner
		says       string
	}{
		{"rancher-desktop", `C:\Users\ann\AppData\Local\rancher-desktop\distro\ext4.vhdx`, OwnerRancher, "quit Rancher Desktop"},
		{"Rancher-Desktop-Data", `D:\rd\data\ext4.vhdx`, OwnerRancher, "quit Rancher Desktop"},
		// A machine's name is the person's choice: where Podman put it says.
		{"podman-machine-default", podman, OwnerPodman, "podman machine stop"},
		{"podman-dev", strings.ReplaceAll(strings.ReplaceAll(podman, "podman-machine-default", "podman-dev"), `\`, "/"), OwnerPodman, "podman machine stop"},
		{"docker-desktop", `C:\Users\ann\AppData\Local\Docker\wsl\main\ext4.vhdx`, OwnerDocker, "quit Docker Desktop"},
	} {
		d := disks.Disk{Name: c.name, Kind: disks.KindDistro, Distro: c.name, Path: c.path, Running: true, Estimate: &estimate.Result{}}
		if got := OwnerOf(d); got != c.owner {
			t.Fatalf("%s: owner %q, want %q", c.name, got, c.owner)
		}
		if got := Preflight(d, true, false); !slices.Equal(got, []Block{BlockHeld}) || !strings.Contains(BlockHeld.Reason(d), c.says) {
			t.Fatalf("%s running: %v, %q", c.name, got, BlockHeld.Reason(d))
		}
		d.Running = false
		if got := Preflight(d, true, false); len(got) != 0 {
			t.Fatalf("%s stopped: %v", c.name, got)
		}
	}
	// The person's own: a name like Podman's in a folder of their own, and
	// a file from the settings in a folder like Podman's, which is no
	// distribution to stop.
	for _, d := range []disks.Disk{
		{Name: "podman-machine-default", Kind: disks.KindDistro, Distro: "podman-machine-default", Path: `D:\wsl\podman\ext4.vhdx`, Running: true},
		{Name: "ext4.vhdx", Kind: disks.KindFile, Path: podman},
	} {
		if got := OwnerOf(d); got != OwnerNone {
			t.Fatalf("%s at %s: owner %q", d.Name, d.Path, got)
		}
	}
	// Docker Desktop's data disk is its own, held or not.
	if got := OwnerOf(disks.Disk{Name: "Docker Desktop data", Kind: disks.KindDocker}); got != OwnerDocker {
		t.Fatalf("Docker Desktop's disk: owner %q", got)
	}
}

func TestAFixedSizeDiskIsRefusedBeforeAnythingStops(t *testing.T) {
	// Windows' compaction of a fixed-size disk reports success and leaves
	// the file as it is: a run would stop the distribution and its
	// containers for nothing. The check refuses the disk, a distribution's
	// or a file Windows itself compacts, and says why; the file is read
	// afresh for it, so the listing need not have said so.
	for name, as := range map[string]func(*disks.Disk){
		"a distribution":               func(*disks.Disk) {},
		"a file WSL does not register": func(d *disks.Disk) { d.Distro, d.Kind, d.Running = "", disks.KindFile, false },
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			d := w.disk()
			as(&d)
			// Listed as a disk that grows, with space to give back.
			if d.Estimate == nil || d.Estimate.Fixed || d.Estimate.Reclaim == 0 {
				t.Fatalf("listed as %+v", d.Estimate)
			}
			// The same file, written anew as a fixed-size disk: every block
			// in it, most of them zeros.
			blocks := map[int64][]byte{0: {1}, 2: {1}, 5: {1}}
			for i := int64(0); i < 16; i++ {
				if _, ok := blocks[i]; !ok {
					blocks[i] = []byte{}
				}
			}
			vhdxtest.Write(t, w.path, vhdxtest.Spec{VirtualSize: 16 << 20, Blocks: blocks, Fixed: true})
			compacted := false
			w.env.CompactFile = func(string) error { compacted = true; return nil }
			r := Run(context.Background(), w.env, d, Options{}, nil)
			if statuses(r)[StepCheck] != Failed || r.FailedAt != StepCheck || len(r.Events) != 2 || !strings.Contains(r.Err, "fixed-size") {
				t.Fatalf("%s %v", r.Err, r.Events)
			}
			// Nothing was stopped and nothing compacted, by WSL or by Windows.
			if compacted {
				t.Fatal("Windows was asked to compact it")
			}
			for _, c := range w.fake.CallList() {
				if !strings.HasPrefix(c, "run:") {
					t.Fatalf("changed something: %v", w.fake.CallList())
				}
			}
			// It runs as it did.
			if r.Left != LeftRunning && d.Distro != "" {
				t.Fatalf("left %q", r.Left)
			}
		})
	}
}
