package tui

import (
	"context"
	"fmt"
	"image/color"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/kanggu12/wsl-shrinker/internal/app"
	"github.com/kanggu12/wsl-shrinker/internal/engine/disks"
	"github.com/kanggu12/wsl-shrinker/internal/engine/estimate"
	"github.com/kanggu12/wsl-shrinker/internal/engine/journal"
	"github.com/kanggu12/wsl-shrinker/internal/engine/shrink"
	"github.com/kanggu12/wsl-shrinker/internal/engine/survey"
	"github.com/kanggu12/wsl-shrinker/internal/front/i18n"
	"github.com/kanggu12/wsl-shrinker/internal/front/ui"
	"github.com/kanggu12/wsl-shrinker/internal/machine/winapi"
	"github.com/kanggu12/wsl-shrinker/internal/machine/wsl"
)

// variant is the made-up PC with some of its answers replaced: the disks,
// WSL's version, what is inside, or a run that fails at a step.
type variant struct {
	*Demo
	disks    func([]disks.Disk) []disks.Disk
	info     *wsl.Info
	inside   *survey.Result
	failAt   shrink.Step
	failDisk string
	failWith string
	// then is what the engine reports after the failure: how starting the
	// distribution again went.
	then []shrink.Event
	// unfinished is a run the journal says has not ended; recovered counts
	// the times Recover was asked, which then clears it.
	unfinished      journal.Entry
	unfinishedState journal.State
	recovered       int
}

func (v *variant) Pending() (journal.Entry, journal.State) { return v.unfinished, v.unfinishedState }

func (v *variant) Recover(context.Context) (string, error) {
	v.recovered++
	v.unfinishedState = journal.None
	return v.unfinished.Disk + ": running, 2 containers started again", nil
}

func (v *variant) Disks(ctx context.Context) ([]disks.Disk, error) {
	l, err := v.Demo.Disks(ctx)
	if v.disks != nil {
		l = v.disks(l)
	}
	return l, err
}

func (v *variant) Info(ctx context.Context) wsl.Info {
	if v.info != nil {
		return *v.info
	}
	return v.Demo.Info(ctx)
}

func (v *variant) Survey(ctx context.Context, distro string) (survey.Result, error) {
	if v.inside != nil {
		return *v.inside, nil
	}
	return v.Demo.Survey(ctx, distro)
}

func (v *variant) Shrink(ctx context.Context, d disks.Disk, opt shrink.Options, emit func(shrink.Event)) shrink.Result {
	// Only the disk named fails; the others run as on the demo.
	if v.failAt == "" || v.failDisk != "" && d.Name != v.failDisk {
		return v.Demo.Shrink(ctx, d, opt, emit)
	}
	res := shrink.Result{Disk: d.Name, Before: d.Estimate}
	for _, s := range shrink.Steps {
		emit(shrink.Event{Step: s, Status: shrink.Running, At: time.Now()})
		if s == v.failAt {
			res.Events = append(res.Events, shrink.Event{Step: s, Status: shrink.Failed, Detail: v.failWith})
			res.Events = append(res.Events, v.then...)
			for _, e := range res.Events[len(res.Events)-1-len(v.then):] {
				emit(e)
			}
			res.Err = v.failWith
			return settledLike(d, res)
		}
		res.Events = append(res.Events, shrink.Event{Step: s, Status: shrink.Done})
		emit(shrink.Event{Step: s, Status: shrink.Done, At: time.Now()})
	}
	return settledLike(d, res)
}

