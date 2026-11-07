package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/kanggu12/wsl-shrinker/internal/app"
	"github.com/kanggu12/wsl-shrinker/internal/core/units"
	"github.com/kanggu12/wsl-shrinker/internal/engine/disks"
	"github.com/kanggu12/wsl-shrinker/internal/engine/shrink"
	"github.com/kanggu12/wsl-shrinker/internal/engine/survey"
	"github.com/kanggu12/wsl-shrinker/internal/machine/winapi"
	"github.com/kanggu12/wsl-shrinker/internal/settings/config"
)

// listCmd prints every disk with its estimate.
func listCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the WSL disks and what a shrink would give back",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := app.New()
			if err != nil {
				return err
			}
			return list(cmd.Context(), a, cmd.OutOrStdout(), asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

// list prints the disks as a table or as JSON.
func list(ctx context.Context, a *app.App, w io.Writer, asJSON bool) error {
	ds, err := disks.Find(ctx, a.DisksEnv())
	if err != nil {
		return err
	}
	if asJSON {
		return printJSON(w, ds)
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(tw, "DISK\tSTATE\tFILE\tAFTER\tFREES\t")
	for _, d := range ds {
		state := "stopped"
		switch {
		case d.Kind == disks.KindDocker && d.File.Held:
			state = "in use"
		case d.Kind != disks.KindDistro:
			state = "file"
		case d.Running:
			state = "running"
		}
		if d.Estimate == nil {
			fmt.Fprintf(tw, "%s\t%s\t-\t-\t-\t %s\n", d.Name, state, d.Err)
			continue
		}
		e := d.Estimate
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t\n", d.Name, state, units.Bytes(e.FileSize), units.Bytes(e.After), units.Bytes(e.Reclaim))
	}
	return tw.Flush()
}

// insideCmd prints what can go inside a running distribution.
func insideCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "inside DISTRO",
		Short: "Show what can be removed inside a running distribution",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := app.New()
			if err != nil {
				return err
			}
			r, err := survey.Run(cmd.Context(), a.WSL, args[0])
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(cmd.OutOrStdout(), r.Items)
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "SIZE\tKIND\tID\tWHERE")
			for _, it := range r.Items {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", units.Bytes(it.Bytes), it.Kind, it.ID, it.Label)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

// shrinkCmd shrinks disks named on the command line.
func shrinkCmd() *cobra.Command {
	var (
		yes    bool
		asJSON bool
		clean  []string
	)
	cmd := &cobra.Command{
		Use:   "shrink DISK...",
		Short: "Shrink disks by name, printing each step",
		Long: "Shrinks the named disks one after another. --clean NAME=ID,ID removes those items\n" +
			"inside NAME first; the ids are the ones `wsl-shrinker inside NAME` prints.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := app.New()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			out := cmd.OutOrStdout()
			ds, err := disks.Find(ctx, a.DisksEnv())
			if err != nil {
				return err
			}
			// Find every named disk first: a typo stops before anything runs.
			var queue []disks.Disk
			for _, name := range args {
				i := slices.IndexFunc(ds, func(d disks.Disk) bool { return strings.EqualFold(d.Name, name) })
				if i < 0 {
					return fmt.Errorf("%w: no disk named %q (see wsl-shrinker list)", errUsage, name)
				}
				if a.Config.Protected(ds[i].Name) {
					return fmt.Errorf("%s is protected in %s", ds[i].Name, a.ConfigPath)
				}
				queue = append(queue, ds[i])
			}
			chosen, err := cleanChoices(ctx, a, queue, clean)
			if err != nil {
				return err
			}
			if !yes {
				for _, d := range queue {
					gain := "?"
					if d.Estimate != nil {
						gain = units.Bytes(d.Estimate.Reclaim)
					}
					fmt.Fprintf(out, "%s: about %s back to Windows", d.Name, gain)
					if d.Running {
						fmt.Fprint(out, "; it stops and starts again")
					}
					fmt.Fprintln(out)
				}
				if !ask(cmd.InOrStdin(), out, "Shrink? [y/N] ") {
					return fmt.Errorf("not shrunk")
				}
			}
			// From inside WSL, the distribution this runs in may stop: go on
			// in a new window when any running distribution is in the queue.
			if slices.ContainsFunc(queue, func(d disks.Disk) bool { return d.Running && d.Distro != "" && app.Hosts(d.Distro) }) {
				// The run moves to the screen in a new window, and its result
				// stays there: a script waiting on this command could not read
				// it, so --json is refused rather than answered with nothing.
				if asJSON {
					return fmt.Errorf("%w: --json cannot follow a run into a new window; run it from PowerShell", errUsage)
				}
				reopen := []string{"--reopened"}
				if l := cmd.Flag("lang").Value.String(); l != "" {
					reopen = append(reopen, "--lang", l)
				}
				for _, d := range queue {
					reopen = append(reopen, "--run", d.Name)
				}
				// The disk names as found, with the items the check above
				// resolved, so the new window looks for exactly those.
				for name, items := range chosen {
					ids := make([]string, 0, len(items))
					for _, it := range items {
						ids = append(ids, it.ID)
					}
					reopen = append(reopen, "--clean", name+"="+strings.Join(ids, ","))
				}
				if err := winapi.Reopen(reopen); err != nil {
					return err
				}
				fmt.Fprintln(out, "Continuing in a new window: this terminal runs inside the distribution being shrunk.")
				fmt.Fprintln(out, "The result is in that window and in `wsl-shrinker history`.")
				return nil
			}
			var results []shrink.Result
			failed := false
			for _, d := range queue {
				if !asJSON {
					fmt.Fprintf(out, "%s\n", d.Name)
				}
				res := a.Shrink(ctx, d, shrink.Options{Clean: chosen[d.Name]}, func(e shrink.Event) {
					if asJSON || e.Status == shrink.Running {
						return
					}
					fmt.Fprintf(out, "  %-8s %-8s %s\n", e.Step, e.Status, e.Detail)
				})
				results = append(results, res)
				if res.Err != "" {
					failed = true
					break
				}
				if !asJSON {
					fmt.Fprintf(out, "  freed %s in %s\n", units.Bytes(res.Freed), res.Took.Round(time.Second))
				}
			}
			if asJSON {
				if err := printJSON(out, results); err != nil {
					return err
				}
			}
			if failed {
				return fmt.Errorf("%s: %s", results[len(results)-1].Disk, results[len(results)-1].Err)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the results as JSON")
	cmd.Flags().StringArrayVar(&clean, "clean", nil, "NAME=ID,ID: remove these items inside NAME first")
	return cmd
}

// cleanChoices resolves --clean NAME=ID,ID against a fresh look inside each
// distribution, so only commands built by this program run.
func cleanChoices(ctx context.Context, a *app.App, queue []disks.Disk, clean []string) (map[string][]survey.Item, error) {
	out := map[string][]survey.Item{}
	for _, c := range clean {
		name, ids, ok := strings.Cut(c, "=")
		if !ok || ids == "" {
			return nil, fmt.Errorf("%w: --clean takes NAME=ID,ID", errUsage)
		}
		i := slices.IndexFunc(queue, func(d disks.Disk) bool { return strings.EqualFold(d.Name, name) })
		if i < 0 {
			return nil, fmt.Errorf("%w: --clean names %q, which is not being shrunk", errUsage, name)
		}
		d := queue[i]
		if !d.Running || d.Distro == "" {
			return nil, fmt.Errorf("%s is not running: nothing inside it can be removed", d.Name)
		}
		r, err := survey.Run(ctx, a.WSL, d.Distro)
		if err != nil {
			return nil, err
		}
		for _, id := range strings.Split(ids, ",") {
			j := slices.IndexFunc(r.Items, func(it survey.Item) bool { return it.ID == id })
			if j < 0 {
				return nil, fmt.Errorf("%w: nothing with id %q inside %s now (see wsl-shrinker inside %s)", errUsage, id, d.Name, d.Name)
			}
			out[d.Name] = append(out[d.Name], r.Items[j])
		}
	}
	return out, nil
}

// ask reads a yes or no; anything but y or yes is no.
func ask(in io.Reader, out io.Writer, q string) bool {
	fmt.Fprint(out, q)
	line, _ := bufio.NewReader(in).ReadString('\n')
	a := strings.ToLower(strings.TrimSpace(line))
	return a == "y" || a == "yes"
}

// historyCmd prints past runs.
func historyCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "history",
		Short: "Show past runs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := app.New()
			if err != nil {
				return err
			}
			all, err := a.History.All()
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(cmd.OutOrStdout(), all)
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "WHEN\tDISK\tBEFORE\tAFTER\tFREED\tRESULT")
			for _, e := range all {
				res := "ok"
				if e.Err != "" {
					res = e.Err
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", e.At.Local().Format("2006-01-02 15:04"), e.Disk,
					units.Bytes(e.Before), units.Bytes(e.After), units.Bytes(e.Freed), res)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

// configCmd shows where the settings are, or writes the example file.
func configCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Show or create the settings file", Args: cobra.NoArgs}
	cmd.AddCommand(
		&cobra.Command{Use: "path", Short: "Print the settings file's path", Args: cobra.NoArgs,
			Run: func(cmd *cobra.Command, _ []string) {
				fmt.Fprintln(cmd.OutOrStdout(), config.Path(winapi.AppData()))
			}},
		&cobra.Command{Use: "init", Short: "Write a settings file with every key at its default", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				p := config.Path(winapi.AppData())
				if _, err := os.Stat(p); err == nil {
					return fmt.Errorf("%s exists already", p)
				}
				if err := os.MkdirAll(dirOf(p), 0o755); err != nil {
					return err
				}
				if err := os.WriteFile(p, []byte(config.Example), 0o644); err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), p)
				return nil
			}},
	)
	return cmd
}

// dirOf is the folder of a path with either separator.
func dirOf(p string) string {
	i := strings.LastIndexAny(p, `\/`)
	if i < 0 {
		return "."
	}
	return p[:i]
}
