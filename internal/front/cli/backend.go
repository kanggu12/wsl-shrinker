package cli

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/kanggu12/wsl-shrinker/internal/app"
	"github.com/kanggu12/wsl-shrinker/internal/engine/disks"
	"github.com/kanggu12/wsl-shrinker/internal/engine/journal"
	"github.com/kanggu12/wsl-shrinker/internal/engine/shrink"
	"github.com/kanggu12/wsl-shrinker/internal/engine/survey"
	"github.com/kanggu12/wsl-shrinker/internal/front/i18n"
	"github.com/kanggu12/wsl-shrinker/internal/machine/winapi"
	"github.com/kanggu12/wsl-shrinker/internal/version"
)

// backendCmd answers the screen when it runs on the Linux side of WSL: one
// question a call, the answer as JSON on stdout. The disks, a look inside
// and the history come from list, inside and history with --json; these
// are the rest. Hidden: it is the way two builds shipped together talk, not
// a command for people.
func backendCmd(p *apps) *cobra.Command {
	cmd := &cobra.Command{Use: "backend", Hidden: true, Args: noArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return fmt.Errorf("%w: backend takes hello, info, settings, pending, crashes, shrink or reopen", errUsage)
	}}
	// hello names the build, so the launcher talks only to its own.
	cmd.AddCommand(&cobra.Command{Use: "hello", Args: noArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return printJSON(cmd.OutOrStdout(), struct {
			Schema  int    `json:"schema"`
			Version string `json:"version"`
		}{schema, version.String()})
	}})
	// info is WSL's version, and whether it can compact.
	cmd.AddCommand(&cobra.Command{Use: "info", Args: noArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		a, err := p.get()
		if err != nil {
			return err
		}
		info := a.Info(cmd.Context())
		return printJSON(cmd.OutOrStdout(), struct {
			Schema     int    `json:"schema"`
			Version    string `json:"version"`
			CanCompact bool   `json:"can_compact"`
		}{schema, info.Version, info.CanCompact})
	}})
	// settings are what the screen needs from config.toml, with the language
	// already chosen the Windows way (the setting, WSL_SHRINKER_LANG, then
	// Windows' display language), the mode, and whether there is no
	// settings file yet, for setup to come first.
	cmd.AddCommand(&cobra.Command{Use: "settings", Args: noArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		a, err := p.get()
		if err != nil {
			return err
		}
		c := a.Config
		protect := c.Protect
		if protect == nil {
			protect = []string{}
		}
		return printJSON(cmd.OutOrStdout(), struct {
			Schema       int      `json:"schema"`
			Language     string   `json:"language"`
			Theme        string   `json:"theme"`
			MinGainBytes int64    `json:"min_gain_bytes"`
			Protect      []string `json:"protect"`
			Mode         string   `json:"mode"`
			First        bool     `json:"first"`
		}{schema, string(i18n.Choose(c.Language, winapi.Korean())), c.Theme, int64(c.MinGainGB * (1 << 30)), protect,
			c.Mode, a.ConfigPath != "" && !exists(a.ConfigPath)})
	}})
	// crashes takes the crashes this program kept and did not say yet, for
	// the launcher's screen to say: this side's stderr reaches no one.
	cmd.AddCommand(&cobra.Command{Use: "crashes", Args: noArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		shown := ClaimCrashes()
		// None is an empty list, never null.
		if shown == nil {
			shown = []string{}
		}
		return printJSON(cmd.OutOrStdout(), struct {
			Schema int      `json:"schema"`
			Shown  []string `json:"shown"`
		}{schema, shown})
	}})
	// pending is a run that has not ended.
	cmd.AddCommand(&cobra.Command{Use: "pending", Args: noArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		a, err := p.get()
		if err != nil {
			return err
		}
		e, st := a.Pending()
		return printJSON(cmd.OutOrStdout(), struct {
			Schema int           `json:"schema"`
			State  string        `json:"state"`
			Run    journal.Entry `json:"run"`
		}{schema, stateWord(st), e})
	}})
	cmd.AddCommand(backendShrinkCmd(p))
	// reopen continues a run in a new window, from the arguments after --,
	// and returns once that window said it started.
	cmd.AddCommand(&cobra.Command{Use: "reopen -- ARGS...", RunE: func(cmd *cobra.Command, args []string) error {
		return app.Reopen(args)
	}})
	return cmd
}

// stateWord is a journal state in the backend's JSON.
func stateWord(st journal.State) string {
	switch st {
	case journal.Going:
		return "going"
	case journal.CutOff:
		return "cutoff"
	}
	return "none"
}

// backendShrinkCmd runs one disk for the screen, its events one JSON line
// each as they happen, then the result: {"event":...} lines, one {"result":...}.
// The items to clean come one --clean NAME=ID each, as a handoff passes
// them, and are found in a fresh look inside, so only commands this program
// builds run.
func backendShrinkCmd(p *apps) *cobra.Command {
	var name string
	var clean []string
	cmd := &cobra.Command{Use: "shrink", Args: noArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		a, err := p.get()
		if err != nil {
			return err
		}
		ctx := cmd.Context()
		ds, err := disks.Find(ctx, a.DisksEnv())
		if err != nil {
			return err
		}
		i := slices.IndexFunc(ds, func(d disks.Disk) bool { return strings.EqualFold(d.Name, name) })
		if i < 0 {
			return fmt.Errorf("%w: no disk named %q", errUsage, name)
		}
		d := ds[i]
		var items []survey.Item
		if len(clean) > 0 {
			ids, err := parseScreenClean(clean)
			if err != nil {
				return err
			}
			// Every item names the disk being shrunk, in any case: a name of
			// another is a mistake, said rather than dropped.
			for n := range ids {
				if !strings.EqualFold(n, d.Name) {
					return fmt.Errorf("%w: --clean names %q, which is not being shrunk", errUsage, n)
				}
			}
			if d.Distro == "" || !d.Running {
				return fmt.Errorf("%s is not running: nothing inside it can be removed", d.Name)
			}
			r, err := survey.Run(ctx, a.WSL, d.Distro)
			if err != nil {
				return err
			}
			var chosen []string
			for n, list := range ids {
				if strings.EqualFold(n, d.Name) {
					chosen = append(chosen, list...)
				}
			}
			for _, id := range chosen {
				j := slices.IndexFunc(r.Items, func(it survey.Item) bool { return it.ID == id })
				if j < 0 {
					return fmt.Errorf("%w: nothing with id %q inside %s now", errUsage, id, d.Name)
				}
				items = append(items, r.Items[j])
			}
		}
		out := cmd.OutOrStdout()
		// One line an event, as it happens: the screen draws the steps
		// while they run. Each line is one write to a stdout that holds
		// nothing back.
		var mu sync.Mutex
		line := func(v any) {
			mu.Lock()
			defer mu.Unlock()
			_ = json.NewEncoder(out).Encode(v)
		}
		res := a.Shrink(ctx, d, shrink.Options{Clean: items}, func(e shrink.Event) {
			line(struct {
				Event shrink.Event `json:"event"`
			}{e})
		})
		line(struct {
			Result shrink.Result `json:"result"`
		}{res})
		return nil
	}}
	cmd.Flags().StringVar(&name, "disk", "", "the disk to shrink, by name")
	cmd.Flags().StringArrayVar(&clean, "clean", nil, "NAME=ID: an item to remove inside NAME first; once per item")
	return cmd
}
