package disks

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kanggu12/wsl-shrinker/internal/machine/winapi"
	"github.com/kanggu12/wsl-shrinker/internal/machine/wsl"
	"github.com/kanggu12/wsl-shrinker/internal/testutil/vhdxtest"
)

func TestFind(t *testing.T) {
	dir := t.TempDir()
	// Two distributions and a Docker Desktop data disk, each a VHDX file.
	small := filepath.Join(dir, "small.vhdx")
	big := filepath.Join(dir, "big.vhdx")
	docker := filepath.Join(dir, `Docker\wsl\disk\docker_data.vhdx`)
	// On Windows the path has folders; elsewhere the backslashes are part of
	// one file name, as discovery joins it the same way.
	if err := os.MkdirAll(filepath.Dir(docker), 0o755); err != nil {
		t.Fatal(err)
	}
	vhdxtest.Write(t, small, vhdxtest.Spec{VirtualSize: 8 << 20, Blocks: map[int64][]byte{0: {1}}})
	vhdxtest.Write(t, big, vhdxtest.Spec{VirtualSize: 8 << 20, Blocks: map[int64][]byte{0: {1}, 1: {1}}})
	vhdxtest.Write(t, docker, vhdxtest.Spec{VirtualSize: 8 << 20})
	fake := &wsl.Fake{List: []wsl.Distro{
		{Name: "Small", VHD: small, Version: 2, Default: true, Running: true},
		{Name: "Big", VHD: big, Version: 2},
		{Name: "Legacy", Version: 1},
		{Name: "Gone", VHD: filepath.Join(dir, "missing.vhdx"), Version: 2},
	}}
	// The host files are padded: as if 50 MiB were stored and freed in each.
	env := Env{
		WSL: fake,
		Stat: func(p string) (winapi.FileInfo, error) {
			fi, err := winapi.Stat(p)
			fi.OnDisk += 50 << 20
			return fi, err
		},
		Open:         OpenFile,
		LocalAppData: dir,
	}
	got, err := Find(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	// WSL 1 has no disk; the rest are listed, default first, then by gain.
	names := []string{}
	for _, d := range got {
		names = append(names, d.Name)
	}
	// Big and the docker disk give back the same; ties keep the order found.
	want := []string{"Small", "Big", "Docker Desktop data", "Gone"}
	if len(names) != len(want) {
		t.Fatalf("got %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("got %v, want %v", names, want)
		}
	}
	// A disk that could not be read says why and has no estimate.
	if got[3].Estimate != nil || got[3].Err == "" {
		t.Fatalf("missing file: %+v", got[3])
	}
	// The docker disk is found under LOCALAPPDATA and is not a distribution.
	if got[2].Kind != KindDocker || got[2].Distro != "" {
		t.Fatalf("docker disk: %+v", got[2])
	}
	// The padding comes back, and the estimate follows the stored blocks.
	if e := got[0].Estimate; e == nil || e.Reclaim < 50<<20 || e.Stored != 1<<20 {
		t.Fatalf("small: %+v", got[0].Estimate)
	}
	if !got[0].Running || !got[0].Default {
		t.Fatalf("state lost: %+v", got[0])
	}
}