// settledLike fills in how a run left its disk, from its events, as the
// engine's settle does: the screen reads these fields, not the events.
func settledLike(d disks.Disk, res shrink.Result) shrink.Result {
	ran := d.Distro != "" && d.Running
	restart, restarted := shrink.Event{}, false
	for _, e := range res.Events {
		switch {
		case e.Status == shrink.Failed && res.FailedAt == "":
			res.FailedAt = e.Step
		case e.Step == shrink.StepCompact && e.Status == shrink.Done:
			res.Compacted = true
		}
		if e.Step == shrink.StepRestart && e.Status != shrink.Running {
			restart, restarted = e, true
		}
	}
	switch {
	case restarted && restart.Status == shrink.Skipped && res.FailedAt != "":
		res.Left = shrink.LeftSuspect
	case d.Distro == "":
		res.Left = shrink.LeftFile
	case !ran:
		res.Left = shrink.LeftStopped
	case restarted && restart.Status == shrink.Failed:
		res.Left = shrink.LeftDown
	default:
		res.Left, res.ContainersNotStarted = shrink.LeftRunning, restart.NotRestarted
	}
	return res
}

// screen is one frame as plain text lines, in English unless lang says.
func screen(b Backend, w, h int, keys ...string) []string {
	return strings.Split(ansi.Strip(Snapshot(b, w, h, true, keys, "")), "\n")
}

