package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kanggu12/wsl-shrinker/internal/core/units"
	"github.com/kanggu12/wsl-shrinker/internal/engine/disks"
	"github.com/kanggu12/wsl-shrinker/internal/engine/journal"
	"github.com/kanggu12/wsl-shrinker/internal/engine/shrink"
	"github.com/kanggu12/wsl-shrinker/internal/machine/winapi"
	"github.com/kanggu12/wsl-shrinker/internal/machine/wsl"
	"github.com/kanggu12/wsl-shrinker/internal/settings/config"
)

// doctorCmd checks what a shrink needs, and says what to do about each gap.
func doctorCmd(made *apps) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check what a shrink needs on this PC",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			l := lookFor(out)
			bad := 0
			// On a terminal a check reads "✓ what" or "✗ what" with "→ fix"
			// under it, a note "! what"; piped, the words a script greps:
			// ok, FAIL and note.
			line := func(ok bool, what, fix string) {
				if !ok {
					bad++
				}
				switch {
				case l.on && ok:
					fmt.Fprintf(out, "%s %s\n", l.gain("✓"), what)
				case l.on:
					fmt.Fprintf(out, "%s %s\n", l.bad("✗"), what)
					if fix != "" {
						fmt.Fprintf(out, "  %s\n", l.muted("→ "+fix))
					}
				case ok:
					fmt.Fprintf(out, "ok    %s\n", what)
				default:
					fmt.Fprintf(out, "FAIL  %s\n", what)
					if fix != "" {
						fmt.Fprintf(out, "      %s\n", fix)
					}
				}
			}
			note := func(what string) {
				if l.on {
					fmt.Fprintf(out, "%s %s\n", l.warn("!"), what)
					return
				}
				fmt.Fprintf(out, "note  %s\n", what)
			}
			// The settings file.
			p := config.Path(winapi.AppData())
			if _, err := config.Load(p); err != nil {
				line(false, "settings: "+err.Error(), settingsHint(err))
				return problems(bad)
			}
			line(true, settingsLine(p), "")
			a, err := made.get()
			if err != nil {
				return err
			}
			// A run cut off is the first thing to fix; one going on is news.
			switch e, st := a.Pending(); st {
			case journal.CutOff:
				what, fix := journal.Words(e, st)
				line(false, what, fix)
			case journal.Going:
				what, _ := journal.Words(e, st)
				note(what)
			}
			ctx := cmd.Context()
			info := a.Info(ctx)
			// The disks are read before WSL's line is printed: a PC with
			// distributions has WSL already, and the fix for it differs.
			ds, findErr := disks.Find(ctx, a.DisksEnv())
			switch {
			case info.NoAnswer:
				// Not an old WSL: one that did not answer in time.
				line(false, "WSL did not answer wsl --version in time", noAnswerFix)
			case info.Version == "":
				line(false, "WSL does not answer wsl --version", wslFix(findErr == nil && len(ds) > 0))
			case !info.CanCompact:
				line(false, "WSL "+info.Version+" cannot compact (wsl --manage --compact)", "update WSL: wsl --update")
			default:
				line(true, "WSL "+info.Version+" can compact", "")
			}
			if winapi.FromWSL() {
				if here := os.Getenv("WSL_DISTRO_NAME"); here != "" {
					note("this terminal runs inside " + here + ": shrinking it continues in a new window")
				} else {
					note("this terminal runs inside WSL: shrinking a running distribution continues in a new window")
				}
			}
			if findErr != nil {
				fix := "wsl --status says why"
				if errors.Is(findErr, wsl.ErrNoAnswer) {
					fix = noAnswerFix
				}
				line(false, "could not list the distributions: "+findErr.Error(), fix)
				return problems(bad)
			}
			if len(ds) == 0 {
				note("no WSL 2 disk found")
			}
			for _, d := range ds {
				doctorDisk(d, info.CanCompact, a.Config.Protected(d.Name), line, note)
			}
			// A protected name no disk has protects nothing now: a typo, or
			// a disk gone or listed under another name since.
			for _, p := range a.Config.Protect {
				if !slices.ContainsFunc(ds, func(d disks.Disk) bool { return strings.EqualFold(d.Name, p) }) {
					note(fmt.Sprintf("protect names %q, which no disk is called now; wsl-shrinker list shows the names", p))
				}
			}
			return problems(bad)
		},
	}
}

// settingsLine says which settings are in use: the file, or the defaults
// when there is none yet.
func settingsLine(p string) string {
	if _, err := os.Stat(p); errors.Is(err, fs.ErrNotExist) {
		return "settings: defaults; wsl-shrinker config init writes them to " + p
	}
	return "settings: " + p
}

// wslFix is what to do when wsl --version gives nothing. With distributions
// on the PC this is the old WSL built into Windows, which the Store version
// replaces; without any, WSL is not there yet.
func wslFix(hasDistros bool) string {
	if hasDistros {
		return "update WSL to the Microsoft Store version: wsl --update"
	}
	return "install WSL: wsl --install"
}

// problems is doctor's error for n failed checks, nil for none.
func problems(n int) error {
	switch n {
	case 0:
		return nil
	case 1:
		return errors.New("1 problem")
	}
	return fmt.Errorf("%d problems", n)
}

// doctorDisk checks one disk: every reason the engine's rule (Preflight)
// gives for not shrinking it now, as a problem or a note, then what is
// worth knowing.
func doctorDisk(d disks.Disk, canCompact, protected bool, line func(bool, string, string), note func(string)) {
	blocks := shrink.Preflight(d, canCompact, protected)
	if d.Estimate != nil {
		e := d.Estimate
		line(true, fmt.Sprintf("%s: %s, about %s back", d.Name, units.Bytes(e.FileSize), units.Bytes(e.Reclaim)), "")
	}
	for _, b := range blocks {
		switch b {
		case shrink.BlockUnreadable:
			line(false, d.Name+": read "+d.Path+": "+d.Err, "")
		case shrink.BlockEncrypted:
			line(false, d.Name+": the file is encrypted (EFS)", "Windows cannot compact an encrypted disk; decrypt it in its Properties")
		case shrink.BlockFSErrors:
			line(false, d.Name+": the filesystem records errors", "check it with e2fsck first: see Troubleshooting in the docs")
		case shrink.BlockFixed:
			// How the disk was made, not something missing on this PC.
			note(d.Name + ": a fixed-size disk; its file keeps its full size, and Windows compacts only disks that grow")
		case shrink.BlockProtected:
			// A choice made in the settings, not a problem.
			note(d.Name + ": protected in the settings; wsl-shrinker setup --unprotect " + d.Name + " shrinks it again")
		case shrink.BlockHeld:
			// Waiting on a program, not a problem.
			switch shrink.OwnerOf(d) {
			case shrink.OwnerDocker:
				note(d.Name + ": in use; quit Docker Desktop to shrink it")
			case shrink.OwnerRancher:
				note(d.Name + ": in use; quit Rancher Desktop to shrink it")
			case shrink.OwnerPodman:
				note(d.Name + ": in use; stop the Podman machine (podman machine stop) to shrink it")
			default:
				note(d.Name + ": in use by another program; close it to shrink the disk")
			}
		}
		// BlockNoCompact is WSL's, said once above for every distribution.
	}
	if d.Estimate == nil {
		return
	}
	e := d.Estimate
	if d.File.Sparse {
		note(d.Name + ": sparse file; it gives space back on its own")
	}
	if e.Reclaim > 0 && d.File.DriveFree < 1<<30 {
		note(d.Name + ": under 1 GB free on its drive")
	}
}
