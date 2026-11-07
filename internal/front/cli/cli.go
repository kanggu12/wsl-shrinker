// Package cli is wsl-shrinker's command line: the screen when started bare,
// and commands for scripts and for a look without the screen.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/kanggu12/wsl-shrinker/internal/app"
	"github.com/kanggu12/wsl-shrinker/internal/front/i18n"
	"github.com/kanggu12/wsl-shrinker/internal/front/tui"
	"github.com/kanggu12/wsl-shrinker/internal/machine/winapi"
	"github.com/kanggu12/wsl-shrinker/internal/version"
)

// Exit codes: 0 done, 1 failed, 2 wrong usage.
const (
	exitOK    = 0
	exitFail  = 1
	exitUsage = 2
)

// errUsage marks an error in how the command was called.
var errUsage = errors.New("usage")

// Execute runs the command line and returns the exit code.
func Execute() int { return run(os.Args[1:], os.Stdout, os.Stderr) }

// run runs one command line, writing to out and errOut.
func run(args []string, out, errOut io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	root := newRoot()
	root.SetContext(ctx)
	root.SetArgs(args)
	root.SetOut(out)
	root.SetErr(errOut)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(errOut, "wsl-shrinker:", strings.TrimPrefix(err.Error(), "usage: "))
		if errors.Is(err, errUsage) || cobraUsage(err) {
			return exitUsage
		}
		return exitFail
	}
	return exitOK
}

// newRoot builds every command.
func newRoot() *cobra.Command {
	var (
		demo     bool
		snapshot string
		keys     string
		stopAt   string
		theme    string
		lang     string
		reopened bool
		run      []string
		clean    []string
	)
	root := &cobra.Command{
		Use:   "wsl-shrinker",
		Short: "See where your WSL disk space went, clean it, shrink it, and check it worked",
		Long: "wsl-shrinker shows every WSL 2 disk with what a shrink would give back to Windows,\n" +
			"what fills the running distributions, and shrinks the chosen disks step by step.\n" +
			"Started without a command it opens the screen.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			// The language: --lang, then the settings, then Windows.
			setting := lang
			if setting == "" {
				if a, err := app.New(); err == nil {
					setting = a.Config.Language
				}
			}
			if setting != "" && setting != "auto" && setting != "en" && setting != "ko" {
				return fmt.Errorf("%w: --lang takes en or ko", errUsage)
			}
			i18n.Set(i18n.Choose(setting, winapi.Korean()))
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			// A frame for the README's pictures, from the made-up PC.
			if snapshot != "" {
				w, h, err := size(snapshot)
				if err != nil {
					return err
				}
				d := tui.NewDemo()
				d.Fast = true
				fmt.Fprintln(cmd.OutOrStdout(), tui.Snapshot(d, w, h, theme != "light", tui.ParseKeys(keys), stopAt))
				return nil
			}
			if demo {
				return tui.Run(cmd.Context(), tui.NewDemo(), tui.Options{Theme: theme})
			}
			a, err := app.New()
			if err != nil {
				return err
			}
			// Without a terminal there is no screen to draw: print the list.
			if !term.IsTerminal(os.Stdout.Fd()) {
				return list(cmd.Context(), a, cmd.OutOrStdout(), false)
			}
			if theme == "" {
				theme = a.Config.Theme
			}
			opt := tui.Options{Theme: theme, Run: run, Clean: map[string][]string{}}
			for _, c := range clean {
				name, ids, ok := strings.Cut(c, "=")
				if !ok {
					return fmt.Errorf("%w: --clean takes NAME=ID,ID", errUsage)
				}
				opt.Clean[name] = strings.Split(ids, ",")
			}
			// The window a run moved to does not move again.
			b := app.Backend{A: a, Reopened: reopened}
			return tui.Run(cmd.Context(), b, opt)
		},
	}
	root.Flags().BoolVar(&demo, "demo", false, "open the screen on a made-up PC; nothing real is read or changed")
	root.Flags().StringVar(&theme, "theme", "", "colors: dark or light (default: ask the terminal)")
	root.PersistentFlags().StringVar(&lang, "lang", "", "language: en or ko (default: the settings, then Windows)")
	root.Flags().StringVar(&snapshot, "snapshot", "", "print one frame WIDTHxHEIGHT of the demo and exit")
	root.Flags().StringVar(&keys, "keys", "", "keys to press before --snapshot prints, like \"c space enter\"")
	root.Flags().StringVar(&stopAt, "stop-at", "", "with --snapshot, stop a run while this step runs")
	root.Flags().BoolVar(&reopened, "reopened", false, "this is the new window a run moved to")
	root.Flags().StringArrayVar(&run, "run", nil, "start a run of this disk at once, already confirmed")
	root.Flags().StringArrayVar(&clean, "clean", nil, "with --run, NAME=ID,ID to remove inside NAME first")
	for _, f := range []string{"snapshot", "keys", "stop-at", "reopened", "run", "clean"} {
		_ = root.Flags().MarkHidden(f)
	}
	root.AddCommand(listCmd(), insideCmd(), shrinkCmd(), historyCmd(), doctorCmd(), configCmd(), versionCmd())
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
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintln(cmd.OutOrStdout(), "wsl-shrinker", version.String())
		},
	}
}
