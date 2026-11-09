// Package disks finds the WSL disk files on this PC and works out, for each,
// how much a trim and a compaction would give back. It only reads.
package disks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/kanggu12/wsl-shrinker/internal/engine/estimate"
	"github.com/kanggu12/wsl-shrinker/internal/machine/ext4"
	"github.com/kanggu12/wsl-shrinker/internal/machine/vhdx"
	"github.com/kanggu12/wsl-shrinker/internal/machine/winapi"
	"github.com/kanggu12/wsl-shrinker/internal/machine/wsl"
)

// Kind says what a disk belongs to.
type Kind string

const (
	// KindDistro is a registered WSL 2 distribution's disk.
	KindDistro Kind = "distro"
	// KindDocker is Docker Desktop's data disk, which WSL does not register.
	KindDocker Kind = "docker"
	// KindFile is another VHDX file the settings list.
	KindFile Kind = "file"
)

// Disk is one disk file and what is known about it.
type Disk struct {
	// Name is what the screen calls it: the distribution's name, or the file's.
	Name string `json:"name"`
	// Kind says what it belongs to.
	Kind Kind `json:"kind"`
	// Distro is the registered distribution, empty for other files.
	Distro string `json:"distro,omitempty"`
	// Path is the disk file.
	Path string `json:"path"`
	// Default and Running describe the distribution.
	Default bool `json:"default,omitempty"`
	Running bool `json:"running,omitempty"`
	// File is what Windows says about the file.
	File winapi.FileInfo `json:"file"`
	// Estimate is the outcome of reading it; nil with Err set when it could
	// not be read.
	Estimate *estimate.Result `json:"estimate,omitempty"`
	Err      string           `json:"error,omitempty"`
}

// Env is what discovery reads from: WSL, the files, and where Docker Desktop
// keeps its disks.
type Env struct {
	WSL wsl.System
	// Stat and Open reach the files; tests give fakes.
	Stat func(string) (winapi.FileInfo, error)
	Open func(string) (ReaderAtCloser, int64, error)
	// LocalAppData is the per-user folder Docker Desktop's disks are under.
	LocalAppData string
	// AppData is the per-user folder of settings, where Docker Desktop
	// says where its disk was moved to.
	AppData string
	// Extra lists more VHDX files to look at, from the settings.
	Extra []string
}

// ReaderAtCloser is an open disk file.
type ReaderAtCloser interface {
	io.ReaderAt
	io.Closer
}

// OpenFile opens a file for reading, next to the process that holds it.
func OpenFile(path string) (ReaderAtCloser, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, st.Size(), nil
}

// dockerDisks are where Docker Desktop keeps disks WSL does not register,
// relative to %LOCALAPPDATA%: the data disk of current releases, and the
// one older releases used.
var dockerDisks = []string{`Docker\wsl\disk\docker_data.vhdx`, `Docker\wsl\data\ext4.vhdx`}

// movedDockerDisk is where Docker Desktop's data disk is when its settings
// moved it (Resources, Advanced, "Disk image location"): the folder chosen
// there, CustomWslDistroDir in %APPDATA%\Docker\settings-store.json, holds
// it as disk\docker_data.vhdx. "" when the disk was not moved, or the file
// does not say.
func movedDockerDisk(appData string) string {
	if appData == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(appData, "Docker", "settings-store.json"))
	if err != nil {
		return ""
	}
	// Some Docker Desktop releases wrote the file with a UTF-8 byte order
	// mark, which the JSON reader does not take.
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	var s struct {
		CustomWslDistroDir string
	}
	// A folder that is not a whole path would be looked for wherever this
	// program runs, and could find a file that is not Docker's there.
	if json.Unmarshal(b, &s) != nil || !filepath.IsAbs(s.CustomWslDistroDir) {
		return ""
	}
	return filepath.Join(s.CustomWslDistroDir, "disk", "docker_data.vhdx")
}

