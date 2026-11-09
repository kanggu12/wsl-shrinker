// Package cli is wsl-shrinker's command line: the screen when started bare,
// and commands for scripts and for a look without the screen.
package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/kanggu12/wsl-shrinker/internal/app"
	"github.com/kanggu12/wsl-shrinker/internal/front/i18n"
	"github.com/kanggu12/wsl-shrinker/internal/front/tui"
	"github.com/kanggu12/wsl-shrinker/internal/machine/winapi"
	"github.com/kanggu12/wsl-shrinker/internal/machine/wsl"
	"github.com/kanggu12/wsl-shrinker/internal/version"
)

// Exit codes: 0 done, 1 failed, 2 wrong usage, 3 failed and a distribution
// left not running.
const (
	exitOK        = 0
	exitFail      = 1
	exitUsage     = 2
	exitAttention = 3
)

// attention is a failure that left a distribution not running: it did not
// start again, or it was left stopped because its disk does not read as
// before. A script must not go on as after a plain failure.
type attention struct{ error }

// Unwrap gives the failure itself.
func (a attention) Unwrap() error { return a.error }

// apps makes the program's App once per command line, on first use: the
// settings are read once, and tests hand in an App of their own.
type apps struct {
	make func() (*app.App, error)
	once sync.Once
	a    *app.App
	err  error
}

// get returns the App, made on the first call. Making it fails only on a
// settings file that does not load, which says what to do about it.
func (p *apps) get() (*app.App, error) {
	p.once.Do(func() {
		p.a, p.err = p.make()
		if p.err != nil {
			p.err = hint(p.err, "fix the file, or move it away to start from the defaults")
		}
	})
	return p.a, p.err
}

// errUsage marks an error in how the command was called.
var errUsage = errors.New("usage")

// Execute runs the command line and returns the exit code. The console's
// modes, as colors changed them, are put back by the caller, after its
// last words (winapi.RestoreConsole).
func Execute() int { return run(os.Args[1:], os.Stdout, os.Stderr) }

// run runs one command line on this PC, writing to out and errOut.
func run(args []string, out, errOut io.Writer) int { return runWith(args, out, errOut, app.New) }

// runWith runs one command line with the App newApp makes.
func runWith(args []string, out, errOut io.Writer, newApp func() (*app.App, error)) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	root := newRoot(&apps{make: newApp})
	root.SetContext(ctx)
	root.SetArgs(args)
	root.SetOut(out)
	root.SetErr(errOut)
	err := root.Execute()
	if err != nil {
		printErr(errOut, withHint(usageHint(root, args, err)))
	}
	return exitFor(err)
}

// withHint adds what to do to an error that comes without, from below the
// command line.
func withHint(err error) error {
	var h hinted
	if errors.As(err, &h) {
		return err
	}
	if errors.Is(err, wsl.ErrNotWindows) {
		return hint(err, "run it on Windows, or inside WSL with wsl-shrinker.exe installed on Windows")
	}
	return err
}

// usageHint adds what to do to cobra's own errors about how a command was
// called: the command meant, for a word that is none, or the command's help.
// Cobra's own suggestion lines are dropped: the hint says it in one.
func usageHint(root *cobra.Command, args []string, err error) error {
	var h hinted
	if errors.As(err, &h) || !(cobraUsage(err) || errors.Is(err, errUsage)) {
		return err
	}
	first, _, _ := strings.Cut(err.Error(), "\n")
	c, rest, ferr := root.Find(args)
	if ferr != nil || c == nil {
		c = root
	}
	if word, ok := strings.CutPrefix(first, "unknown command "); ok {
		// The word cobra did not know, as it quoted it.
		w, _, _ := strings.Cut(strings.Trim(word, `"`), `"`)
		if len(rest) > 0 && w == "" {
			w = rest[0]
		}
		// Cobra sets the distance only on its own path; two letters off
		// is its default too.
		c.SuggestionsMinimumDistance = 2
		if sug := c.SuggestionsFor(w); len(sug) > 0 {
			return hint(fmt.Errorf("%w: %s", errUsage, first), "did you mean: "+c.CommandPath()+" "+sug[0])
		}
		return hint(fmt.Errorf("%w: %s", errUsage, first), c.CommandPath()+" --help lists the commands")
	}
	return hint(fmt.Errorf("%w: %s", errUsage, trimUsage(first)), c.CommandPath()+" --help")
}

// trimUsage drops the "usage: " that marks a mistake in how a command was
// called: the exit code says it, the words need not.
func trimUsage(s string) string { return strings.TrimPrefix(s, "usage: ") }

