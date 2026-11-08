package cli

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/kanggu12/wsl-shrinker/internal/app"
	"github.com/kanggu12/wsl-shrinker/internal/core/units"
	"github.com/kanggu12/wsl-shrinker/internal/engine/disks"
	"github.com/kanggu12/wsl-shrinker/internal/engine/journal"
	"github.com/kanggu12/wsl-shrinker/internal/engine/shrink"
	"github.com/kanggu12/wsl-shrinker/internal/front/i18n"
	"github.com/kanggu12/wsl-shrinker/internal/front/tui"
	"github.com/kanggu12/wsl-shrinker/internal/front/ui"
)

// themes are the values --theme takes; "" is the settings' own.
var themes = []string{"", "auto", "dark", "light"}

// tuiCmd opens the screen, whatever the mode says.
func tuiCmd(p *apps) *cobra.Command {
	var theme string
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Open the screen (TUI), whatever the mode in the settings",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// A wrong value stops before anything is read.
			if !slices.Contains(themes, theme) {
				return fmt.Errorf("%w: --theme takes dark or light", errUsage)
			}
			// The screen draws on a terminal; a pipe gets nothing it can use.
			if !stdoutIsTerminal() {
				return hint(fmt.Errorf("%w: the screen needs a terminal", errUsage), "wsl-shrinker list prints the disks without one")
			}
			a, err := p.get()
			if err != nil {
				return err
			}
			return screen(cmd.Context(), a, theme, tui.Options{}, false, cmd.ErrOrStderr())
		},
	}
	cmd.Flags().StringVar(&theme, "theme", "", "colors of the screen: dark or light (default: the settings, then ask the terminal)")
	return cmd
}

// screen opens the screen on this PC's disks. theme "" is the settings'
// own; reopened is the new window a run moved to, which does not move again.
// A bug that ends it is said on errOut.
func screen(ctx context.Context, a *app.App, theme string, opt tui.Options, reopened bool, errOut io.Writer) error {
	if theme == "" {
		theme = a.Config.Theme
	}
	opt.Theme = theme
	return screenBug(openScreen(ctx, app.Backend{A: a, Reopened: reopened}, opt), errOut)
}

