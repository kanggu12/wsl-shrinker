package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/kanggu12/wsl-shrinker/internal/app"
	"github.com/kanggu12/wsl-shrinker/internal/core/units"
	"github.com/kanggu12/wsl-shrinker/internal/engine/disks"
	"github.com/kanggu12/wsl-shrinker/internal/engine/history"
	"github.com/kanggu12/wsl-shrinker/internal/engine/journal"
	"github.com/kanggu12/wsl-shrinker/internal/engine/shrink"
	"github.com/kanggu12/wsl-shrinker/internal/engine/survey"
	"github.com/kanggu12/wsl-shrinker/internal/front/tui"
	"github.com/kanggu12/wsl-shrinker/internal/front/ui"
	"github.com/kanggu12/wsl-shrinker/internal/machine/winapi"
	"github.com/kanggu12/wsl-shrinker/internal/machine/wsl"
	"github.com/kanggu12/wsl-shrinker/internal/settings/config"
)

// stdinIsTerminal reports whether a person can answer a question, and
// stdoutIsTerminal whether there is a terminal to draw on; tests replace
// them, with the screens they would open.
var (
	stdinIsTerminal  = func() bool { return term.IsTerminal(os.Stdin.Fd()) }
	stdoutIsTerminal = func() bool { return term.IsTerminal(os.Stdout.Fd()) }
	// relayed reports whether WSL's launcher says a person is at the end fd
	// of its terminal (0 stdin, 1 stdout, 2 stderr), which this program may
	// reach only through a pipe: from tmux, zellij, or a terminal whose
	// console is gone. A question, colors and CLI mode's table work then;
	// a screen does not.
	relayed    = func(fd int) bool { return strings.Contains(os.Getenv("WSL_SHRINKER_TTY"), strconv.Itoa(fd)) }
	openScreen = tui.Run
	openSetup  = tui.RunSetup
)

// listCmd prints every disk with its estimate.
func listCmd(p *apps) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the WSL disks and what a shrink would give back",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := p.get()
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
		// None is an empty list, never null, for a script to loop over.
		// Each disk says whether it is protected, and the first thing that
		// stands in the way of a shrink now, "" for none: a script learns it
		// here rather than from a shrink that fails.
		type listed struct {
			disks.Disk
			Protected bool   `json:"protected"`
			Blocked   string `json:"blocked"`
		}
		canCompact := a.Info(ctx).CanCompact
		out := []listed{}
		for _, d := range ds {
			e := listed{Disk: d, Protected: a.Config.Protected(d.Name)}
			if bs := shrink.Preflight(d, canCompact, e.Protected); len(bs) > 0 {
				e.Blocked = string(bs[0])
			}
			out = append(out, e)
		}
		return printJSON(w, struct {
			Schema int      `json:"schema"`
			Disks  []listed `json:"disks"`
		}{schema, out})
	}
	// Names and states read from the left, sizes line up on the right; the
	// last column holds why a disk could not be read. On a terminal the
	// states and what each frees take the screen's colors.
	l := lookFor(w)
	canCompact, minGain := true, int64(a.Config.MinGainGB*(1<<30))
	if l.on {
		canCompact = a.Info(ctx).CanCompact
	}
	rows := [][]string{{l.muted("DISK"), l.muted("STATE"), l.muted("FILE"), l.muted("AFTER"), l.muted("FREES"), ""}}
	for _, d := range ds {
		state := "stopped"
		switch {
		case a.Config.Protected(d.Name):
			// Never shrunk, whatever else it is.
			state = "protected"
		case d.Err != "" || d.Estimate == nil:
			state = "unreadable"
		case d.File.Encrypted:
			state = "encrypted"
		case d.Estimate.FSErrors:
			state = "fs errors"
		case d.Estimate.Fixed:
			state = "fixed"
		case d.Kind != disks.KindDistro && d.File.Held:
			// Docker Desktop's disk, or a file from the settings, that its
			// program has open.
			state = "in use"
		case d.Kind != disks.KindDistro:
			state = "file"
		case d.Running:
			state = "running"
		}
		switch {
		case state == "protected":
			state = l.safe(state)
		case state == "unreadable" || state == "encrypted" || state == "fs errors" || state == "fixed":
			state = l.bad(state)
		case d.Running || state == "in use":
			state = l.live(state)
		}
		if d.Estimate == nil {
			rows = append(rows, []string{d.Name, state, "-", "-", "-", l.bad(d.Err)})
			continue
		}
		e := d.Estimate
		frees := units.Bytes(e.Reclaim)
		if l.on {
			// The color the screen gives the disk.
			block := shrink.Block("")
			if bs := shrink.Preflight(d, canCompact, a.Config.Protected(d.Name)); len(bs) > 0 {
				block = bs[0]
			}
			paint, _ := overviewLook(l, d, block, minGain)
			frees = paint(frees)
		}
		rows = append(rows, []string{d.Name, state, units.Bytes(e.FileSize), units.Bytes(e.After), frees, ""})
	}
	return writeTable(w, []bool{false, false, true, true, true, false}, rows)
}

