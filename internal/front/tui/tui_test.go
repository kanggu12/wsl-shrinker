package tui

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

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
		"wsl-shrinker", "WSL 3.0.3.0", "87.3 GB can be given back now", "+36.0 GB in use", // header: protected Work is not counted, the docker disk in use apart
		"Ubuntu-24.04", "Docker Desktop data", "in use", // the docker disk and its state
		"Gives back about 64.0 GB.", "after 248 GB", // the selected disk's verdict and bar
		"WSL 3.0.3.0 can compact", "read while running", // its checks
		"Docker build cache", "c: choose what to remove before shrinking", // what fills it
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
	// Mid-run, the compaction shows as running and the later steps wait.
	mid := ansi.Strip(Snapshot(demo(), 110, 30, true, []string{"s", "enter"}, "compact"))
	if !strings.Contains(mid, "Windows compacts the file") || strings.Contains(mid, "Shrank") {
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
	if m.mode == modeRun || cmd == nil {
		t.Fatal("ran here instead of moving")
	}
	want := []string{"--reopened", "--lang", "en", "--run", "Ubuntu-24.04", "--clean", "Ubuntu-24.04=docker-build-cache"}
	if !slices.Equal(d.reopened, want) {
		t.Fatalf("reopened with %v", d.reopened)
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
	m := playKeys(t, d, []string{"space", "space", "space", "space", "s"})
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
	// Every key the code uses is in English, and in Korean unless it is a
	// ".one" form, which Korean does not need.
	used := map[string]bool{}
	re := regexp.MustCompile(`i18n\.TN?\("([a-z.]+)"[,)]`)
	dyn := regexp.MustCompile(`i18n\.TN?\("([a-z.]+)"\s*\+`)
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		b, _ := os.ReadFile(f)
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			used[m[1]] = true
		}
		for _, m := range dyn.FindAllStringSubmatch(string(b), -1) {
			// Keys built from a step, a kind or a finding: check each value.
			switch m[1] {
			case "step.", "run.skipped.", "run.about.", "run.state.":
				used["run.state.compact.stopped"] = true
				for _, s := range shrink.Steps {
					used[m[1]+string(s)] = true
				}
			case "what.":
				for _, w := range []string{"pip", "npm", "yarn", "go-build", "go-mod", "cargo", "huggingface", "playwright",
					"trash", "apt", "journal", "docker-build-cache", "docker-dangling"} {
					used[m[1]+w] = true
				}
			case "since.":
				for _, u := range []string{"second", "minute", "hour", "day", "week", "month", "year"} {
					used[m[1]+u] = true
				}
			case "kind.", "group.":
				for _, k := range []survey.Kind{survey.KindCache, survey.KindDownload, survey.KindLog, survey.KindImage} {
					used[m[1]+string(k)] = true
					if m[1] == "group." {
						used[m[1]+string(k)+".cost"] = true
					}
				}
			}
		}
	}
	if len(used) < 100 {
		t.Fatalf("found only %d keys: the pattern misses the code", len(used))
	}
	for k := range used {
		if !i18n.Has(i18n.English, k) {
			t.Errorf("English has no %q", k)
		}
		if !i18n.Has(i18n.Korean, k) {
			t.Errorf("Korean has no %q", k)
		}
	}
}

// playKeys loads the screen and presses keys.
func playKeys(t *testing.T, b Backend, keys []string) model {
	t.Helper()
	m := newModel(context.Background(), b)
	m.width, m.height = 120, 34
	l, err := b.Disks(m.ctx)
	next, _ := m.Update(disksMsg{list: l, err: err})
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

// failingRun fails every run at its compaction, as WSL can.
type failingRun struct{ recording }

func (f *failingRun) Shrink(_ context.Context, d disks.Disk, _ shrink.Options, emit func(shrink.Event)) shrink.Result {
	for _, s := range []shrink.Step{shrink.StepCheck, shrink.StepClean, shrink.StepStop} {
		emit(shrink.Event{Step: s, Status: shrink.Done})
	}
	emit(shrink.Event{Step: shrink.StepCompact, Status: shrink.Failed, Detail: "E_ACCESSDENIED"})
	return shrink.Result{Disk: d.Name, Err: "E_ACCESSDENIED"}
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