// overview is what wsl-shrinker prints, started bare in a terminal, in CLI
// mode: the disks with the screen's colors and words, what is worth
// shrinking, and the commands to go on with, a real disk's name in them.
// It is for people, in the language of the settings; list is the table for
// scripts. b is this PC, or the made-up one for the README's picture; a
// line wider than width is cut.
func overview(ctx context.Context, b tui.Backend, w io.Writer, l look, width int) error {
	info := b.Info(ctx)
	// The headline, as the screen's: whether this WSL can compact at all,
	// beside the name, or under it in a narrow terminal.
	can := l.gain("✓") + " " + i18n.T("check.compact", info.Version)
	if !info.CanCompact {
		can = l.bad("✗") + " " + i18n.T("check.nocompact")
	}
	if head := l.bold("wsl-shrinker") + l.muted(" · ") + can; ansi.StringWidth(head) <= width-1 {
		fmt.Fprintln(w, head)
	} else {
		fmt.Fprintln(w, l.bold("wsl-shrinker"))
		fmt.Fprintln(w, can)
	}
	// A run that has not ended comes before everything: it says what to do.
	if s := pendingWords(b); s != "" {
		fmt.Fprintln(w, l.warn("!")+" "+s)
	}
	fmt.Fprintln(w)
	ds, err := b.Disks(ctx)
	if err != nil {
		return hint(fmt.Errorf("could not list the disks: %w", err), "wsl-shrinker doctor checks what a shrink needs")
	}
	if len(ds) == 0 {
		fmt.Fprintln(w, "  "+l.bold(i18n.T("empty.title")))
		fmt.Fprintln(w, "  "+l.muted(i18n.T("overview.empty")))
		return nil
	}
	minGain := b.MinGain()
	// The table: a dot in the disk's color, the columns of the screen, and
	// a note on what stands in the way, if anything does.
	rows := [][]string{{"", l.muted(i18n.T("col.disk")), l.muted(i18n.T("col.state")), l.muted(i18n.T("col.file")),
		l.muted(i18n.T("col.after")), l.muted(i18n.T("col.frees")), ""}}
	// worth is what gives back enough; shrinkable is every disk nothing
	// stands in the way of, for the commands' example.
	var worth, shrinkable []disks.Disk
	var back int64
	for _, d := range ds {
		block := ""
		if bs := shrink.Preflight(d, info.CanCompact, b.Protected(d.Name)); len(bs) > 0 {
			block = string(bs[0])
		}
		paint, note := overviewLook(l, d, shrink.Block(block), minGain)
		if block == "" {
			shrinkable = append(shrinkable, d)
			if d.Estimate.Reclaim >= minGain {
				worth = append(worth, d)
				back += d.Estimate.Reclaim
			}
		}
		state := stateWords(d)
		if d.Running || (d.Kind != disks.KindDistro && d.File.Held) {
			state = l.live(state)
		}
		if d.Estimate == nil {
			rows = append(rows, []string{paint(ui.Block), d.Name, state, "-", "-", "-", note})
			continue
		}
		e := d.Estimate
		rows = append(rows, []string{paint(ui.Block), d.Name, state, units.Bytes(e.FileSize), units.Bytes(e.After), paint(units.Bytes(e.Reclaim)), note})
	}
	// The table in the terminal's width: the notes go first, then the names
	// shorten; the sizes are never cut.
	fitted, right := fitTable(rows, []bool{false, false, false, true, true, true, false}, width-1)
	if err := writeTable(w, right, fitted); err != nil {
		return err
	}
	fmt.Fprintln(w)
	// What is worth it, all told, wrapped to the terminal.
	var sum string
	switch {
	case len(worth) == 0:
		sum = l.muted(i18n.T("overview.none", units.Bytes(minGain)))
	case len(worth) == 1:
		sum = l.gain(i18n.T("overview.worth.single", worth[0].Name, units.Bytes(back)))
	default:
		sum = l.gain(i18n.T("overview.worth", len(worth), units.Bytes(back)))
	}
	for _, line := range strings.Split(lipgloss.NewStyle().Width(width-1).Render(sum), "\n") {
		fmt.Fprintln(w, strings.TrimRight(line, " "))
	}
	fmt.Fprintln(w)
	// The commands to go on with, with the disk that gives back most of
	// those that can be shrunk now.
	slices.SortStableFunc(shrinkable, func(x, y disks.Disk) int { return cmp.Compare(y.Estimate.Reclaim, x.Estimate.Reclaim) })
	var cmds [][2]string
	// None that can be shrunk now: no shrink to suggest.
	if len(shrinkable) > 0 {
		cmds = append(cmds, [2]string{"wsl-shrinker shrink " + shellArg(shrinkable[0].Name), i18n.T("overview.cmd.shrink")})
	}
	// A look inside needs a running distribution: the one worth most, or
	// any that runs.
	pool := append(slices.Clone(shrinkable), ds...)
	if i := slices.IndexFunc(pool, func(d disks.Disk) bool { return d.Running && d.Distro != "" }); i >= 0 {
		cmds = append(cmds, [2]string{"wsl-shrinker inside " + shellArg(pool[i].Name), i18n.T("overview.cmd.inside")})
	}
	cmds = append(cmds,
		[2]string{"wsl-shrinker tui", i18n.T("overview.cmd.tui")},
		[2]string{"wsl-shrinker setup", i18n.T("overview.cmd.setup")},
		[2]string{"wsl-shrinker --help", i18n.T("overview.cmd.help")})
	// Each command whole; what it does, cut to the terminal when it must be.
	cmdW := 0
	for _, c := range cmds {
		cmdW = max(cmdW, ansi.StringWidth(c[0]))
	}
	var hints [][]string
	room := width - 1 - (2 + cmdW + 2)
	for _, c := range cmds {
		what := c[1]
		switch {
		case room < 8:
			// Too narrow to say anything useful: the commands alone.
			what = ""
		case ansi.StringWidth(what) > room:
			what = ansi.Truncate(what, room-1, "") + "…"
		}
		hints = append(hints, []string{"  " + l.bold(c[0]), l.muted(what)})
	}
	return writeTable(w, []bool{false, false}, hints)
}

