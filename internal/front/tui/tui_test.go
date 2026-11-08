package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/kanggu12/wsl-shrinker/internal/engine/disks"
	"github.com/kanggu12/wsl-shrinker/internal/engine/shrink"
	"github.com/kanggu12/wsl-shrinker/internal/engine/survey"
	"github.com/kanggu12/wsl-shrinker/internal/front/i18n"
)

// demo returns the made-up PC without pauses.
func demo() *Demo {
	d := NewDemo()
	d.Fast = true
	return d
}

// keySets are the screens the tests look at, as the keys that open them.
var keySets = map[string][]string{
	"main":    nil,
	"second":  {"down", "down"},
	"clean":   {"c", "space", "down", "space"},
	"confirm": {"c", "space", "enter"},
	"marked":  {"space", "space", "s"},
	"run":     {"s", "enter"},
	"history": {"h"},
	"help":    {"?"},
}

func TestEveryScreenFitsEveryWidth(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.English, i18n.Korean} {
		i18n.Set(lang)
		for name, keys := range keySets {
			for _, w := range []int{40, 60, 80, 100, 110, 132, 160, 200} {
				for _, h := range []int{12, 24, 40} {
					out := Snapshot(demo(), w, h, true, keys, "")
					lines := strings.Split(out, "\n")
					// Exactly the screen's height, every line exactly its width:
					// nothing wraps, nothing is left over.
					if len(lines) != h {
						t.Fatalf("%s %s %dx%d: %d lines", lang, name, w, h, len(lines))
					}
					for i, l := range lines {
						if got := lipgloss.Width(l); got != w {
							t.Fatalf("%s %s %dx%d: line %d is %d wide: %q", lang, name, w, h, i, got, ansi.Strip(l))
						}
					}
				}
			}
		}
	}
	i18n.Set(i18n.English)
}

