package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/kanggu12/wsl-shrinker/internal/app"
	"github.com/kanggu12/wsl-shrinker/internal/core/units"
	"github.com/kanggu12/wsl-shrinker/internal/engine/disks"
	"github.com/kanggu12/wsl-shrinker/internal/machine/winapi"
	"github.com/kanggu12/wsl-shrinker/internal/settings/config"
)

// doctorCmd checks what a shrink needs, and says what to do about each gap.
func doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check what a shrink needs on this PC",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			bad := 0
			line := func(ok bool, what, fix string) {
				mark := "ok  "
				if !ok {
					mark, bad = "FAIL", bad+1
				}
				fmt.Fprintf(out, "%s  %s\n", mark, what)
				if !ok && fix != "" {
					fmt.Fprintf(out, "      %s\n", fix)
				}
			}
			note := func(what string) { fmt.Fprintf(out, "note  %s\n", what) }
			// The settings file.
			p := config.Path(winapi.AppData())
			if _, err := config.Load(p); err != nil {
				line(false, "settings: "+err.Error(), "fix the file or move it away")
				return fmt.Errorf("%d problems", bad)
			}
			line(true, "settings: "+p, "")
			a, err := app.New()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			info := a.Info(ctx)
			switch {
			case info.Version == "":
				line(false, "WSL answers wsl --version", "install WSL from the Microsoft Store: wsl --install")
			case !info.CanCompact:
				line(false, "WSL "+info.Version+" can compact (wsl --manage --compact)", "update WSL: wsl --update")
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
			ds, err := disks.Find(ctx, a.DisksEnv())
			if err != nil {
				line(false, "list the distributions: "+err.Error(), "")
				return fmt.Errorf("%d problems", bad)
			}
			if len(ds) == 0 {
				note("no WSL 2 disk found")
			}
			for _, d := range ds {
				doctorDisk(out, d, line, note)
			}
			if bad > 0 {
				return fmt.Errorf("%d problems", bad)
			}
			return nil
		},
	}
}

// doctorDisk checks one disk.
func doctorDisk(_ io.Writer, d disks.Disk, line func(bool, string, string), note func(string)) {
	if d.Estimate == nil {
		line(false, d.Name+": read "+d.Path+": "+d.Err, "")
		return
	}
	e := d.Estimate
	line(true, fmt.Sprintf("%s: %s, about %s back", d.Name, units.Bytes(e.FileSize), units.Bytes(e.Reclaim)), "")
	if d.File.Encrypted {
		line(false, d.Name+": the file is encrypted (EFS)", "Windows cannot compact an encrypted disk; decrypt it in its Properties")
	}
	if d.Kind == disks.KindDocker && d.File.Held {
		note(d.Name + ": in use; quit Docker Desktop to shrink it")
	}
	if d.File.Sparse {
		note(d.Name + ": sparse file; it gives space back on its own")
	}
	if e.Reclaim > 0 && d.File.DriveFree < 1<<30 {
		note(d.Name + ": under 1 GB free on its drive")
	}
}