// many is the demo with n more stopped distributions, so the table is
// longer than any window.
func many(n int) func([]disks.Disk) []disks.Disk {
	return func(l []disks.Disk) []disks.Disk {
		for i := range n {
			e := &estimate.Result{FileSize: 9 * gb, After: 5 * gb, Reclaim: 4 * gb, Filesystem: true}
			name := fmt.Sprintf("distro-%02d", i)
			l = append(l, disks.Disk{Name: name, Kind: disks.KindDistro, Distro: name, Path: `D:\WSL\` + name + `\ext4.vhdx`,
				File: winapi.FileInfo{Size: 9 * gb, OnDisk: 9 * gb}, Estimate: e})
		}
		return l
	}
}

// tableRow finds the table's row of a disk: it starts with the disk's dot.
func tableRow(lines []string, name string) (int, bool) {
	re := regexp.MustCompile(`^\s*(✓\s)?\s*▪ ` + regexp.QuoteMeta(name) + `\b`)
	for i, l := range lines {
		if re.MatchString(l) {
			return i, true
		}
	}
	return 0, false
}

func TestTableScrollsToTheSelectedDisk(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 30}} {
		b := &variant{Demo: demo(), disks: many(30)}
		lines := screen(b, size[0], size[1], "end")
		if _, ok := tableRow(lines, "distro-29"); !ok {
			t.Errorf("%dx%d: the last disk is selected but its row is not on screen:\n%s", size[0], size[1], strings.Join(lines, "\n"))
		}
		// Back to the top: the first row comes back.
		lines = screen(b, size[0], size[1], "end", "home")
		if _, ok := tableRow(lines, "Ubuntu-24.04"); !ok {
			t.Errorf("%dx%d: back at the first disk, its row is not on screen", size[0], size[1])
		}
	}
}

func TestTableHeaderSitsOverItsColumns(t *testing.T) {
	for _, w := range []int{80, 132} {
		lines := screen(demo(), w, 30)
		var head string
		for _, l := range lines {
			if strings.Contains(l, "DISK") {
				head = l
				break
			}
		}
		at, ok := tableRow(lines, "Ubuntu-24.04")
		if head == "" || !ok {
			t.Fatalf("%d: no header or no row", w)
		}
		row := lines[at]
		// Positions in cells, not bytes: the dot that leads a row is three bytes.
		cell := func(s, sub string) int { return lipgloss.Width(s[:strings.Index(s, sub)]) }
		if a, b := cell(head, "DISK"), cell(row, "Ubuntu-24.04"); a != b {
			t.Errorf("%d: DISK at %d, the names at %d", w, a, b)
		}
		// The sizes end where their headings end.
		if a, b := cell(head, "FREES")+len("FREES"), cell(row, "64.0 GB")+len("64.0 GB"); a != b {
			t.Errorf("%d: FREES ends at %d, its numbers at %d", w, a, b)
		}
	}
}

func TestDiskThatCannotBeCompactedIsNotCountedAsGain(t *testing.T) {
	old := &variant{Demo: demo(), info: &wsl.Info{Version: "2.0.14.0"}}
	enc := &variant{Demo: demo(), disks: func(l []disks.Disk) []disks.Disk {
		l[0].File.Encrypted = true
		return l
	}}
	// Errors the kernel recorded in Ubuntu's filesystem: the run's check
	// refuses it until e2fsck has looked.
	fserr := &variant{Demo: demo(), disks: func(l []disks.Disk) []disks.Disk {
		e := *l[0].Estimate
		e.FSErrors = true
		l[0].Estimate = &e
		return l
	}}
	for _, c := range []struct {
		name string
		b    *variant
		now  string
	}{
		// An old WSL can compact no distribution: nothing can go now.
		{"old WSL", old, "0 B can be given back now"},
		// Only Debian (23 GB) is worth shrinking now: Ubuntu is encrypted,
		// Docker Desktop holds its disk, Work is protected, and kali's
		// 300 MB is under the gain worth a run, as CLI mode counts too.
		{"encrypted", enc, "23.0 GB can be given back now"},
		{"filesystem errors", fserr, "23.0 GB can be given back now"},
	} {
		name, b := c.name, c.b
		lines := screen(b, 132, 34)
		if !strings.Contains(lines[0], c.now) {
			t.Errorf("%s: header %q", name, strings.TrimSpace(lines[0]))
		}
		m := playKeys(t, b, nil)
		if d, _ := m.selected(); m.category(d) != ui.CatBad {
			t.Errorf("%s: Ubuntu shows as %v, not as one that cannot be shrunk", name, m.category(d))
		}
		if out := strings.Join(lines, "\n"); strings.Contains(out, "Gives back about 64.0 GB.") {
			t.Errorf("%s: the verdict promises what a shrink cannot do now", name)
		}
	}
}

func TestHeldFileFromTheSettingsIsNotDockerDesktop(t *testing.T) {
	b := &variant{Demo: demo(), disks: func([]disks.Disk) []disks.Disk {
		return []disks.Disk{{Name: "data.vhdx", Kind: disks.KindFile, Path: `E:\vm\data.vhdx`,
			File:     winapi.FileInfo{Size: 5 * gb, OnDisk: 5 * gb, Held: true},
			Estimate: &estimate.Result{FileSize: 5 * gb, After: 4 * gb, Reclaim: 1 * gb}}}
	}}
	out := strings.Join(screen(b, 132, 30), "\n")
	if strings.Contains(out, "Docker Desktop") {
		t.Errorf("a file another program holds is blamed on Docker Desktop:\n%s", out)
	}
	if at, ok := tableRow(strings.Split(out, "\n"), "data.vhdx"); !ok || !strings.Contains(strings.Split(out, "\n")[at], "in use") {
		t.Errorf("its state does not say in use")
	}
	m := playKeys(t, b, []string{"s"})
	if !strings.Contains(m.toast, "in use") || strings.Contains(m.toast, "Docker") {
		t.Errorf("toast %q", m.toast)
	}
}

// colorOf is the escape sequence that turns a color on.
func colorOf(c color.Color) string {
	s := ui.Fg(c).Render("x")
	return s[:strings.Index(s, "x")]
}

func TestRunBarsUseTheColorsOfTheBlockMap(t *testing.T) {
	m := playKeys(t, demo(), []string{"down", "down", "s", "enter"})
	for !m.run.done {
		next, _ := m.Update(<-m.run.events)
		m = next.(model)
	}
	teal, slate := colorOf(m.th.Cat[ui.CatDone]), colorOf(m.th.Stays)
	var before, after string
	for _, l := range strings.Split(m.View().Content, "\n") {
		switch s := ansi.Strip(l); {
		case strings.HasPrefix(strings.TrimSpace(s), "before"):
			before = l
		case strings.HasPrefix(strings.TrimSpace(s), "after"):
			after = l
		}
	}
	// Before: the data that stays in slate, what goes back in teal. After: the
	// data alone, in slate.
	if !strings.Contains(before, slate+ui.Block) || !strings.Contains(before, teal+ui.Block) {
		t.Errorf("before bar: %q", before)
	}
	if !strings.Contains(after, slate+ui.Block) || strings.Contains(after, teal+ui.Block) {
		t.Errorf("after bar paints the data that stays as given back: %q", after)
	}
}

func TestRunOfSeveralNamesTheDiskOfTheSteps(t *testing.T) {
	lines := screen(demo(), 120, 34, "space", "down", "space", "s", "enter")
	check := -1
	for i, l := range lines {
		if strings.Contains(l, "Check") {
			check = i
			break
		}
	}
	if check < 0 {
		t.Fatalf("no steps:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(strings.Join(lines[:check], "\n"), "Debian") {
		t.Errorf("the steps on screen are Debian's, and nothing above them says so:\n%s", strings.Join(lines[:check+1], "\n"))
	}
}

func TestConfirmBoxKeepsItsKeysOnASmallScreen(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.English, i18n.Korean} {
		i18n.Set(lang)
		for _, keys := range [][]string{{"s"}, {"space", "down", "space", "s"}} {
			out := strings.Join(screen(demo(), 40, 12, keys...), "\n")
			if !strings.Contains(out, "╰") || !strings.Contains(out, "esc") {
				t.Errorf("%s %v: the box is cut:\n%s", lang, keys, out)
			}
		}
	}
	i18n.Set(i18n.English)
}

func TestCleanListFooterKeepsItsMainKey(t *testing.T) {
	lines := screen(demo(), 60, 16, "c")
	if f := lines[len(lines)-1]; !strings.Contains(f, "space") || !strings.Contains(f, "⏎") {
		t.Errorf("footer %q", f)
	}
}

// joined is a screen's text with its lines and spaces run together, to look
// for a sentence that wraps.
func joined(lines []string) string {
	return strings.Join(strings.Fields(strings.Join(lines, " ")), " ")
}

func TestFailedStepShowsItsWholeError(t *testing.T) {
	why := "Wsl/Service/CompactDistribution/E_ACCESSDENIED: Access is denied. Another process holds the file open."
	b := &variant{Demo: demo(), failAt: shrink.StepCompact, failWith: why}
	if out := joined(screen(b, 80, 24, "down", "down", "s", "enter")); !strings.Contains(out, "holds the file open.") {
		t.Errorf("the error is cut:\n%s", out)
	}
}

func TestDiskWithFilesystemErrorsSaysToCheckItFirst(t *testing.T) {
	b := &variant{Demo: demo(), disks: func(l []disks.Disk) []disks.Disk {
		e := *l[0].Estimate
		e.FSErrors = true
		l[0].Estimate = &e
		return l
	}}
	out := joined(screen(b, 132, 34))
	for _, want := range []string{"once e2fsck has checked", "the filesystem records errors: e2fsck first"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q:\n%s", want, out)
		}
	}
	// Asked to shrink it, the screen says why not, as the run's check would.
	m := playKeys(t, b, []string{"s"})
	if m.mode != modeMain || !strings.Contains(m.toast, "e2fsck") {
		t.Errorf("mode %v, toast %q", m.mode, m.toast)
	}
}

func TestFailureSaysHowTheDistributionWasLeft(t *testing.T) {
	// Past the stop, a failure is followed by the restart step: started
	// again, or, after a disk that did not read as before, left stopped.
	for _, c := range []struct {
		name string
		at   shrink.Step
		then shrink.Event
		want []string
	}{
		{"stop ran out of time", shrink.StepStop, shrink.Event{Step: shrink.StepRestart, Status: shrink.Done, Count: 2},
			[]string{"The disk was not changed, and Ubuntu-24.04 runs as it was."}},
		{"stop, containers left", shrink.StepStop, shrink.Event{Step: shrink.StepRestart, Status: shrink.Warned, Count: 2, NotRestarted: 1},
			[]string{"Ubuntu-24.04 runs; some of its containers did not start again."}},
		{"disk reads wrong", shrink.StepVerify, shrink.Event{Step: shrink.StepRestart, Status: shrink.Skipped, Detail: "left stopped: check the disk first"},
			[]string{"left stopped until the disk is checked", "so Ubuntu-24.04 stays stopped. Do not start it until you know why"}},
		{"disk not read again", shrink.StepVerify, shrink.Event{Step: shrink.StepRestart, Status: shrink.Done},
			[]string{"could not be read again to check it; Ubuntu-24.04 was started again."}},
	} {
		b := &variant{Demo: demo(), failAt: c.at, failWith: "boom", then: []shrink.Event{c.then}}
		out := joined(screen(b, 120, 34, "s", "enter"))
		for _, w := range c.want {
			if !strings.Contains(out, w) {
				t.Errorf("%s: no %q:\n%s", c.name, w, out)
			}
		}
		// Never the words for a distribution in an unknown state, when the
		// engine said how it came back.
		if strings.Contains(out, "may have stopped part way") {
			t.Errorf("%s: says the distribution may be half stopped", c.name)
		}
	}
}

func TestRunOfAFileSaysWindowsCompactsIt(t *testing.T) {
	// Docker Desktop's disk, closed: Windows compacts it, without a trim and
	// with no distribution to stop, and the running step says so.
	b := &variant{Demo: demo(), disks: func(l []disks.Disk) []disks.Disk {
		for i := range l {
			if l[i].Kind == disks.KindDocker {
				l[i].File.Held = false
			}
		}
		return l
	}}
	out := joined(strings.Split(ansi.Strip(Snapshot(b, 120, 30, true, []string{"down", "s", "enter"}, "compact")), "\n"))
	if !strings.Contains(out, "Windows compacts the file: blocks of zeros go") || strings.Contains(out, "trims the filesystem") {
		t.Errorf("the compaction of a file is told as WSL's:\n%s", out)
	}
}

func TestDemoRunLastsAsLongAsItsSteps(t *testing.T) {
	// The README's picture of a run: its total is the steps' own, never
	// shorter than the compaction it shows, with or without the pauses.
	out := joined(screen(demo(), 120, 30, "c", "space", "down", "space", "enter", "enter"))
	if !strings.Contains(out, "Compact 38s") || !strings.Contains(out, "Gave back 81.5 GB to Windows in 44s.") {
		t.Errorf("the run's total does not add up:\n%s", out)
	}
}

func TestRunCutOffIsFinishedFirst(t *testing.T) {
	// The journal says a run of Ubuntu was cut off after stopping it: the
	// screen says so where the counts were, offers R, and holds s back
	// until R has finished it.
	b := &variant{Demo: demo(), unfinishedState: journal.CutOff,
		unfinished: journal.Entry{Disk: "Ubuntu-24.04", Distro: "Ubuntu-24.04", Step: "compact", Stopped: true}}
	out := joined(screen(b, 120, 30))
	if !strings.Contains(out, "The run of Ubuntu-24.04 was cut off at Compact · R finishes it") || !strings.Contains(out, "R finish run") {
		t.Fatalf("no word of the cut-off run:\n%s", out)
	}
	m := playKeys(t, b, []string{"s"})
	if m.mode != modeMain || !strings.Contains(m.toast, "First finish the run that was cut off") {
		t.Fatalf("mode %v, toast %q", m.mode, m.toast)
	}
	// R finishes it: Recover is asked once, its words are said, and the
	// disks are read again.
	m = playKeys(t, b, []string{"R"})
	next, _ := m.Update(recoverMsg{said: "Ubuntu-24.04: running, 2 containers started again"})
	m = next.(model)
	if !strings.Contains(m.toast, "2 containers started again") || !m.loading {
		t.Fatalf("toast %q, loading %v", m.toast, m.loading)
	}
}

func TestRunGoingOnElsewhereHoldsShrinkingBack(t *testing.T) {
	b := &variant{Demo: demo(), unfinishedState: journal.Going, unfinished: journal.Entry{Disk: "Debian"}}
	out := joined(screen(b, 120, 30))
	if !strings.Contains(out, "Debian is being shrunk by another wsl-shrinker") || strings.Contains(out, "R finish run") {
		t.Fatalf("\n%s", out)
	}
	if m := playKeys(t, b, []string{"s"}); m.mode != modeMain || !strings.Contains(m.toast, "finish Debian") {
		t.Fatalf("mode %v, toast %q", m.mode, m.toast)
	}
}

func TestRefusedCheckSpeaksTheScreensLanguage(t *testing.T) {
	// The engine refused at its check with a code: the screen says why in
	// its own language, not in the engine's English.
	i18n.Set(i18n.Korean)
	defer i18n.Set(i18n.English)
	b := &scripted{Demo: demo(), events: []shrink.Event{
		{Step: shrink.StepCheck, Status: shrink.Failed, Detail: "the disk is protected in the settings", Code: string(shrink.BlockProtected)},
	}}
	out := joined(screen(b, 120, 30, "s", "enter"))
	if !strings.Contains(out, "설정에서 보호됩니다") || strings.Contains(out, "is protected in the settings") {
		t.Fatalf("\n%s", out)
	}
}

func TestHelpCanBeReadToTheEnd(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.English, i18n.Korean} {
		i18n.Set(lang)
		seen := map[string]bool{}
		keys := []string{"?"}
		for range 30 {
			for _, l := range screen(demo(), 80, 24, keys...) {
				seen[strings.TrimSpace(l)] = true
			}
			keys = append(keys, "down")
		}
		all := strings.Join(mapKeys(seen), "\n")
		for _, want := range []string{"6.", i18n.T("help.how")} {
			if !strings.Contains(all, want) {
				t.Errorf("%s: %q never shows", lang, want)
			}
		}
		// The legend's last color is the last thing to read.
		if !strings.Contains(joined(mapKeys(seen)), i18n.T("help.legend.bad")) {
			t.Errorf("%s: the legend's end never shows", lang)
		}
	}
	i18n.Set(i18n.English)
}

func mapKeys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestHeaderKeepsTheTotalLongest(t *testing.T) {
	i18n.Set(i18n.Korean)
	defer i18n.Set(i18n.English)
	head := screen(demo(), 60, 16)[0]
	if !strings.Contains(head, "돌려받을") {
		t.Errorf("the total left the header before WSL's version: %q", head)
	}
}

func TestBlockedSaysWhyInTheReasonsColor(t *testing.T) {
	old := &variant{Demo: demo(), info: &wsl.Info{Version: "2.0.14.0"}}
	for _, c := range []struct {
		b    Backend
		keys []string
		want ui.Category
	}{
		{demo(), []string{"down", "space"}, ui.CatLive},                         // Docker Desktop holds its disk
		{demo(), []string{"down", "down", "down", "down", "space"}, ui.CatSafe}, // Work is protected
		{old, []string{"s"}, ui.CatBad},                                         // this WSL cannot compact
	} {
		if m := playKeys(t, c.b, c.keys); m.toastCat != c.want {
			t.Errorf("%v: %q in %v, want %v", c.keys, m.toast, m.toastCat, c.want)
		}
	}
}

func TestConfirmOffersChoosingOnlyWhereThereIsSomething(t *testing.T) {
	empty := &variant{Demo: demo(), inside: &survey.Result{Docker: true, Containers: 1}}
	out := strings.Join(screen(empty, 120, 30, "s"), "\n")
	if strings.Contains(out, "choose inside") {
		t.Errorf("nothing inside, yet c is offered:\n%s", out)
	}
	// Two disks: the box offers no c, and neither does the footer.
	lines := screen(demo(), 120, 30, "space", "down", "space", "s")
	if strings.Contains(strings.Join(lines, "\n"), "choose inside") {
		t.Errorf("several disks, yet c is offered")
	}
	m := playKeys(t, demo(), []string{"space", "down", "space", "s", "c"})
	if m.mode != modeConfirm {
		t.Errorf("c in a box that does not offer it left the box (mode %v)", m.mode)
	}
}

func TestConfirmOfSeveralNamesTheOneItStops(t *testing.T) {
	out := joined(screen(demo(), 120, 30, "space", "down", "space", "s"))
	if !strings.Contains(out, "Stops Ubuntu-24.04") {
		t.Errorf("which distribution stops is not said:\n%s", out)
	}
}

func TestFailedRunListsTheDisksItSkipped(t *testing.T) {
	b := &variant{Demo: demo(), failAt: shrink.StepCompact, failDisk: "Ubuntu-24.04", failWith: "E_FAIL"}
	lines := screen(b, 120, 34, "space", "down", "space", "s", "enter")
	for _, l := range lines {
		if strings.Contains(l, "Debian") && strings.Contains(l, "skipped") {
			return
		}
	}
	t.Errorf("Debian, never run, is not said to be skipped:\n%s", strings.Join(lines, "\n"))
}

func TestWrapKeepsAnAmountWithItsUnit(t *testing.T) {
	m := newModel(context.Background(), demo())
	unit := regexp.MustCompile(`^\s*(B|KB|MB|GB|TB)\b`)
	for w := 16; w <= 70; w++ {
		lines := m.wrapTo("c: choose what to remove before shrinking (67.4 GB found)", w, " ", lipgloss.NewStyle())
		for i := 1; i < len(lines); i++ {
			if unit.MatchString(ansi.Strip(lines[i])) {
				t.Fatalf("width %d: %q", w, lines)
			}
		}
	}
}

func TestNarrowTableKeepsWhatAShrinkFrees(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.English, i18n.Korean} {
		i18n.Set(lang)
		for _, w := range []int{40, 60, 80, 110} {
			lines := screen(demo(), w, 24)
			at, ok := tableRow(lines, "Ubuntu-24.04")
			if !ok {
				t.Fatalf("%s %d: no row", lang, w)
			}
			// The amount is whole, and nothing at the end of the row is cut.
			row := strings.Split(lines[at], "│")[0]
			if !strings.Contains(row, "64.0 GB") || strings.HasSuffix(strings.TrimRight(row, " "), "…") {
				t.Errorf("%s %d: %q", lang, w, row)
			}
		}
	}
	i18n.Set(i18n.English)
}

func TestCleanListSaysItsPromiseWhole(t *testing.T) {
	out := joined(screen(demo(), 60, 16, "c"))
	if !strings.Contains(out, "no other file.") {
		t.Errorf("the promise is cut:\n%s", out)
	}
}

func TestSnapshotKnowsTheNamedKeys(t *testing.T) {
	for name, code := range map[string]rune{"pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown, "home": tea.KeyHome,
		"end": tea.KeyEnd, "tab": tea.KeyTab} {
		if k := snapshotKey(name); k.Code != code {
			t.Errorf("%s is %q", name, k.String())
		}
	}
}

func TestDialogWrapsItsLinesOnce(t *testing.T) {
	m := newModel(context.Background(), demo())
	m.width, m.height = 120, 34
	text := "Stops Ubuntu-24.04 and its 3 running containers, then starts them again."
	box := m.box(ui.CatGain, text)
	// The text wraps once, at the width inside the border and the padding:
	// wrapped twice, a word is left alone on a line.
	inner := lipgloss.Width(strings.Split(box, "\n")[0]) - 2 - 4
	want := len(strings.Split(lipgloss.NewStyle().Width(inner).Render(text), "\n"))
	if got := lipgloss.Height(box) - 2; got != want {
		t.Errorf("%d lines inside, want %d:\n%s", got, want, ansi.Strip(box))
	}
}

func TestReopenPassesEachItemOnItsOwn(t *testing.T) {
	q := []disks.Disk{{Name: "Ubuntu-24.04"}}
	clean := map[string][]survey.Item{"Ubuntu-24.04": {{ID: "apt"}, {ID: "pip:/home/a,b/.cache/pip"}}}
	got := strings.Join(app.HandoffArgs("en", q, clean), " ")
	// An id that holds a comma reaches the new window whole, on its own flag.
	want := "--clean Ubuntu-24.04=apt --clean Ubuntu-24.04=pip:/home/a,b/.cache/pip"
	if !strings.Contains(got, want) {
		t.Errorf("%s", got)
	}
}
