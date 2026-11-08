package wsl

import (
	"context"
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
	// version is Version, 1 or 2.
	version int
}

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
		d := Distro{Name: e.name, ID: e.id, Version: e.version, Default: e.id == def}
		if d.Version == 2 {
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
	// The full list is asked for only to explain a failed running list.
	var allOut string
	var allErr error
	if runErr != nil {
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