// insideCmd prints what can go inside a running distribution.
func insideCmd(p *apps) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "inside DISTRO",
		Short: "Show what can be removed inside a running distribution",
		Args: func(cmd *cobra.Command, args []string) error {
			// One name: the survey looks inside one distribution at a time.
			if len(args) != 1 {
				return hint(fmt.Errorf("%w: inside takes one distribution name", errUsage), "see wsl-shrinker list")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := p.get()
			if err != nil {
				return err
			}
			r, err := survey.Run(cmd.Context(), a.WSL, args[0])
			if err != nil {
				return insideHint(err, args[0])
			}
			if asJSON {
				// The distribution as WSL names it, whatever case it was typed in.
				name := args[0]
				if all, err := a.WSL.Distros(cmd.Context()); err == nil {
					if i := slices.IndexFunc(all, func(d wsl.Distro) bool { return strings.EqualFold(d.Name, name) }); i >= 0 {
						name = all[i].Name
					}
				}
				// The whole look: the root filesystem's numbers, Docker, and
				// the items with their ids.
				return printJSON(cmd.OutOrStdout(), struct {
					Schema       int    `json:"schema"`
					Distribution string `json:"distribution"`
					survey.Result
				}{schema, name, r})
			}
			// The size lines up on the right, the words on the left.
			rows := [][]string{{"SIZE", "KIND", "ID", "WHERE"}}
			for _, it := range r.Items {
				rows = append(rows, []string{units.Bytes(it.Bytes), string(it.Kind), it.ID, it.Label})
			}
			return writeTable(cmd.OutOrStdout(), []bool{true, false, false, false}, rows)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

// shrinkCmd shrinks disks named on the command line.
func shrinkCmd(p *apps) *cobra.Command {
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
		Args: func(cmd *cobra.Command, args []string) error {
			// Nothing is shrunk without a name: there is no "all".
			if len(args) == 0 {
				return hint(fmt.Errorf("%w: shrink takes the names of the disks", errUsage), "see wsl-shrinker list")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			// The question needs someone to answer it: without a terminal and
			// without --yes, stop before anything is read or asked.
			if err := needAnswer(yes, "shrink"); err != nil {
				return err
			}
			a, err := p.get()
			if err != nil {
				return err
			}
			// A run that has not ended comes first: one cut off would lose
			// what it stopped, one going on holds the journal.
			if s, fix := unfinished(a.Pending()); s != "" {
				return hint(errors.New(s), fix)
			}
			ctx := cmd.Context()
			out := cmd.OutOrStdout()
			l := lookFor(out)
			// The summary and the question go to stderr: stdout carries only
			// the steps, or the JSON a script reads.
			talk := cmd.ErrOrStderr()
			ds, err := disks.Find(ctx, a.DisksEnv())
			if err != nil {
				return err
			}
			// Find every named disk first: a typo stops before anything runs.
			var queue []disks.Disk
			for _, name := range args {
				i := slices.IndexFunc(ds, func(d disks.Disk) bool { return strings.EqualFold(d.Name, name) })
				if i < 0 {
					return hint(fmt.Errorf("%w: no disk named %q", errUsage, name), "see wsl-shrinker list")
				}
				// A disk named again, in any letter case as WSL's names go, is
				// in the queue already: run twice, it would be stopped and
				// compacted twice for nothing.
				if slices.ContainsFunc(queue, func(d disks.Disk) bool { return d.Name == ds[i].Name }) {
					continue
				}
				queue = append(queue, ds[i])
			}
			// The engine's own rule, before the question: a disk a run's
			// check would refuse (protected, encrypted, in use, ...) stops
			// here, with the reason.
			canCompact := a.Info(ctx).CanCompact
			for _, d := range queue {
				if bs := shrink.Preflight(d, canCompact, a.Config.Protected(d.Name)); len(bs) > 0 {
					return blockHint(fmt.Errorf("%s cannot be shrunk now: %s", d.Name, bs[0].Reason(d)), bs[0], d)
				}
			}
			chosen, err := cleanChoices(ctx, a, queue, clean)
			if err != nil {
				return err
			}
			// From inside WSL, the distribution this runs in may stop: go on
			// in a new window when any running distribution is in the queue.
			moves := slices.ContainsFunc(queue, func(d disks.Disk) bool { return d.Running && d.Distro != "" && a.Hosts(d.Distro) })
			// The run moves to the screen in a new window, and its result
			// stays there: a script waiting on this command could not read
			// it, so --json is refused, before the question, rather than
			// answered with nothing.
			if moves && asJSON {
				return hint(fmt.Errorf("%w: --json cannot follow a run into a new window", errUsage), "run it from PowerShell")
			}
			if !yes {
				for _, d := range queue {
					gain := "?"
					if d.Estimate != nil {
						// With what --clean removes inside it first, as the run's
						// check step and the screen's question count it: only in
						// a distribution that runs, the one kind cleanChoices
						// takes items for.
						n := d.Estimate.Reclaim
						if d.Running {
							for _, it := range chosen[d.Name] {
								n += it.Bytes
							}
						}
						gain = units.Bytes(n)
					}
					fmt.Fprintf(talk, "%s: about %s back to Windows", d.Name, gain)
					if d.Running {
						fmt.Fprint(talk, "; it stops and starts again")
					}
					fmt.Fprintln(talk)
				}
				// Ctrl+C at the question ends it like a no; a bug in reading
				// the answer is said as one.
				if err := confirm(cmd, "Shrink? [y/N] ", "not shrunk"); err != nil {
					return err
				}
			}
			if moves {
				if err := app.Reopen(app.HandoffArgs(cmd.Flag("lang").Value.String(), queue, chosen)); err != nil {
					return err
				}
				fmt.Fprintln(out, "Continuing in a new window: this terminal runs inside the distribution being shrunk.")
				fmt.Fprintln(out, "The result is in that window and in `wsl-shrinker history`.")
				return nil
			}
			var results []shrink.Result
			failed := false
			// The distribution of the disk run last, for the note a failed
			// run that still started its containers again ends with.
			lastDistro := ""
			for _, d := range queue {
				lastDistro = d.Distro
				// Ctrl+C between disks ends the run before the next one.
				if ctx.Err() != nil {
					failed = true
					results = append(results, shrink.Result{Disk: d.Name, Err: "cancelled"})
					break
				}
				if !asJSON {
					fmt.Fprintf(out, "%s\n", l.bold(d.Name))
				}
				res := a.Shrink(ctx, d, shrink.Options{Clean: chosen[d.Name]}, func(e shrink.Event) {
					if asJSON || e.Status == shrink.Running {
						return
					}
					fmt.Fprintln(out, stepLine(l, e))
				})
				results = append(results, res)
				// The history is the record a bug report starts from: say
				// when it could not be written.
				if n := res.Notes["history"]; n != "" {
					fmt.Fprintf(talk, "note: the run was not written to the history: %s\n", n)
				}
				if res.Err != "" {
					failed = true
					break
				}
				if !asJSON {
					fmt.Fprintf(out, "  %s\n", l.gain(fmt.Sprintf("freed %s in %s", units.Bytes(res.Freed), res.Took.Round(time.Second))))
					// The containers started again run only while WSL keeps
					// the distribution running.
					if s := idleNote(l, res, d.Distro); s != "" {
						fmt.Fprintf(out, "  %s\n", s)
					}
				}
			}
			if asJSON {
				if err := printJSON(out, struct {
					Schema  int             `json:"schema"`
					Results []shrink.Result `json:"results"`
				}{schema, results}); err != nil {
					return err
				}
			}
			if failed {
				last := results[len(results)-1]
				// How the failure left the disk, before why it failed: what
				// to do next comes first.
				if !asJSON {
					if s := leftWords(last); s != "" {
						// A distribution left not running is the line to read.
						if last.Left == shrink.LeftDown || last.Left == shrink.LeftSuspect {
							s = l.warn(s)
						}
						fmt.Fprintf(out, "  %s\n", s)
					}
					// Running again with its containers, as after a run that
					// went through: they last as long as WSL keeps it running.
					if s := idleNote(l, last, lastDistro); s != "" {
						fmt.Fprintf(out, "  %s\n", s)
					}
				}
				err := fmt.Errorf("%s: %s", last.Disk, last.Err)
				// A distribution left not running is more than a failure:
				// exit 3, so a script does not go on as if it ran.
				if last.Left == shrink.LeftDown || last.Left == shrink.LeftSuspect {
					return attention{err}
				}
				return err
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the results as JSON")
	cmd.Flags().StringArrayVar(&clean, "clean", nil, "remove items inside a disk first, as `DISK=ID,ID` with the ids wsl-shrinker inside lists; once per disk")
	return cmd
}

// idleNote is the line under a run that started its distribution and
// containers again: they run only while WSL keeps the distribution running
// (shrink.IdleNote). It is "" for any other run: one whose distribution
// never stopped, or a disk that is not a distribution.
func idleNote(l look, r shrink.Result, distro string) string {
	if r.Left != shrink.LeftRunning || !shrink.Idle(r.DistroStarted, r.ContainersStarted) || distro == "" {
		return ""
	}
	return l.muted("note: " + shrink.IdleNote(distro))
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
			return nil, hint(fmt.Errorf("%s is not running: nothing inside it can be removed", d.Name), "wsl -d "+ui.ShellArg(d.Name)+" starts it")
		}
		r, err := survey.Run(ctx, a.WSL, d.Distro)
		if err != nil {
			return nil, err
		}
		items, bad := pickItems(ids, r.Items)
		if bad != "" {
			return nil, hint(fmt.Errorf("%w: nothing with id %q inside %s now", errUsage, bad, d.Name), "see wsl-shrinker inside "+ui.ShellArg(d.Name))
		}
		// Two flags for one distribution add up; an item named twice is one.
		for _, it := range items {
			if !slices.ContainsFunc(out[d.Name], func(o survey.Item) bool { return o.ID == it.ID }) {
				out[d.Name] = append(out[d.Name], it)
			}
		}
	}
	return out, nil
}

// leftWords says, in a line, how a failed run left its disk: whether the
// disk changed and whether its distribution runs, with what to do when it
// does not. "" when there is nothing to add (a check that refused).
func leftWords(r shrink.Result) string {
	changed := "the disk was not changed"
	if r.Compacted {
		changed = "the disk was compacted"
	}
	switch r.Left {
	case shrink.LeftRunning:
		if r.ContainersNotStarted > 0 {
			return fmt.Sprintf("%s; %s runs again, %d containers did not start", changed, r.Disk, r.ContainersNotStarted)
		}
		if r.FailedAt == shrink.StepCheck {
			return ""
		}
		return fmt.Sprintf("%s; %s runs as it did", changed, r.Disk)
	case shrink.LeftDown:
		return fmt.Sprintf("%s; %s did not start again: wsl -d %s starts it", changed, r.Disk, ui.ShellArg(r.Disk))
	case shrink.LeftSuspect:
		return fmt.Sprintf("%s, but it does not read as before; %s stays stopped until you know why (see Troubleshooting)", changed, r.Disk)
	case shrink.LeftOtherStop:
		// Another wsl-spawner stop ends it: what to do is to wait for it.
		return fmt.Sprintf("%s; another wsl-spawner stop is ending %s: shrink it once that stop is done", changed, r.Disk)
	}
	if r.FailedAt == shrink.StepCheck {
		return ""
	}
	return changed
}

// stepLine is one step of a run as it ended. Piped it reads
// "  STEP     STATUS   detail", the columns a script splits; on a terminal
// a mark says how it went: ✓ done, ✗ failed, ! done with a warning, - skipped.
func stepLine(l look, e shrink.Event) string {
	if !l.on {
		return strings.TrimRight(fmt.Sprintf("  %-8s %-8s %s", e.Step, e.Status, e.Detail), " ")
	}
	mark := l.muted("-")
	switch e.Status {
	case shrink.Done:
		mark = l.gain("✓")
	case shrink.Failed:
		mark = l.bad("✗")
	case shrink.Warned:
		mark = l.warn("!")
	}
	detail := e.Detail
	if e.Status == shrink.Failed {
		detail = l.bad(detail)
	} else {
		detail = l.muted(detail)
	}
	return strings.TrimRight(fmt.Sprintf("  %s %-8s %s", mark, e.Step, detail), " ")
}

// pickItems finds the items a comma-separated list of ids names. An id may
// hold a comma itself (a folder like /home/a,b), so pieces are joined until
// they name an item. It returns the first piece that names nothing, if any.
func pickItems(list string, items []survey.Item) ([]survey.Item, string) {
	var got []survey.Item
	cur := ""
	for _, piece := range strings.Split(list, ",") {
		// The id read so far, with this piece added back after its comma.
		if cur == "" {
			cur = piece
		} else {
			cur += "," + piece
		}
		j := slices.IndexFunc(items, func(it survey.Item) bool { return it.ID == cur })
		if j < 0 {
			continue
		}
		// One item named twice is removed once.
		if !slices.ContainsFunc(got, func(it survey.Item) bool { return it.ID == cur }) {
			got = append(got, items[j])
		}
		cur = ""
	}
	if cur != "" {
		// What was left over names nothing: report where it started.
		first, _, _ := strings.Cut(cur, ",")
		return nil, first
	}
	return got, ""
}

// ask reads a yes or no; anything but y or yes is no. Ctrl+C while it waits
// ends the wait with the context's error: the signal is caught for the run,
// so it would not end the read on its own.
func ask(ctx context.Context, in io.Reader, out io.Writer, q string) (bool, error) {
	fmt.Fprint(out, q)
	// The line read, or the bug that stopped the read.
	type answer struct {
		line  string
		bug   any
		stack []byte
	}
	line := make(chan answer, 1)
	// The read cannot be stopped; when Ctrl+C comes first it is left behind,
	// and the program ends soon after.
	go func() {
		// A bug here says so as the command's error, as one anywhere else
		// does, rather than as a bare Go panic.
		defer func() {
			if r := recover(); r != nil {
				line <- answer{bug: r, stack: debug.Stack()}
			}
		}()
		s, _ := bufio.NewReader(in).ReadString('\n')
		line <- answer{line: s}
	}()
	select {
	case <-ctx.Done():
		// The prompt's line ends, so the error starts on a line of its own.
		fmt.Fprintln(out)
		return false, ctx.Err()
	case got := <-line:
		if got.bug != nil {
			// Where it was, above what to do about it.
			fmt.Fprintln(out)
			out.Write(got.stack)
			return false, hint(fmt.Errorf("stopped on a bug: %v", got.bug), "please report it with the lines above: "+IssuesURL)
		}
		a := strings.ToLower(strings.TrimSpace(got.line))
		return a == "y" || a == "yes", nil
	}
}

// recoverCmd finishes a run that was cut off, or with --forget closes it
// as it is.
func recoverCmd(p *apps) *cobra.Command {
	var forget, yes bool
	cmd := &cobra.Command{
		Use:   "recover",
		Short: "Finish a run that was cut off: start its distribution and containers again",
		Long: "Finishes a run that was cut off (the window closed, a crash, the power): the\n" +
			"distribution it had stopped starts again, with the containers it had stopped.\n\n" +
			"--forget closes the run as it is instead and starts nothing: the way out when\n" +
			"the start keeps failing. It says how to start by hand what the run had stopped.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// recover alone asks nothing: --yes would answer no question.
			if yes && !forget {
				return fmt.Errorf("%w: --yes goes with --forget; recover alone asks nothing", errUsage)
			}
			if forget {
				return forgetRun(cmd, p, yes)
			}
			a, err := p.get()
			if err != nil {
				return err
			}
			did, err := a.Recover(cmd.Context())
			if err != nil {
				// A start that failed leaves the run cut off, and every later
				// run held back: the two ways on are said.
				var still app.StillCutOff
				if errors.As(err, &still) {
					return hint(err, "wsl-shrinker recover tries again; wsl-shrinker recover --forget closes the run as it is")
				}
				return err
			}
			sayDone(cmd.OutOrStdout(), did)
			return nil
		},
	}
	cmd.Flags().BoolVar(&forget, "forget", false, "close the run as it is, starting nothing again")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "with --forget, do not ask")
	return cmd
}

// forgetRun closes a run that was cut off without starting anything again,
// after a question that says what the run had stopped.
func forgetRun(cmd *cobra.Command, p *apps, yes bool) error {
	// The question needs someone to answer it: without a terminal and
	// without --yes, stop before anything is read.
	if err := needAnswer(yes, "forget it"); err != nil {
		return err
	}
	a, err := p.get()
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	// A journal that cannot be read is said: "no run" would be a guess.
	e, st, err := a.Journal.Read()
	if err != nil {
		return err
	}
	switch st {
	case journal.None:
		// Nothing to forget is no mistake: a script may ask to be sure.
		sayDone(out, "no run was cut off")
		return nil
	case journal.Going:
		// Another process is at it: that run is not this one's to close.
		s, fix := unfinished(e, st)
		return hint(errors.New(s), fix)
	}
	// What it had stopped is what forgetting leaves stopped.
	ids := shrink.Startable(e.Containers)
	stopped := e.Stopped && e.Distro != ""
	if !yes {
		// The question goes to stderr, as every question does.
		talk := cmd.ErrOrStderr()
		s, _ := unfinished(e, st)
		fmt.Fprintln(talk, s)
		switch {
		case e.Disk == "":
			// A journal that did not parse says nothing of what its run
			// had stopped: that is said, not guessed.
			fmt.Fprintln(talk, "its journal cannot be read: what it had stopped is not known")
		case stopped && len(ids) > 0:
			fmt.Fprintf(talk, "it had stopped %s and %s; forgetting it starts nothing again\n", e.Distro, counted(len(ids), "container"))
		case stopped:
			fmt.Fprintf(talk, "it had stopped %s; forgetting it starts nothing again\n", e.Distro)
		default:
			fmt.Fprintln(talk, "it had stopped nothing")
		}
		if err := confirm(cmd, "Forget it? [y/N] ", "not forgotten"); err != nil {
			return err
		}
	}
	// The run as the journal has it now: another process may have finished
	// it while the question waited, and another run may have been cut off
	// since, which is not the one asked about.
	shown := e
	e, st, err = a.Forget(&shown)
	if errors.Is(err, app.ErrRunChanged) {
		return hint(err, "wsl-shrinker recover --forget asks about the run cut off now")
	}
	if err != nil {
		return err
	}
	if st == journal.None {
		sayDone(out, "no run was cut off")
		return nil
	}
	did := "forgot the run that was cut off"
	if e.Disk != "" {
		did = "forgot the run of " + e.Disk + " that was cut off at " + e.Step
	}
	// By hand from here: the commands a run would have given.
	if e.Stopped && e.Distro != "" {
		did += "; nothing was started again\nnote: to start by hand what it had stopped: wsl -d " + ui.ShellArg(e.Distro)
		if ids := shrink.Startable(e.Containers); len(ids) > 0 {
			did += ", then docker start " + strings.Join(ids, " ") + " inside it"
		}
	}
	sayDone(out, did)
	return nil
}

// needAnswer stops a command that would ask a question no one can answer:
// no terminal, none the launcher relays, and no --yes. what is what --yes
// does without the question.
func needAnswer(yes bool, what string) error {
	if yes || stdinIsTerminal() || relayed(0) {
		return nil
	}
	return hint(fmt.Errorf("%w: no terminal to ask in", errUsage), "add --yes to "+what+" without the question")
}

// counted is n of a thing, named for one or for several.
func counted(n int, thing string) string {
	if n == 1 {
		return "1 " + thing
	}
	return strconv.Itoa(n) + " " + thing + "s"
}

// confirm asks q on stderr and returns nil for a yes. A no ends the command
// with the words not; Ctrl+C at the question ends it like a no.
func confirm(cmd *cobra.Command, q, not string) error {
	ok, err := ask(cmd.Context(), cmd.InOrStdin(), cmd.ErrOrStderr(), q)
	switch {
	case errors.Is(err, context.Canceled):
		return errors.New(not + ": cancelled")
	case err != nil:
		return err
	case !ok:
		return errors.New(not)
	}
	return nil
}

// sayDone prints what a command did. On a terminal its first line follows a
// check mark and the notes under it are muted; plain, it is the text as it
// is, for a script.
func sayDone(out io.Writer, text string) {
	l := lookFor(out)
	first, notes, _ := strings.Cut(text, "\n")
	fmt.Fprintln(out, l.done(first))
	if notes != "" {
		fmt.Fprintln(out, l.muted(notes))
	}
}

// unfinished says why a run cannot start while another has not ended, and
// what to do about it, from the journal's entry and state; "" when none is
// pending.
func unfinished(e journal.Entry, st journal.State) (string, string) { return journal.Words(e, st) }

// insideHint adds what to do to a look inside that failed.
func insideHint(err error, name string) error {
	switch {
	case errors.Is(err, wsl.ErrNotRunning):
		// Nothing is started just to look inside it.
		return hint(err, "wsl -d "+ui.ShellArg(name)+" starts it")
	case errors.Is(err, wsl.ErrNoDistro):
		return hint(err, "see wsl-shrinker list")
	}
	return err
}

// blockHint adds what to do to a disk the engine's rule refuses, where its
// reason does not say already.
func blockHint(err error, b shrink.Block, d disks.Disk) error {
	switch b {
	case shrink.BlockProtected:
		return hint(err, "to shrink it after all: wsl-shrinker setup --unprotect "+ui.ShellArg(d.Name))
	case shrink.BlockUnreadable:
		return hint(err, "wsl-shrinker doctor says more")
	case shrink.BlockEncrypted:
		return hint(err, "decrypt the file in its Properties, then shrink it")
	}
	return err
}

// historyCmd prints past runs, or with --clear removes them.
func historyCmd(p *apps) *cobra.Command {
	var asJSON, clear, yes bool
	cmd := &cobra.Command{
		Use:   "history",
		Short: "Show past runs",
		Long: "Shows past runs: when, which disk, its size before and after, and how it ended.\n\n" +
			"--clear removes them all. Nothing else reads the history: the disks, the settings\n" +
			"and a run that was cut off stay as they are.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// history alone asks nothing: --yes would answer no question.
			if yes && !clear {
				return fmt.Errorf("%w: --yes goes with --clear; history alone asks nothing", errUsage)
			}
			if clear {
				// Nothing is printed for a script to read after a clear.
				if asJSON {
					return fmt.Errorf("%w: --clear prints no JSON; leave --json out", errUsage)
				}
				return clearHistory(cmd, p, yes)
			}
			a, err := p.get()
			if err != nil {
				return err
			}
			all, err := a.History.All()
			if err != nil {
				return err
			}
			if asJSON {
				// None is an empty list, never null.
				if all == nil {
					all = []history.Entry{}
				}
				return printJSON(cmd.OutOrStdout(), struct {
					Schema int             `json:"schema"`
					Runs   []history.Entry `json:"runs"`
				}{schema, all})
			}
			// Sizes line up on the right, the time, the disk and the result on
			// the left; on a terminal a run that failed reads in red, and no
			// run yet is said rather than shown as a lone heading.
			l := lookFor(cmd.OutOrStdout())
			if len(all) == 0 && l.on {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), l.muted("No run yet."))
				return err
			}
			rows := [][]string{{l.muted("WHEN"), l.muted("DISK"), l.muted("BEFORE"), l.muted("AFTER"), l.muted("FREED"), l.muted("RESULT")}}
			for _, e := range all {
				res, freed := l.gain("ok"), l.gain(units.Bytes(e.Freed))
				if e.Err != "" {
					res, freed = l.bad(e.Err), units.Bytes(e.Freed)
				}
				rows = append(rows, []string{l.muted(e.At.Local().Format("2006-01-02 15:04")), e.Disk,
					units.Bytes(e.Before), units.Bytes(e.After), freed, res})
			}
			return writeTable(cmd.OutOrStdout(), []bool{false, false, true, true, true, false}, rows)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	cmd.Flags().BoolVar(&clear, "clear", false, "remove every past run from the history")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "with --clear, do not ask")
	return cmd
}