// exitFor is the exit code for how a command ended: 2 for a mistake in how it
// was called, a distribution name WSL does not have included, 1 for a
// failure, 0 for none.
func exitFor(err error) int {
	var at attention
	switch {
	case err == nil:
		return exitOK
	case errors.As(err, &at):
		return exitAttention
	case errors.Is(err, errUsage) || cobraUsage(err) || errors.Is(err, wsl.ErrNoDistro):
		return exitUsage
	}
	return exitFail
}

// newRoot builds every command; p makes their App.
func newRoot(p *apps) *cobra.Command {
	var (
		demo     bool
		snapshot string
		keys     string
		stopAt   string
		theme    string
		lang     string
		reopened bool
		cliMode  bool
		handoff  string
		run      []string
		clean    []string
	)
	root := &cobra.Command{
		Use:   "wsl-shrinker",
		Short: "See where your WSL disk space went, clean it, shrink it, and check it worked",
		Long: "wsl-shrinker shows every WSL 2 disk with what a shrink would give back to Windows,\n" +
			"what fills the running distributions, and shrinks the chosen disks step by step.\n\n" +
			"Started without a command in a terminal it works in the mode the settings say: TUI\n" +
			"opens the screen, CLI prints the disks and the commands to use. Every command works\n" +
			"in both. The first start opens setup, to choose; wsl-shrinker setup opens it again.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			// The language: --lang, then the settings, then Windows.
			setting := lang
			if setting == "" {
				if a, err := p.get(); err == nil {
					setting = a.Config.Language
				}
			}
			if setting != "" && setting != "auto" && setting != "en" && setting != "ko" {
				return fmt.Errorf("%w: --lang takes auto, en or ko", errUsage)
			}
			i18n.Set(i18n.Choose(setting, winapi.Korean()))
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			// A wrong theme is refused like a wrong language, before anything is read.
			if !slices.Contains(themes, theme) {
				return fmt.Errorf("%w: --theme takes auto, dark or light", errUsage)
			}
			// The clean choices are checked first too: a mistake stops before the screen.
			cleanMap, err := parseScreenClean(clean)
			if err != nil {
				return err
			}
			// A frame for the README's pictures, from the made-up PC.
			if snapshot != "" {
				w, h, err := size(snapshot)
				if err != nil {
					return err
				}
				d := tui.NewDemo()
				d.Fast = true
				// CLI mode's overview, in color, for its picture.
				if cliMode {
					return overview(cmd.Context(), d, cmd.OutOrStdout(), look{on: true}, w)
				}
				fmt.Fprintln(cmd.OutOrStdout(), tui.Snapshot(d, w, h, theme != "light", tui.ParseKeys(keys), stopAt))
				return nil
			}
			if demo {
				return screenBug(tui.Run(cmd.Context(), tui.NewDemo(), tui.Options{Theme: theme}), cmd.ErrOrStderr())
			}
			// The window a run moved to says it has started, first of all:
			// the terminal it came from waits for that.
			if handoff != "" {
				if err := app.Received(handoff); err != nil {
					return err
				}
			}
			a, err := p.get()
			if err != nil {
				return err
			}
			ctx, out := cmd.Context(), cmd.OutOrStdout()
			// Without a terminal there is no screen to draw: print the list.
			// A terminal WSL's launcher relays gets CLI mode's table, which
			// needs no console; the launcher draws the screen and setup itself.
			if !stdoutIsTerminal() {
				if relayed() && len(run) == 0 && !reopened {
					return overview(ctx, app.Backend{A: a}, out, lookFor(out), widthOf(out))
				}
				return list(ctx, a, out, false)
			}
			// The window a run moved to goes on with that run, on the screen;
			// anything else starts as the settings say.
			if len(run) == 0 && !reopened {
				// The first start in a terminal asks how to work, once: with
				// no settings file, setup comes first, and leaving it writes
				// the defaults.
				if a.ConfigPath != "" && !exists(a.ConfigPath) && stdinIsTerminal() {
					if _, err := runSetup(ctx, out, a, true, lang); err != nil {
						return err
					}
					// The language just chosen, unless --lang said: the
					// screen may have shown another while choosing.
					i18n.Set(i18n.Choose(cmp.Or(lang, a.Config.Language), winapi.Korean()))
				}
				// CLI mode: the disks and the commands, no screen.
				if a.Config.Mode == "cli" {
					return overview(ctx, app.Backend{A: a}, out, lookFor(out), widthOf(out))
				}
			}
			return screen(ctx, a, theme, tui.Options{Run: run, Clean: cleanMap}, reopened, cmd.ErrOrStderr())
		},
	}
	root.Flags().BoolVar(&demo, "demo", false, "open the screen on a made-up PC; nothing real is read or changed")
	root.Flags().StringVar(&theme, "theme", "", "colors of the screen: auto, dark or light (default: the settings, then ask the terminal)")
	// The commands print English; the flag is on every command so a run that
	// moves to a new window keeps the language of the screen there.
	root.PersistentFlags().StringVar(&lang, "lang", "", "language of the screen, setup and CLI mode; the other commands print English: auto, en or ko (default: the settings, then WSL_SHRINKER_LANG, then Windows)")
	root.Flags().StringVar(&snapshot, "snapshot", "", "print one frame WIDTHxHEIGHT of the demo and exit")
	root.Flags().StringVar(&keys, "keys", "", "keys to press before --snapshot prints, like \"c space enter\"")
	root.Flags().StringVar(&stopAt, "stop-at", "", "with --snapshot, stop a run while this step runs")
	root.Flags().BoolVar(&cliMode, "cli", false, "with --snapshot, print CLI mode's overview instead of the screen")
	root.Flags().BoolVar(&reopened, "reopened", false, "this is the new window a run moved to")
	root.Flags().StringVar(&handoff, "handoff", "", "the receipt to leave for the terminal the run moved from")
	root.Flags().StringArrayVar(&run, "run", nil, "start a run of this disk at once, already confirmed")
	root.Flags().StringArrayVar(&clean, "clean", nil, "with --run, NAME=ID: an item to remove inside NAME first; once per item")
	for _, f := range []string{"snapshot", "keys", "stop-at", "cli", "reopened", "handoff", "run", "clean"} {
		_ = root.Flags().MarkHidden(f)
	}
	root.AddCommand(listCmd(p), insideCmd(p), shrinkCmd(p), recoverCmd(p), historyCmd(p), doctorCmd(p), tuiCmd(p), setupCmd(p), configCmd(), versionCmd(), backendCmd(p))
	// Shell completion is cobra's own: it still works, but the help lists
	// only what this program does.
	root.CompletionOptions.HiddenDefaultCmd = true
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return fmt.Errorf("%w: %w", errUsage, err) })
	return root
}

