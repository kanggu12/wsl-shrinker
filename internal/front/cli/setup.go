package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kanggu12/wsl-shrinker/internal/app"
	"github.com/kanggu12/wsl-shrinker/internal/engine/disks"
	"github.com/kanggu12/wsl-shrinker/internal/front/i18n"
	"github.com/kanggu12/wsl-shrinker/internal/front/tui"
	"github.com/kanggu12/wsl-shrinker/internal/machine/winapi"
	"github.com/kanggu12/wsl-shrinker/internal/settings/config"
)

// setupFlags are the choices setup takes on the command line, the CLI way
// to the same settings the screen chooses.
type setupFlags struct {
	lang, theme, mode  string
	minGain            float64
	protect, unprotect []string
}

// setupCmd chooses how wsl-shrinker works: on a screen in a terminal, or
// from flags anywhere.
func setupCmd(p *apps) *cobra.Command {
	var f setupFlags
	var installer bool
	var snapshot, keys string
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Choose the language, colors, mode, what is worth shrinking, and the disks to protect",
		Long: `Open a wizard to choose how wsl-shrinker works, a page each: the language, the
colors, what it opens with (the screen, TUI, or the disks as a table with the
commands, CLI), the size a disk must give back to be worth shrinking, and the
disks to protect. Up and down choose, shown at once; Enter goes on, Left goes
back. Enter on the last page writes the choices into the settings file, leaving
the rest of it as it is; Esc changes nothing, or, with no settings file yet,
writes the defaults; Ctrl+C writes nothing. Nothing on the disks changes here.

With any of the flags below it opens no screen: the flags are the choices, what
they leave out stays as it is. That works anywhere, in a script too:

  wsl-shrinker setup --mode cli --min-gain 5 --protect Work
  wsl-shrinker setup --language ko --theme light

wsl-shrinker opens this screen by itself the first time it starts in a terminal.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// A frame of the screen on the made-up PC, for the README; --theme
			// light draws it for a light terminal.
			if snapshot != "" {
				w, h, err := size(snapshot)
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), tui.SetupSnapshot(demoSetup(), w, h, f.theme != "light", tui.ParseKeys(keys)))
				return nil
			}
			a, err := p.get()
			if err != nil {
				return err
			}
			// Choices given as flags: no screen, so no terminal needed.
			if setFlags(cmd) {
				return writeChoices(cmd, a, f)
			}
			if !stdinIsTerminal() || !stdoutIsTerminal() {
				return hint(fmt.Errorf("%w: setup needs a terminal for its screen", errUsage), "or give the choices as flags: wsl-shrinker setup --mode cli")
			}
			first := !exists(a.ConfigPath)
			// The installer opens it after every install: an upgrade keeps
			// the settings it has.
			if installer && !first {
				l := lookFor(cmd.OutOrStdout())
				fmt.Fprintln(cmd.OutOrStdout(), l.done("settings kept: "+a.ConfigPath))
				return nil
			}
			_, err = runSetup(cmd.Context(), cmd.OutOrStdout(), a, first || installer, cmd.Flag("lang").Value.String())
			return err
		},
	}
	cmd.Flags().StringVar(&f.lang, "language", "", "language of the screen: auto, en or ko")
	cmd.Flags().StringVar(&f.theme, "theme", "", "colors: auto, dark or light")
	cmd.Flags().StringVar(&f.mode, "mode", "", "what wsl-shrinker opens in a terminal: tui (the screen) or cli (the disks and the commands)")
	cmd.Flags().Float64Var(&f.minGain, "min-gain", 0, "GB a disk must give back to be worth shrinking")
	cmd.Flags().StringArrayVar(&f.protect, "protect", nil, "a disk to protect, by name; once per disk")
	cmd.Flags().StringArrayVar(&f.unprotect, "unprotect", nil, "a disk to protect no more, by name; once per disk")
	cmd.Flags().BoolVar(&installer, "installer", false, "opened by the installer")
	cmd.Flags().StringVar(&snapshot, "snapshot", "", "print one frame WIDTHxHEIGHT of the screen on the made-up PC")
	cmd.Flags().StringVar(&keys, "keys", "", "keys to press before --snapshot prints")
	for _, n := range []string{"installer", "snapshot", "keys"} {
		_ = cmd.Flags().MarkHidden(n)
	}
	return cmd
}

// setFlags reports whether any choice was given as a flag.
func setFlags(cmd *cobra.Command) bool {
	for _, n := range []string{"language", "theme", "mode", "min-gain", "protect", "unprotect"} {
		if cmd.Flags().Changed(n) {
			return true
		}
	}
	return false
}

// writeChoices writes the choices given as flags. A value no setting takes
// is a mistake in the command line; a file that cannot be written is not.
func writeChoices(cmd *cobra.Command, a *app.App, f setupFlags) error {
	// --lang, the language of this run, names the language to keep too,
	// unless --language does: "setup --lang ko --mode cli" keeps Korean.
	if l := cmd.Flag("lang").Value.String(); l != "" && !cmd.Flags().Changed("language") {
		f.lang = l
		_ = cmd.Flags().Set("language", l)
	}
	// Each value is checked first, in the flag's own words.
	for _, c := range []struct {
		flag, value string
		takes       []string
	}{{"language", f.lang, setupLanguages}, {"theme", f.theme, setupThemes}, {"mode", f.mode, setupModes}} {
		if cmd.Flags().Changed(c.flag) && !slices.Contains(c.takes, c.value) {
			return fmt.Errorf("%w: --%s takes %s", errUsage, c.flag, orList(c.takes))
		}
	}
	// A disk is named by its name: an empty one names none.
	for _, n := range append(slices.Clone(f.protect), f.unprotect...) {
		if strings.TrimSpace(n) == "" {
			return fmt.Errorf("%w: --protect and --unprotect take a disk's name, as wsl-shrinker list shows it", errUsage)
		}
	}
	// A number TOML cannot write, or none at all, is no size.
	if cmd.Flags().Changed("min-gain") && (math.IsNaN(f.minGain) || math.IsInf(f.minGain, 0) || f.minGain < 0) {
		return fmt.Errorf("%w: --min-gain takes a number of GB, 0 or more", errUsage)
	}
	values := map[string]string{}
	set := func(flag, key, v string) {
		if cmd.Flags().Changed(flag) {
			values[key] = config.Quote(v)
		}
	}
	set("language", "language", f.lang)
	set("theme", "theme", f.theme)
	set("mode", "mode", f.mode)
	if cmd.Flags().Changed("min-gain") {
		values["min_gain_gb"] = strconv.FormatFloat(f.minGain, 'f', -1, 64)
	}
	if cmd.Flags().Changed("protect") || cmd.Flags().Changed("unprotect") {
		values["protect"] = config.QuoteList(protectList(a.Config.Protect, f.protect, f.unprotect))
	}
	if err := config.SetKeys(a.ConfigPath, values); err != nil {
		// A value the settings refuse, as too large a size, is the command
		// line's mistake; anything else is the file's.
		var invalid *config.InvalidError
		if errors.As(err, &invalid) {
			return fmt.Errorf("%w: %w", errUsage, err)
		}
		return err
	}
	return saidSaved(cmd.OutOrStdout(), a.ConfigPath)
}

// orList joins values as English lists them: "tui or cli", "auto, en or ko".
func orList(vs []string) string {
	if len(vs) < 2 {
		return strings.Join(vs, "")
	}
	return strings.Join(vs[:len(vs)-1], ", ") + " or " + vs[len(vs)-1]
}

// The values setup's flags take, as the screen offers them.
var (
	setupLanguages = []string{"auto", "en", "ko"}
	setupThemes    = []string{"auto", "dark", "light"}
	setupModes     = []string{"tui", "cli"}
)

// protectList is now's protected disks, with add put on and drop taken off,
// by name in any case.
func protectList(now, add, drop []string) []string {
	out := []string{}
	has := func(list []string, name string) bool {
		return slices.ContainsFunc(list, func(p string) bool { return strings.EqualFold(p, name) })
	}
	for _, p := range append(slices.Clone(now), add...) {
		if !has(out, p) && !has(drop, p) {
			out = append(out, p)
		}
	}
	return out
}

// runSetup opens the screen and writes what was chosen. first is a first
// run or the installer: leaving with esc then keeps the defaults, written
// down, so the screen does not come back at every start. lang, from --lang,
// is the language the screen starts in, when given. It reports whether
// settings were written.
func runSetup(ctx context.Context, out io.Writer, a *app.App, first bool, lang string) (bool, error) {
	// The disks, to choose which to protect; none when WSL has none yet.
	ds, _ := disks.Find(ctx, a.DisksEnv())
	c := a.Config
	start := tui.SetupStart{Language: c.Language, Theme: c.Theme, Mode: c.Mode, MinGainGB: c.MinGainGB, Protect: c.Protect,
		Disks: ds, AutoLanguage: i18n.Choose("auto", winapi.Korean()), Path: a.ConfigPath, First: first}
	// The language asked for on the command line is the one chosen so far.
	if lang != "" {
		start.Language = lang
	}
	choice, saved, err := openSetup(ctx, start)
	if err != nil {
		// Ctrl+C says why itself; a bug says it is one.
		return false, screenBug(err, out)
	}
	if !saved && !first {
		fmt.Fprintln(out, "nothing changed")
		return false, nil
	}
	if !saved {
		// The defaults, as they stand.
		choice = tui.SetupChoice{Language: c.Language, Theme: c.Theme, Mode: c.Mode, MinGainGB: c.MinGainGB, Protect: c.Protect}
	}
	// Only what changed is written, so a file written by hand keeps the
	// rest as it was; without a file every key is, so it is there next time.
	none := !exists(a.ConfigPath)
	values := map[string]string{}
	put := func(key, value string, changed bool) {
		if changed || none {
			values[key] = value
		}
	}
	put("language", config.Quote(choice.Language), choice.Language != c.Language)
	put("theme", config.Quote(choice.Theme), choice.Theme != c.Theme)
	put("mode", config.Quote(choice.Mode), choice.Mode != c.Mode)
	put("min_gain_gb", strconv.FormatFloat(choice.MinGainGB, 'f', -1, 64), choice.MinGainGB != c.MinGainGB)
	protect := protectList(choice.Protect, nil, nil)
	put("protect", config.QuoteList(protect), !slices.Equal(protect, protectList(c.Protect, nil, nil)))
	if len(values) == 0 {
		fmt.Fprintln(out, "nothing changed")
		return false, nil
	}
	if err := config.SetKeys(a.ConfigPath, values); err != nil {
		return false, err
	}
	// What runs next in this process follows the choice.
	a.Config.Language, a.Config.Theme, a.Config.Mode = choice.Language, choice.Theme, choice.Mode
	a.Config.MinGainGB, a.Config.Protect = choice.MinGainGB, protectList(choice.Protect, nil, nil)
	return true, saidSaved(out, a.ConfigPath)
}

// saidSaved says where the settings went.
func saidSaved(out io.Writer, path string) error {
	l := lookFor(out)
	_, err := fmt.Fprintln(out, l.done("saved: "+path))
	return err
}

// exists reports whether a file is there.
func exists(path string) bool {
	_, err := os.Stat(path)
	return !errors.Is(err, fs.ErrNotExist)
}

// demoSetup is the setup screen on the made-up PC, for its pictures.
func demoSetup() tui.SetupStart {
	ds, _ := tui.NewDemo().Disks(context.Background())
	return tui.SetupStart{Language: "auto", Theme: "auto", Mode: "tui", MinGainGB: 1, Protect: []string{"Work"}, Disks: ds,
		AutoLanguage: i18n.Current(), Path: `C:\Users\ann\AppData\Roaming\wsl-shrinker\config.toml`, First: true}
}