func TestMainScreenSaysWhatMatters(t *testing.T) {
	out := ansi.Strip(Snapshot(demo(), 132, 34, true, nil, ""))
	for _, want := range []string{
		"wsl-shrinker", "WSL 3.0.3.0", "87.0 GB can be given back now", "+36.0 GB when not in use", // header: the disks worth shrinking; protected Work is not counted, the docker disk in use apart
		"Ubuntu-24.04", "Docker Desktop data", "in use", // the docker disk and its state
		"Gives back about 64.0 GB.", "after 248 GB", // the selected disk's verdict and bar
		"WSL 3.0.3.0 can compact", "read while running", // its checks
		"Docker build cache", "c: choose what to remove first", // what fills it
		"5 disks · 2 worth shrinking", // the status line: the docker disk in use is not counted
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestProtectedDiskNeverRuns(t *testing.T) {
	// Select Work (protected, last), try to mark it, and ask for a run:
	// nothing starts.
	keys := []string{"down", "down", "down", "down", "space", "s"}
	m := playKeys(t, demo(), keys)
	if m.mode == modeConfirm || m.mode == modeRun {
		t.Fatalf("mode %v for a protected disk", m.mode)
	}
	if m.marked["Work"] {
		t.Fatal("a protected disk was marked")
	}
}

func TestMarkedDisksRunInOrder(t *testing.T) {
	d := demo()
	// Mark Ubuntu and Debian, skip the docker disk.
	m := playKeys(t, d, []string{"space", "down", "space", "s"})
	q := m.queueFor()
	names := []string{}
	for _, x := range q {
		names = append(names, x.Name)
	}
	if !slices.Equal(names, []string{"Ubuntu-24.04", "Debian"}) {
		t.Fatalf("queue %v", names)
	}
	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "Shrink 2 disks?") {
		t.Fatalf("confirm:\n%s", out)
	}
}

func TestRunPlaysToTheEnd(t *testing.T) {
	out := ansi.Strip(Snapshot(demo(), 110, 30, true, []string{"c", "space", "enter", "enter"}, ""))
	for _, want := range []string{"Shrank Ubuntu-24.04", "1 item, about 14.8 GB", "3 containers", "3 containers started again", "Gave back 78.8 GB"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	// With items chosen inside, the run says what the question said: the
	// check's amount and the size expected count what is removed.
	box := ansi.Strip(Snapshot(demo(), 120, 30, true, []string{"c", "space", "down", "space", "enter"}, ""))
	chosen := ansi.Strip(Snapshot(demo(), 120, 30, true, []string{"c", "space", "down", "space", "enter", "enter"}, "compact"))
	if !strings.Contains(box, "→ about 231 GB: about 81.5 GB back") || !strings.Contains(chosen, "about 81.5 GB to give back") ||
		!regexp.MustCompile(`expected\s+▪+\s+231 GB`).MatchString(chosen) {
		t.Fatalf("the question and the run disagree:\n%s\n%s", box, chosen)
	}
	// Mid-run, the compaction shows as running and the later steps wait.
	mid := ansi.Strip(Snapshot(demo(), 110, 30, true, []string{"s", "enter"}, "compact"))
	if !strings.Contains(mid, "Compacting; this cannot be paused") || strings.Contains(mid, "Shrank") {
		t.Fatalf("mid-run:\n%s", mid)
	}
}

func TestCleanChoicesGoToTheRun(t *testing.T) {
	d := &recording{Demo: demo()}
	// Space chooses and moves down a row: the first and the third item of
	// the list, which starts with the caches, largest first.
	m := playKeys(t, d, []string{"c", "space", "down", "space", "enter", "enter"})
	for m.run != nil && !m.run.done {
		next, _ := m.Update(<-m.run.events)
		m = next.(model)
	}
	got := []string{}
	for _, it := range d.cleaned {
		got = append(got, it.ID)
	}
	if !slices.Equal(got, []string{"docker-build-cache", "pip:/home/ann/.cache/pip"}) {
		t.Fatalf("cleaned %v", got)
	}
}

func TestReopenFromInsideTheDistribution(t *testing.T) {
	d := &recording{Demo: demo(), hosts: "Ubuntu-24.04"}
	m := playKeys(t, d, []string{"c", "space", "enter"})
	// The question says so before anything happens.
	if !strings.Contains(ansi.Strip(m.View().Content), "This terminal runs inside WSL") {
		t.Fatal("the confirm box does not say the run moves")
	}
	next, cmd := m.Update(snapshotKey("enter"))
	m = next.(model)
	if m.mode == modeRun || cmd == nil || !m.reopening {
		t.Fatal("ran here instead of moving")
	}
	// The new window opens in the background, and this screen ends once it
	// has said it started.
	msg, ok := cmd().(reopenMsg)
	if !ok || msg.err != nil {
		t.Fatalf("the handoff answered %#v", msg)
	}
	want := []string{"--reopened", "--lang", "en", "--run", "Ubuntu-24.04", "--clean", "Ubuntu-24.04=docker-build-cache"}
	if !slices.Equal(d.reopened, want) {
		t.Fatalf("reopened with %v", d.reopened)
	}
	if _, quit := m.Update(msg); quit == nil {
		t.Fatal("this screen stayed after the new window took the run")
	}
	// Another distribution than the terminal's runs here.
	d = &recording{Demo: demo(), hosts: "Debian"}
	m = playKeys(t, d, []string{"s", "enter"})
	if m.mode != modeRun || d.reopened != nil {
		t.Fatalf("mode %v, reopened %v", m.mode, d.reopened)
	}
}

func TestStartRunFromOptions(t *testing.T) {
	// The window a run moved to starts it at once, cleaning what was chosen;
	// the disk name may come in any case from the command line.
	start := func(clean map[string][]string) (model, *recording) {
		d := &recording{Demo: demo()}
		m := newModel(context.Background(), d)
		m.pending = &Options{Run: []string{"ubuntu-24.04"}, Clean: clean}
		l, _ := d.Disks(m.ctx)
		next, cmd := m.Update(disksMsg{list: l})
		m = next.(model)
		next, _ = m.Update(cmd())
		return next.(model), d
	}
	m, d := start(map[string][]string{"UBUNTU-24.04": {"apt"}})
	if m.mode != modeRun {
		t.Fatalf("mode %v", m.mode)
	}
	for !m.run.done {
		next, _ := m.Update(<-m.run.events)
		m = next.(model)
	}
	if len(d.cleaned) != 1 || d.cleaned[0].ID != "apt" {
		t.Fatalf("cleaned %v", d.cleaned)
	}
	// An id no longer found inside stops it: nothing runs.
	m, d = start(map[string][]string{"Ubuntu-24.04": {"apt", "not-there"}})
	if m.mode == modeRun || d.cleaned != nil || !strings.Contains(m.toast, "not-there") {
		t.Fatalf("mode %v, cleaned %v, toast %q", m.mode, d.cleaned, m.toast)
	}
}

func TestCleanListEnterRunsThatDisk(t *testing.T) {
	// Debian marked first, then items chosen inside Ubuntu: the run takes
	// both, Ubuntu with its items.
	d := &recording{Demo: demo()}
	m := playKeys(t, d, []string{"down", "down", "space", "up", "up", "up", "c", "space", "enter"})
	names := []string{}
	for _, x := range m.queueFor() {
		names = append(names, x.Name)
	}
	if !slices.Equal(names, []string{"Ubuntu-24.04", "Debian"}) {
		t.Fatalf("queue %v", names)
	}
}

func TestChoicesBelongToOneDistribution(t *testing.T) {
	// Two running distributions with the same item ids: choosing in one
	// leaves the other's alone.
	d := &twoRunning{recording{Demo: demo()}}
	// Selecting Debian looks inside it too, so both have the same ids loaded.
	m := playKeys(t, d, []string{"c", "space", "esc", "down", "down", "up", "up"})
	if got := m.chosenItems("Ubuntu-24.04"); len(got) != 1 {
		t.Fatalf("Ubuntu: %v", got)
	}
	if got := m.chosenItems("Debian"); len(got) != 0 {
		t.Fatalf("Debian got Ubuntu's choice: %v", got)
	}
}

func TestBlockedDisksStayOutOfARun(t *testing.T) {
	// An unreadable disk and Docker's disk in use, marked with Ubuntu: the
	// confirm box shows Ubuntu alone and nothing panics.
	d := &unreadable{recording{Demo: demo()}}
	// space on a disk that cannot be marked leaves the cursor there, on the
	// disk the message is about: down moves past it.
	if m := playKeys(t, d, []string{"space", "space"}); func() string { s, _ := m.selected(); return s.Name }() != "Docker Desktop data" {
		t.Fatal("the cursor left the refused disk")
	}
	m := playKeys(t, d, []string{"space", "space", "down", "space", "space", "s"})
	names := []string{}
	for _, x := range m.queueFor() {
		names = append(names, x.Name)
	}
	if !slices.Equal(names, []string{"Ubuntu-24.04", "Debian"}) {
		t.Fatalf("queue %v", names)
	}
	_ = m.View()
	for _, name := range []string{"Docker Desktop data", "kali-linux"} {
		if m.marked[name] {
			t.Fatalf("%s was marked", name)
		}
	}
}

// twoRunning is the demo with Debian running too.
type twoRunning struct{ recording }

func (r *twoRunning) Disks(ctx context.Context) ([]disks.Disk, error) {
	l, err := r.Demo.Disks(ctx)
	for i := range l {
		if l[i].Name == "Debian" {
			l[i].Running = true
		}
	}
	return l, err
}

// unreadable is the demo with kali-linux unreadable.
type unreadable struct{ recording }

func (r *unreadable) Disks(ctx context.Context) ([]disks.Disk, error) {
	l, err := r.Demo.Disks(ctx)
	for i := range l {
		if l[i].Name == "kali-linux" {
			l[i].Estimate, l[i].Err = nil, "boom"
		}
	}
	return l, err
}

func TestCatalogsComplete(t *testing.T) {
	// Every key the code can build is in English, and in Korean unless it
	// is a ".one" form, which Korean does not need; every key of the
	// catalogs is one the code can build. The keys come from the code
	// itself, and the values of a key built at run time from the code's
	// own lists, so neither can be forgotten here.
	cat := catalogKeys(t)
	// The first word of every key: a dotted literal starting with one of
	// them is a key.
	namespaces := map[string]bool{}
	for k := range cat[i18n.English] {
		namespaces[strings.SplitN(k, ".", 2)[0]] = true
	}
	// The source of every package that shows words: the ones that import
	// i18n, under the module's internal and cmd folders.
	u := &codeUse{required: map[string]string{}, optional: map[string]string{}, prefixes: map[string]string{}, opaque: map[string]string{}}
	root := filepath.Join("..", "..", "..")
	scanned := 0
	for _, top := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return err
			}
			// The catalog package's own code looks keys up, it builds none.
			if filepath.Base(filepath.Dir(p)) == "i18n" {
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil || !strings.Contains(string(b), `"github.com/kanggu12/wsl-shrinker/internal/front/i18n"`) {
				return err
			}
			scanned++
			scanCode(t, p, namespaces, u)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	// The screen's files at least: fewer means the walk missed them.
	if scanned < 4 {
		t.Fatalf("scanned %d files", scanned)
	}

	// The steps, as the engine lists them; every Step it declares is in
	// the list, or its words would never be looked for.
	var steps []string
	for _, s := range shrink.Steps {
		steps = append(steps, string(s))
	}
	for _, s := range constsOf(t, filepath.Join(root, "internal", "engine", "shrink", "shrink.go"), "Step") {
		if !slices.Contains(steps, s) {
			t.Errorf("step %q is not in shrink.Steps", s)
		}
	}
	// The kinds of findings the survey declares, and what it can find.
	kinds := constsOf(t, filepath.Join(root, "internal", "engine", "survey", "survey.go"), "Kind")
	var whats []string
	for _, it := range surveyFinds(t) {
		if !slices.Contains(kinds, string(it.Kind)) {
			t.Errorf("the survey finds %s of kind %q, which it does not declare", it.ID, it.Kind)
		}
		// An image is named by its own name; everything else by its words.
		if it.What == "image" {
			if got := itemLabel(it); got != it.Label {
				t.Errorf("an image is named %q, not by its name %q: it needs words of its own", got, it.Label)
			}
			continue
		}
		if !slices.Contains(whats, it.What) {
			whats = append(whats, it.What)
		}
	}
	// The units of Docker's "how long ago", from the pattern since reads.
	unit := regexp.MustCompile(`\(((?:[a-z]+\|)+[a-z]+)\)`).FindStringSubmatch(sinceRe.String())
	if unit == nil {
		t.Fatalf("no units in %s", sinceRe)
	}
	// What each prefix the code builds keys from is completed with.
	// The setup screen's rows and the values each row takes, as it lists them.
	values := map[string][]string{
		"step.": steps, "run.skipped.": steps, "run.about.": steps, "run.state.": steps,
		"kind.": kinds, "group.": kinds, "what.": whats, "since.": strings.Split(unit[1], "|"),
		"setup.row.": setupRows, "setup.language.": setupLanguages, "setup.theme.": setupThemes,
		"setup.mode.": setupModes, "setup.mode.hint.": setupModes,
	}
	for ps, at := range u.prefixes {
		pre, suf, _ := strings.Cut(ps, "\x00")
		vals, ok := values[pre]
		if !ok {
			t.Errorf("%s builds keys from %q: say here which list completes it", at, pre)
		}
		for _, v := range vals {
			u.required[pre+v+suf] = at
		}
	}
	// failureState's endings: every way the engine leaves a distribution
	// has one, and each may follow any step.
	for _, l := range constsOf(t, filepath.Join(root, "internal", "engine", "shrink", "shrink.go"), "Left") {
		if _, ok := stateEnds[shrink.Left(l)]; !ok {
			t.Errorf("failureState has no ending for Left %q", l)
		}
	}
	for _, s := range steps {
		for _, end := range stateEndings() {
			u.optional["run.state."+s+end] = "failureState"
		}
	}
	// A key from a variable is seen only where its values are written:
	// these functions' keys are, as literals or through the table above.
	known := map[string]bool{"failureState": true, "helpLines": true, "tableColumns": true}
	for fn, at := range u.opaque {
		if !known[fn] {
			t.Errorf("%s: %s passes i18n a key the test cannot see; write its values as literals, or derive them here", at, fn)
		}
	}
	for fn := range known {
		if _, ok := u.opaque[fn]; !ok {
			t.Errorf("%s no longer takes its key from a variable: drop it from the known ones", fn)
		}
	}
	if len(u.required) < 100 {
		t.Fatalf("found only %d keys: the scan misses the code", len(u.required))
	}

	// What the code builds is there.
	for k, at := range u.required {
		if !cat[i18n.English][k] {
			t.Errorf("%s: English has no %q", at, k)
		}
		if !cat[i18n.Korean][k] && !strings.HasSuffix(k, ".one") {
			t.Errorf("%s: Korean has no %q", at, k)
		}
	}
	// A key looked for with a fallback is in both languages or neither:
	// in one only, the two would say different things.
	for k, at := range u.optional {
		if cat[i18n.English][k] != cat[i18n.Korean][k] && !strings.HasSuffix(k, ".one") {
			t.Errorf("%s: %q is in one language only", at, k)
		}
	}
	// The two catalogs hold the same keys, but for English's ".one".
	for k := range cat[i18n.English] {
		if !cat[i18n.Korean][k] && !strings.HasSuffix(k, ".one") {
			t.Errorf("Korean has no %q", k)
		}
	}
	for k := range cat[i18n.Korean] {
		if !cat[i18n.English][k] {
			t.Errorf("English has no %q, which Korean has", k)
		}
	}
	// And nothing in the catalogs is left that the code cannot build.
	var unused []string
	for k := range cat[i18n.English] {
		if u.required[k] == "" && u.optional[k] == "" {
			unused = append(unused, k)
		}
	}
	slices.Sort(unused)
	for _, k := range unused {
		t.Errorf("no code builds %q", k)
	}
}

// playKeys loads the screen and presses keys.
func playKeys(t *testing.T, b Backend, keys []string) model {
	t.Helper()
	m := newModel(context.Background(), b)
	m.width, m.height = 120, 34
	// WSL's version, as Init loads it: it decides what can be compacted.
	m.info, m.infoKnown = b.Info(m.ctx), true
	next, _ := m.Update(m.loadDisks()())
	m = next.(model).surveyNow()
	for _, k := range keys {
		var cmd tea.Cmd
		next, cmd = m.Update(snapshotKey(k))
		_ = cmd
		m = next.(model).surveyNow()
	}
	return m
}

// recording is the demo that remembers what a run was asked to clean and
// how the program reopened itself.
type recording struct {
	*Demo
	hosts    string
	cleaned  []survey.Item
	reopened []string
}

func (r *recording) Shrink(ctx context.Context, d disks.Disk, opt shrink.Options, emit func(shrink.Event)) shrink.Result {
	r.cleaned = append(r.cleaned, opt.Clean...)
	return r.Demo.Shrink(ctx, d, opt, emit)
}

func (r *recording) Hosts(distro string) bool { return distro == r.hosts }

// buggy is the demo whose run hits a bug outside the engine's recovery.
type buggy struct{ *Demo }

func (buggy) Shrink(context.Context, disks.Disk, shrink.Options, func(shrink.Event)) shrink.Result {
	panic("a bug past the engine")
}

func TestABugInARunEndsTheScreenAndSaysWhere(t *testing.T) {
	// The run's goroutine panics: the screen ends as it would on q, and Run
	// hands over what it was and where, for the program to say.
	m := playKeys(t, buggy{demo()}, []string{"s", "enter"})
	if m.run == nil {
		t.Fatal("no run started")
	}
	var msg tea.Msg
	select {
	case msg = <-m.run.events:
	case <-time.After(10 * time.Second):
		t.Fatal("the run's goroutine said nothing")
	}
	next, cmd := m.Update(msg)
	if cmd == nil {
		t.Fatalf("the screen goes on after %T", msg)
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("not a quit: %T", cmd())
	}
	var bug *BugError
	if err := ended(next, nil); !errors.As(err, &bug) || fmt.Sprint(bug.Value) != "a bug past the engine" || !strings.Contains(string(bug.Stack), "Shrink") {
		t.Fatalf("%v", err)
	}
}

func (r *recording) Reopen(args []string) error { r.reopened = args; return nil }

func TestSinceInKorean(t *testing.T) {
	i18n.Set(i18n.Korean)
	defer i18n.Set(i18n.English)
	for in, want := range map[string]string{"5 weeks ago": "5주 전", "About an hour ago": "1시간 전", "17 months ago": "17개월 전", "2 years ago": "2년 전", "soon": "soon"} {
		if got := since(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
	// English keeps Docker's own words.
	i18n.Set(i18n.English)
	if got := since("5 weeks ago"); got != "5 weeks ago" {
		t.Errorf("English: %q", got)
	}
}

// failingRun fails every run at its compaction, as WSL can, and then does
// what the engine does: a distribution that ran is started again.
type failingRun struct{ recording }

func (f *failingRun) Shrink(_ context.Context, d disks.Disk, _ shrink.Options, emit func(shrink.Event)) shrink.Result {
	for _, s := range []shrink.Step{shrink.StepCheck, shrink.StepClean, shrink.StepStop} {
		emit(shrink.Event{Step: s, Status: shrink.Done})
	}
	res := shrink.Result{Disk: d.Name, Err: "E_ACCESSDENIED", Events: []shrink.Event{{Step: shrink.StepCompact, Status: shrink.Failed, Detail: "E_ACCESSDENIED"}}}
	if d.Running && d.Distro != "" {
		res.Events = append(res.Events, shrink.Event{Step: shrink.StepRestart, Status: shrink.Done})
	}
	for _, e := range res.Events {
		emit(e)
	}
	return settledLike(d, res)
}

func TestFailedRunSaysWhereThingsStand(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.English, i18n.Korean} {
		i18n.Set(lang)
		// The running Ubuntu, then the stopped Debian: started again, or not.
		for _, c := range []struct {
			keys []string
			want map[i18n.Lang]string
		}{
			{[]string{"s", "enter"}, map[i18n.Lang]string{i18n.English: "started again as it was", i18n.Korean: "원래대로 다시 켰습니다"}},
			{[]string{"down", "down", "s", "enter"}, map[i18n.Lang]string{i18n.English: "The disk was not changed.", i18n.Korean: "디스크는 그대로입니다."}},
		} {
			out := ansi.Strip(Snapshot(&failingRun{recording{Demo: demo()}}, 120, 30, true, c.keys, ""))
			if !strings.Contains(out, c.want[lang]) || strings.Contains(out, "%!") || strings.Contains(out, "Shrank") {
				t.Errorf("%s %v:\n%s", lang, c.keys, out)
			}
		}
	}
	i18n.Set(i18n.English)
}

func TestNoHeadingWithoutItsLines(t *testing.T) {
	// In short windows a section of the details shows whole or not at all.
	for _, h := range []int{14, 18, 20, 24, 28} {
		for _, w := range []int{60, 80, 100} {
			lines := strings.Split(ansi.Strip(Snapshot(demo(), w, h, true, nil, "")), "\n")
			for i, l := range lines[:len(lines)-2] {
				head := strings.TrimSpace(l)
				if head == "Space" || head == "Checks" || head == "Inside" {
					if i+1 >= len(lines)-2 || strings.TrimSpace(lines[i+1]) == "" {
						t.Fatalf("%dx%d: %q has nothing under it", w, h, head)
					}
				}
			}
		}
	}
}