// cobraUsage reports whether an error is cobra's own about how the command
// was called: an unknown command, or the wrong number of arguments. Cobra
// returns these as plain errors, so they are told apart by their words.
func cobraUsage(err error) bool {
	msg := err.Error()
	for _, p := range []string{"unknown command", "accepts ", "requires at least", "requires at most", "unknown flag", "unknown shorthand"} {
		if strings.HasPrefix(msg, p) {
			return true
		}
	}
	return false
}

// noArgs refuses arguments to a command that takes none. Cobra's own words
// for it, "unknown command", point at a missing command instead.
func noArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("%w: %s takes no arguments, got %q", errUsage, cmd.Name(), args[0])
	}
	return nil
}

// parseScreenClean reads the screen's --clean NAME=ID flags, one item each,
// as the screen passes them to the window a run moves to: an id is a path,
// which may hold a comma, so a value is never split into a list.
func parseScreenClean(flags []string) (map[string][]string, error) {
	out := map[string][]string{}
	for _, c := range flags {
		name, id, _ := strings.Cut(c, "=")
		// A name and an id, or the flag says nothing.
		if name == "" || id == "" {
			return nil, fmt.Errorf("%w: --clean takes NAME=ID", errUsage)
		}
		// A repeated id is one item.
		if !slices.Contains(out[name], id) {
			out[name] = append(out[name], id)
		}
	}
	return out, nil
}

// size parses WIDTHxHEIGHT.
func size(s string) (int, int, error) {
	ws, hs, ok := strings.Cut(s, "x")
	w, err1 := strconv.Atoi(ws)
	h, err2 := strconv.Atoi(hs)
	if !ok || err1 != nil || err2 != nil || w < 40 || h < 12 {
		return 0, 0, fmt.Errorf("%w: --snapshot wants WIDTHxHEIGHT of at least 40x12, like 132x34", errUsage)
	}
	return w, h, nil
}

// schema is the version of the JSON the commands print. Every output is an
// object that starts with it, so a later change can say so instead of
// breaking a script quietly.
const schema = 1

// printJSON writes v as indented JSON.
func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// versionCmd prints the build.
func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  noArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintln(cmd.OutOrStdout(), "wsl-shrinker", version.String())
		},
	}
}