// clearHistory removes every past run from the history, after a question
// that says how many there are.
func clearHistory(cmd *cobra.Command, p *apps, yes bool) error {
	// The question needs someone to answer it: without a terminal and
	// without --yes, stop before anything is read.
	if err := needAnswer(yes, "clear it"); err != nil {
		return err
	}
	a, err := p.get()
	if err != nil {
		return err
	}
	// A run going on writes its line as it ends: cleared now, the history
	// would come back with that run alone.
	if e, st := a.Pending(); st == journal.Going {
		s, fix := unfinished(e, st)
		return hint(errors.New(s), fix)
	}
	all, err := a.History.All()
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if len(all) == 0 {
		// No run to ask about; a file that holds none (damaged lines, or a
		// write cut short) goes without a word.
		if err := a.History.Clear(); err != nil {
			return err
		}
		sayDone(out, "the history is empty already")
		return nil
	}
	runs := counted(len(all), "run")
	if !yes {
		if err := confirm(cmd, "Clear the history of "+runs+"? [y/N] ", "not cleared"); err != nil {
			return err
		}
	}
	if err := a.History.Clear(); err != nil {
		return err
	}
	sayDone(out, "cleared the history of "+runs)
	return nil
}

// configCmd shows where the settings are, or writes the example file.
func configCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Show or create the settings file",
		// A word that is none of its commands is cobra's "unknown command".
		Args: cobra.NoArgs,
		// Alone it lists its commands. Without a RunE cobra would print the
		// same help for any word after it and exit 0.
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }}
	cmd.AddCommand(
		&cobra.Command{Use: "path", Short: "Print the settings file's path", Args: noArgs,
			Run: func(cmd *cobra.Command, _ []string) {
				fmt.Fprintln(cmd.OutOrStdout(), config.Path(winapi.AppData()))
			}},
		&cobra.Command{Use: "init", Short: "Write a settings file with every key at its default", Args: noArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				p := config.Path(winapi.AppData())
				if _, err := os.Stat(p); err == nil {
					return hint(fmt.Errorf("%s exists already", p), "wsl-shrinker setup changes it; move it away to start from the defaults")
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