// Find lists every disk with its estimate, the default distribution first,
// then by how much each gives back.
func Find(ctx context.Context, env Env) ([]Disk, error) {
	distros, err := env.WSL.Distros(ctx)
	if err != nil {
		return nil, err
	}
	var out []Disk
	seen := map[string]bool{}
	for _, d := range distros {
		// WSL 1 distributions keep their files in the Windows filesystem.
		if d.Version != 2 || d.VHD == "" {
			continue
		}
		out = append(out, Disk{Name: d.Name, Kind: KindDistro, Distro: d.Name, Path: d.VHD, Default: d.Default, Running: d.Running})
		seen[strings.ToLower(d.VHD)] = true
	}
	// Docker Desktop's data disks that exist and WSL does not register:
	// where its settings moved the disk, then where it keeps disks by
	// default. The first found is "Docker Desktop data"; another one, as a
	// disk an older release left behind, is "Docker Desktop data 2". Their
	// names are Docker Desktop's own, never a file's: a file from the
	// settings keeps its name whatever Docker Desktop leaves behind, and a
	// disk protected by name stays the one protected.
	var docker []string
	if p := movedDockerDisk(env.AppData); p != "" {
		docker = append(docker, p)
	}
	if env.LocalAppData != "" {
		for _, rel := range dockerDisks {
			docker = append(docker, filepath.Join(env.LocalAppData, rel))
		}
	}
	for _, p := range docker {
		if seen[strings.ToLower(p)] {
			continue
		}
		if _, err := env.Stat(p); err == nil {
			name := "Docker Desktop data"
			if n := 1 + len(slices.DeleteFunc(slices.Clone(out), func(d Disk) bool { return d.Kind != KindDocker })); n > 1 {
				name = fmt.Sprintf("%s %d", name, n)
			}
			out = append(out, Disk{Name: name, Kind: KindDocker, Path: p})
			seen[strings.ToLower(p)] = true
		}
	}
	for _, p := range env.Extra {
		// The same file spelt two ways ("a\.\x.vhdx", "a\x.vhdx") is one.
		p = filepath.Clean(p)
		if !seen[strings.ToLower(p)] {
			out = append(out, Disk{Name: uniqueName(out, p), Kind: KindFile, Path: p})
			seen[strings.ToLower(p)] = true
		}
	}
	// Read every disk at once: each read is a few dozen MiB of metadata.
	var wg sync.WaitGroup
	for i := range out {
		wg.Go(func() { inspect(env, &out[i]) })
	}
	wg.Wait()
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Default != b.Default {
			return a.Default
		}
		return reclaim(a) > reclaim(b)
	})
	return out, nil
}

// uniqueName is the name an extra file is listed by: its file name, or with
// its folder in front when another disk has that name already (other tools
// call their disks ext4.vhdx too), and a number after when even that is
// taken. Names pick disks for marking, protecting and the command line, so
// two disks never share one; WSL's names ignore case, so these do too. The
// folder is joined with "/", not "\": the name goes into the settings'
// protect list, where a backslash in a TOML string starts an escape.
func uniqueName(out []Disk, p string) string {
	taken := func(n string) bool {
		for _, d := range out {
			if strings.EqualFold(d.Name, n) {
				return true
			}
		}
		return false
	}
	name := filepath.Base(p)
	if !taken(name) {
		return name
	}
	name = filepath.Base(filepath.Dir(p)) + "/" + name
	for i, try := 2, name; ; i++ {
		if !taken(try) {
			return try
		}
		try = fmt.Sprintf("%s (%d)", name, i)
	}
}

// Inspect reads one disk again, after a change.
func Inspect(env Env, d *Disk) { inspect(env, d) }

// inspect fills in a disk's file facts and estimate.
func inspect(env Env, d *Disk) {
	d.Estimate, d.Err = nil, ""
	fi, err := env.Stat(d.Path)
	if err != nil {
		d.Err = err.Error()
		return
	}
	d.File = fi
	f, size, err := env.Open(d.Path)
	if err != nil {
		d.Err = err.Error()
		return
	}
	defer f.Close()
	vd, err := vhdx.Read(f, size)
	if err != nil {
		d.Err = err.Error()
		return
	}
	// A disk without ext4 still gets an estimate, from its blocks alone.
	fs, _ := ext4.Read(vd.Contents(), vd.VirtualSize)
	// A distribution's disk is compacted by WSL, which trims first; any
	// other file by Windows alone, which does not.
	var r estimate.Result
	if d.Kind == KindDistro {
		r = estimate.Of(vd, fs, fi.OnDisk)
	} else {
		r = estimate.OfUntrimmed(vd, fs, fi.OnDisk)
	}
	d.Estimate = &r
}

// reclaim is what a disk gives back, zero when unknown.
func reclaim(d Disk) int64 {
	if d.Estimate == nil {
		return 0
	}
	return d.Estimate.Reclaim
}
