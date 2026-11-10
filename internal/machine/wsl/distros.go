package wsl

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
)

// regEntry is one subkey of the WSL registry key, as read: the raw values
// the list of distributions is built from. It is kept apart from the
// registry calls so how the list is built can be tested on any system.
type regEntry struct {
	// id is the subkey's name, a GUID.
	id string
	// name is DistributionName; empty for leftovers of failed installs.
	name string
	// base is BasePath, the folder of the distribution's disk.
	base string
	// file is VhdFileName; empty means the default ext4.vhdx.
	file string
	// flags is Flags. Its VM mode bit says WSL 2; the Version value beside
	// it is the version of WSL 1's file system, which a WSL 1 distribution
	// made today holds as 2 too, so it says nothing about WSL 1 or 2.
	flags uint32
	// flagsUnread is set when Flags is there but could not be read as a
	// number; version, the Version value, is then all there is to go by.
	flagsUnread bool
	version     uint32
}

// vmMode is the bit of Flags that makes a distribution WSL 2, as WSL itself
// reads it (LXSS_DISTRO_FLAGS_VM_MODE). Without a Flags value WSL takes its
// default flags, which do not have it.
const vmMode = 0x8

// wslRunner runs wsl.exe with args and returns its decoded output; the
// real one is wslExe, tests pass recorded output.
type wslRunner func(ctx context.Context, args ...string) (string, error)

// buildDistros turns registry entries into distributions and asks wsl.exe,
// through run, which of them run. def is the DefaultDistribution value.
func buildDistros(ctx context.Context, entries []regEntry, def string, run wslRunner) ([]Distro, error) {
	var out []Distro
	for _, e := range entries {
		// Entries without a name are leftovers of failed installs.
		if e.name == "" {
			continue
		}
		d := Distro{Name: e.name, ID: e.id, Version: 1, Default: e.id == def}
		// Flags that cannot be read leave Version: a WSL 2 distribution
		// is listed, and a WSL 1 one taken for it shows as a disk file
		// that is not there, rather than a disk going missing.
		if e.flags&vmMode != 0 || (e.flagsUnread && e.version == 2) {
			d.Version = 2
			// The file name is ext4.vhdx unless the registration says otherwise.
			file := e.file
			if file == "" {
				file = "ext4.vhdx"
			}
			// Store installs record the base path with a \\?\ prefix.
			d.VHD = filepath.Join(strings.TrimPrefix(e.base, `\\?\`), file)
		}
		out = append(out, d)
	}
	// With nothing registered there is nothing to ask about, and both lists
	// below would fail, which would read as WSL not answering.
	if len(out) == 0 {
		return nil, nil
	}
	// Without running distributions wsl.exe exits non-zero: runningNames
	// tells that apart from WSL not answering, which is an error.
	runOut, runErr := run(ctx, "--list", "--running", "--quiet")
	// The full list is asked for only to explain a failed running list,
	// and not after WSL did not answer at all: a second wait for the same
	// silence would only double it.
	var allOut string
	var allErr error
	if runErr != nil && !errors.Is(runErr, ErrNoAnswer) && ctx.Err() == nil {
		allOut, allErr = run(ctx, "--list", "--quiet")
	}
	running, err := runningNames(runOut, runErr, allOut, allErr)
	if err != nil {
		return nil, err
	}
	for i := range out {
		// wsl.exe's names and the registry's may differ in case only.
		out[i].Running = running[strings.ToLower(out[i].Name)]
	}
	return out, nil
}
