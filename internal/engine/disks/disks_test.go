package disks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"

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

func TestExtraDisksGetNamesOfTheirOwn(t *testing.T) {
	// Other tools name their disks alike (ext4.vhdx): each one listed gets
	// a name no other disk has, so marking, protecting or naming one on the
	// command line means that one. The same file spelt twice is one disk.
	dir := t.TempDir()
	a := filepath.Join(dir, "a", "ext4.vhdx")
	b := filepath.Join(dir, "b", "ext4.vhdx")
	for _, p := range []string{a, b} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		vhdxtest.Write(t, p, vhdxtest.Spec{VirtualSize: 4 << 20})
	}
	env := Env{WSL: &wsl.Fake{}, Stat: winapi.Stat, Open: OpenFile,
		Extra: []string{a, b, filepath.Dir(a) + string(filepath.Separator) + "." + string(filepath.Separator) + "ext4.vhdx"}}
	got, err := Find(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("%d disks: %+v", len(got), got)
	}
	if got[0].Name == got[1].Name {
		t.Fatalf("both are called %q", got[0].Name)
	}
	// The second is named with its folder, in a form the settings' protect
	// list can hold as it is (no backslash to escape).
	if got[1].Name != "b/ext4.vhdx" {
		t.Fatalf("the second is called %q", got[1].Name)
	}
	var c struct{ Protect []string }
	if _, err := toml.Decode(`protect = ["`+got[1].Name+`"]`, &c); err != nil || c.Protect[0] != got[1].Name {
		t.Fatalf("the name does not go into the settings as it is: %v %v", c.Protect, err)
	}
}

func TestDockerDiskMovedInItsSettingsIsFound(t *testing.T) {
	// Docker Desktop can move its data disk ("Disk image location"): its
	// settings, written with a byte order mark by some releases, name the
	// folder. The disk there is Docker's; one left in the default folder
	// is listed too, under a name of its own.
	appData, local, moved := t.TempDir(), t.TempDir(), t.TempDir()
	disk := filepath.Join(moved, "disk", "docker_data.vhdx")
	left := filepath.Join(local, `Docker\wsl\disk\docker_data.vhdx`)
	for _, p := range []string{disk, left} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		vhdxtest.Write(t, p, vhdxtest.Spec{VirtualSize: 4 << 20})
	}
	settings, err := json.Marshal(map[string]any{"CustomWslDistroDir": moved, "AutoStart": false})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(appData, "Docker"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appData, "Docker", "settings-store.json"), append([]byte("\xef\xbb\xbf"), settings...), 0o644); err != nil {
		t.Fatal(err)
	}
	env := Env{WSL: &wsl.Fake{}, Stat: winapi.Stat, Open: OpenFile, LocalAppData: local, AppData: appData}
	got, err := Find(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Path != disk || got[0].Name != "Docker Desktop data" || got[0].Kind != KindDocker {
		t.Fatalf("%+v", got)
	}
	if got[1].Path != left || got[1].Name == got[0].Name || got[1].Kind != KindDocker {
		t.Fatalf("the one left behind: %+v", got[1])
	}
	// Settings that do not name a folder, or cannot be read, change nothing.
	for _, content := range []string{`{"AutoStart": true}`, `{`, ``, `{"CustomWslDistroDir": "DockerDesktopWSL"}`} {
		if err := os.WriteFile(filepath.Join(appData, "Docker", "settings-store.json"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if p := movedDockerDisk(appData); p != "" {
			t.Errorf("%q: %q", content, p)
		}
	}
}

func TestDockerDiskIsEstimatedWithoutATrim(t *testing.T) {
	// Docker Desktop's disk is compacted by Windows without a trim: its
	// estimate is the untrimmed one, while a distribution's is trimmed.
	dir := t.TempDir()
	docker := filepath.Join(dir, `Docker\wsl\disk\docker_data.vhdx`)
	distro := filepath.Join(dir, "d.vhdx")
	if err := os.MkdirAll(filepath.Dir(docker), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{docker, distro} {
		vhdxtest.Write(t, p, vhdxtest.Spec{VirtualSize: 4 << 20, Blocks: map[int64][]byte{0: {1}}})
	}
	env := Env{WSL: &wsl.Fake{List: []wsl.Distro{{Name: "D", VHD: distro, Version: 2}}}, Stat: winapi.Stat, Open: OpenFile, LocalAppData: dir}
	got, err := Find(context.Background(), env)
	if err != nil || len(got) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	for _, d := range got {
		if d.Estimate == nil || d.Estimate.Trimmed != (d.Kind == KindDistro) {
			t.Fatalf("%s: %+v", d.Name, d.Estimate)
		}
	}
}

func TestDiskJSONKeys(t *testing.T) {
	b, err := json.Marshal(Disk{Name: "n", Kind: KindDistro, Path: "p"})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"name":"n","kind":"distro","path":"p","file":{"size_bytes":0,"on_disk_bytes":0,"sparse":false,"compressed":false,"encrypted":false,"held":false,"drive_free_bytes":0}}`
	if string(b) != want {
		t.Fatalf("got  %s\nwant %s", b, want)
	}
}

func TestDockerDisksNeverTakeAFilesName(t *testing.T) {
	// A file from the settings is listed, and protected, by its file name.
	// A Docker Desktop disk an older release left behind, found later,
	// takes a name of Docker Desktop's own: the file keeps its name, and
	// with it its protection.
	dir, local := t.TempDir(), t.TempDir()
	extra := filepath.Join(dir, "a", "ext4.vhdx")
	current := filepath.Join(local, `Docker\wsl\disk\docker_data.vhdx`)
	old := filepath.Join(local, `Docker\wsl\data\ext4.vhdx`)
	write := func(p string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		vhdxtest.Write(t, p, vhdxtest.Spec{VirtualSize: 4 << 20})
	}
	write(extra)
	write(current)
	names := func() map[string]string {
		env := Env{WSL: &wsl.Fake{}, Stat: winapi.Stat, Open: OpenFile, LocalAppData: local, Extra: []string{extra}}
		got, err := Find(context.Background(), env)
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]string{}
		for _, d := range got {
			m[d.Path] = d.Name
		}
		return m
	}
	before := names()
	write(old)
	after := names()
	if before[extra] != "ext4.vhdx" || after[extra] != "ext4.vhdx" {
		t.Fatalf("the file's name moved: %q, then %q", before[extra], after[extra])
	}
	if after[current] != "Docker Desktop data" || after[old] != "Docker Desktop data 2" {
		t.Fatalf("%v", after)
	}
}