// fitTable makes the overview's table fit width cells, dropping what says
// least first: the notes, then room in the names (five cells kept), then
// the size after (the file less what goes back), the file's size and the
// state. The disk and what it gives back always stay, whole. It returns the
// rows and which columns line up on the right.
func fitTable(rows [][]string, right []bool, width int) ([][]string, []bool) {
	fits := func() bool {
		var b strings.Builder
		_ = writeTable(&b, right, rows)
		for _, l := range strings.Split(b.String(), "\n") {
			if ansi.StringWidth(l) > width {
				return false
			}
		}
		return true
	}
	// drop takes column c out of every row.
	drop := func(c int) {
		for i, r := range rows {
			rows[i] = append(r[:c:c], r[c+1:]...)
		}
		right = append(right[:c:c], right[c+1:]...)
	}
	if fits() {
		return rows, right
	}
	// The notes: the last column.
	drop(6)
	if fits() {
		return rows, right
	}
	// The names, as short as they must be.
	var b strings.Builder
	_ = writeTable(&b, right, rows)
	over := 0
	for _, l := range strings.Split(b.String(), "\n") {
		over = max(over, ansi.StringWidth(l)-width)
	}
	names := 0
	for _, r := range rows {
		names = max(names, ansi.StringWidth(r[1]))
	}
	to := max(5, names-over)
	for _, r := range rows {
		if ansi.StringWidth(r[1]) > to {
			r[1] = ansi.Truncate(r[1], to-1, "") + "…"
		}
	}
	// Then the size after, the file's size and the state, one by one.
	for _, c := range []int{4, 3, 2} {
		if fits() {
			break
		}
		drop(c)
	}
	return rows, right
}

// overviewLook is a disk's color, as the screen gives it, and the note on
// what stands in its way: pink protected, indigo in use, teal worth
// shrinking, grey little to give back, red cannot be shrunk now.
func overviewLook(l look, d disks.Disk, block shrink.Block, minGain int64) (func(string) string, string) {
	switch block {
	case shrink.BlockProtected:
		return l.safe, l.safe(i18n.T("setup.disk.protected"))
	case shrink.BlockHeld:
		// Docker Desktop's disk, or a file another program has open.
		// The state says "in use" already: the note says what to do.
		if d.Kind == disks.KindDocker {
			return l.live, l.muted(i18n.T("overview.held"))
		}
		return l.live, l.muted(i18n.T("overview.held.file"))
	case shrink.BlockUnreadable:
		return l.bad, l.bad(i18n.T("check.unreadable", d.Err))
	case shrink.BlockEncrypted:
		return l.bad, l.bad(i18n.T("check.encrypted"))
	case shrink.BlockFSErrors:
		return l.bad, l.bad(i18n.T("check.fserrors"))
	case shrink.BlockNoCompact:
		// Said once, in the headline.
		return l.bad, ""
	case "":
		if d.Estimate.Reclaim >= minGain {
			return l.gain, ""
		}
		return l.muted, ""
	}
	return l.bad, ""
}

// widthOf is the width of the terminal w writes to, 100 when it is none.
func widthOf(w io.Writer) int {
	if f, ok := w.(*os.File); ok {
		if tw, _, err := term.GetSize(f.Fd()); err == nil && tw > 0 {
			return tw
		}
	}
	return 100
}

// stateWords is a disk's state in the screen's words.
func stateWords(d disks.Disk) string {
	switch {
	case d.Kind != disks.KindDistro && d.File.Held:
		return i18n.T("state.inuse")
	case d.Kind != disks.KindDistro:
		return i18n.T("state.file")
	case d.Running:
		return i18n.T("state.running")
	}
	return i18n.T("state.stopped")
}

// pendingWords says, in the settings' language, what to do about a run that
// has not ended, or "" when there is none.
func pendingWords(b tui.Backend) string {
	e, st := b.Pending()
	switch {
	case st == journal.Going:
		return i18n.T("unfinished.going", e.Disk)
	case st == journal.CutOff && (e.Disk == "" || e.Step == ""):
		return i18n.T("overview.cutoff.unknown")
	case st == journal.CutOff:
		return i18n.T("overview.cutoff", e.Disk, i18n.T("step."+e.Step))
	}
	return ""
}
