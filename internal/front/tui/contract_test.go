package tui

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kanggu12/wsl-shrinker/internal/core/units"
	"github.com/kanggu12/wsl-shrinker/internal/engine/disks"
	"github.com/kanggu12/wsl-shrinker/internal/engine/shrink"
	"github.com/kanggu12/wsl-shrinker/internal/engine/survey"
	"github.com/kanggu12/wsl-shrinker/internal/front/i18n"
	"github.com/kanggu12/wsl-shrinker/internal/machine/winapi"
	"github.com/kanggu12/wsl-shrinker/internal/machine/wsl"
	"github.com/kanggu12/wsl-shrinker/internal/testutil/vhdxtest"
)

// The screen's other run tests stand in for the engine by hand (variant,
// scripted, failingRun, settledLike). The tests here put the real engine
// behind the screen instead: disks.Find and shrink.Run over a fake WSL and
// a small VHDX, so a change in what the engine emits or how it fills its
// Result shows as a screen that says the wrong thing.

// contractPad is the space the host file holds beyond its blocks, which a
// compaction gives back: what a run that goes through frees.
const contractPad = int64(30 << 20)

// engineWorld is one distribution on a VHDX file in a temporary folder, a
// fake WSL that answers for it, and the engine's Env over both, as the
// engine's own tests build it (newWorld in shrink_test.go).
type engineWorld struct {
	// path is the VHDX file of the distribution.
	path string
	// fake is the WSL the engine talks to.
	fake *wsl.Fake
	// env is what shrink.Run and disks.Find are given.
	env shrink.Env
	// pad is how many bytes the file is larger than its blocks.
	pad int64
}

// newEngineWorld sets up a running distribution "Dev" with two containers
// and a pre-stop hook, whose file a compaction makes contractPad smaller.
func newEngineWorld(t *testing.T) *engineWorld {
	t.Helper()
	// The file lives in the test's own folder: nothing outside it is read.
	w := &engineWorld{path: filepath.Join(t.TempDir(), "ext4.vhdx"), pad: contractPad}
	// Three stored blocks: enough for the reader to find a disk to compare.
	vhdxtest.Write(t, w.path, vhdxtest.Spec{VirtualSize: 16 << 20, Blocks: map[int64][]byte{0: {1}, 2: {1}, 5: {1}}})
	// WSL knows one WSL 2 distribution, running, on that file.
	w.fake = &wsl.Fake{List: []wsl.Distro{{Name: "Dev", VHD: w.path, Version: 2, Running: true}}}
	// The scripts the engine runs inside answer as a healthy distribution.
	w.fake.Script = func(_, _, s string) (string, error) {
		switch {
		case strings.Contains(s, "docker ps -q"):
			// The containers the stop step names before it stops them.
			return "running|0123456789ab fedcba987654 \n", nil
		case strings.Contains(s, "pre-stop.d"):
			// The stop script: one hook ran.
			return "hook|ok|/etc/wsl-shrinker/pre-stop.d/save\n", nil
		case strings.Contains(s, "/proc/mounts"):
			// The root comes back writable after a start.
			return "rw,relatime,discard\n", nil
		case strings.Contains(s, "docker start"):
			// Both containers start again, one line each.
			return "started|0123456789ab\nstarted|fedcba987654\n", nil
		}
		// Anything else (the survey, the clean) has nothing to say.
		return "", nil
	}
	// WSL's compaction drops the padding, as Windows would.
	w.fake.OnCompact = func(string) error { w.pad = 0; return nil }
	w.env = shrink.Env{
		Disks: disks.Env{
			WSL: w.fake,
			// The file's size on disk is its blocks plus the padding left.
			Stat: func(p string) (winapi.FileInfo, error) {
				fi, err := winapi.Stat(p)
				fi.OnDisk += w.pad
				return fi, err
			},
			// The disk is read from the real temporary file.
			Open: disks.OpenFile,
		},
		// A file WSL does not register would be compacted here; Dev is not one.
		CompactFile: func(string) error { w.pad = 0; return nil },
		// This WSL can compact a distribution.
		CanCompact: true,
		// The engine's waits between checks take no time in a test.
		Sleep: func(time.Duration) {},
	}
	return w
}

// engineBreaks are the ways a run goes wrong, set up on the world as the
// engine's matrix test does (breaks in matrix_test.go).
var engineBreaks = map[string]func(w *engineWorld){
	// Nothing goes wrong.
	"none": func(*engineWorld) {},
	// The compaction fails before WSL stopped the distribution.
	"compact-early": func(w *engineWorld) {
		w.fake.Fail = map[string]error{"compact:Dev": errors.New("E_ACCESSDENIED")}
	},
	// The compaction fails once WSL has stopped the distribution: the fake
	// Compact stops it, then OnCompact fails.
	"compact-late": func(w *engineWorld) {
		w.fake.OnCompact = func(string) error { return errors.New("compaction failed") }
	},
	// The file cannot be opened after the compaction: nothing says the disk
	// is wrong, only that it was not read again.
	"verify-open": func(w *engineWorld) {
		compacted := false
		w.fake.OnCompact = func(string) error { compacted, w.pad = true, 0; return nil }
		open := w.env.Disks.Open
		w.env.Disks.Open = func(p string) (disks.ReaderAtCloser, int64, error) {
			// Before the compaction the disk reads as usual.
			if compacted {
				return nil, 0, errors.New("the process cannot access the file")
			}
			return open(p)
		}
	},
	// The file no longer reads as a VHDX after the compaction.
	"verify-suspect": func(w *engineWorld) {
		w.fake.OnCompact = func(string) error {
			f, err := os.OpenFile(w.path, os.O_RDWR, 0)
			if err != nil {
				return err
			}
			defer f.Close()
			w.pad = 0
			// Its file signature is overwritten: the reader no longer knows it.
			_, err = f.WriteAt([]byte("XXXXXXXX"), 0)
			return err
		}
	},
	// The distribution does not start again after a good compaction.
	"restart": func(w *engineWorld) {
		w.fake.Fail = map[string]error{"start:Dev": errors.New("WSL_E_VM_MODE_INVALID_STATE")}
	},
}

// engineBackend is the made-up PC with its disks and runs replaced by the
// real engine over an engineWorld; the rest (WSL's version, the survey,
// the history) stays the demo's.
type engineBackend struct {
	*Demo
	w *engineWorld
}

// Disks lists the world's disks as the program does.
func (b *engineBackend) Disks(ctx context.Context) ([]disks.Disk, error) {
	return disks.Find(ctx, b.w.env.Disks)
}

// Shrink runs the engine itself on the disk.
func (b *engineBackend) Shrink(ctx context.Context, d disks.Disk, opt shrink.Options, emit func(shrink.Event)) shrink.Result {
	return shrink.Run(ctx, b.w.env, d, opt, emit)
}

// MinGain is nothing: the world's disk gives back a few MB, which the demo's
// 1 GB would count as not worth it.
func (b *engineBackend) MinGain() int64 { return 0 }

// stepRowRe matches the row of step s on a run screen: three spaces, the
// mark, two spaces, the step's name, then a space or the line's end.
func stepRowRe(s shrink.Step) *regexp.Regexp {
	return regexp.MustCompile(`^ {3}(\S) {2}` + regexp.QuoteMeta(i18n.T("step."+string(s))) + `( |$)`)
}

// stepMarks reads the mark in front of each step's row of a run screen:
// ✓ done, ✗ failed, – skipped, ! warned, · not reached; none for no row.
func stepMarks(lines []string) map[shrink.Step]string {
	out := map[shrink.Step]string{}
	for _, s := range shrink.Steps {
		// The first line that is the step's row holds its mark.
		if row := stepRow(lines, s); row != "" {
			out[s] = stepRowRe(s).FindStringSubmatch(row)[1]
		}
	}
	return out
}

// stepRow is the row of step s on a run screen, "" when there is none.
func stepRow(lines []string, s shrink.Step) string {
	re := stepRowRe(s)
	for _, l := range lines {
		// A step has one row: the first match is it.
		if re.MatchString(l) {
			return l
		}
	}
	return ""
}

// said is a catalog sentence about the distribution "Dev", with its spaces
// run together as joined runs a screen's together.
func said(key string) string {
	return strings.Join(strings.Fields(fmt.Sprintf(i18n.T(key), "Dev")), " ")
}

func TestScreenSaysWhatTheEngineDid(t *testing.T) {
	// The marks of the six steps, in the order of shrink.Steps: check,
	// clean, stop, compact, verify, start again. Nothing is chosen to
	// clean, so that step is always skipped.
	for _, c := range []struct {
		// name is the break set up on the world.
		name string
		// title is the catalog key of the run's title.
		title string
		// marks are the step marks, in the order of shrink.Steps.
		marks string
		// outcome is the sentence under the bars, from the catalog.
		outcome string
		// rows are words a step's row must hold.
		rows map[shrink.Step]string
	}{
		// Every step goes through: the containers stopped and started again,
		// and the padding comes back.
		{name: "none", title: "run.title.done", marks: "✓–✓✓✓✓",
			rows: map[shrink.Step]string{shrink.StepRestart: i18n.TN("detail.started", 2, 2)}},
		// WSL stopped Dev, then failed to compact: the disk is as it was and
		// the engine started Dev again.
		{name: "compact-late", title: "run.title.failed", marks: "✓–✓✗·✓", outcome: "run.state.compact.running"},
		// WSL refused the compaction before stopping Dev: the same, as the
		// engine settles both alike.
		{name: "compact-early", title: "run.title.failed", marks: "✓–✓✗·✓", outcome: "run.state.compact.running"},
		// Compacted, then the file no longer reads as before: Dev is left
		// stopped on purpose.
		{name: "verify-suspect", title: "run.title.problem", marks: "✓–✓✓✗–", outcome: "run.state.verify.suspect",
			rows: map[shrink.Step]string{shrink.StepRestart: i18n.T("run.skipped.restart.left")}},
		// Compacted, then the file could not be opened to check it: Dev is
		// started again all the same.
		{name: "verify-open", title: "run.title.problem", marks: "✓–✓✓✗✓", outcome: "run.state.verify.running"},
		// Compacted and checked, then Dev does not start again.
		{name: "restart", title: "run.title.problem", marks: "✓–✓✓✓✗", outcome: "run.state.restart"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newEngineWorld(t)
			engineBreaks[c.name](w)
			b := &engineBackend{Demo: demo(), w: w}
			// Dev is the only disk, so it is selected: s asks, enter runs.
			lines := screen(b, 120, 34, "s", "enter")
			out := joined(lines)
			// The title names the disk with the words for how the run ended.
			if want := fmt.Sprintf(i18n.T(c.title), "Dev"); !strings.Contains(out, want) {
				t.Errorf("no title %q:\n%s", want, strings.Join(lines, "\n"))
			}
			// The title of the other endings is not there: a failed run never
			// says it shrank, and a compacted one never that it did not.
			for _, other := range []string{"run.title.done", "run.title.failed", "run.title.problem"} {
				if other != c.title && strings.Contains(out, fmt.Sprintf(i18n.T(other), "Dev")) {
					t.Errorf("says %q", fmt.Sprintf(i18n.T(other), "Dev"))
				}
			}
			// Each step's mark, as the engine's events left it.
			marks := stepMarks(lines)
			want := []rune(c.marks)
			for i, s := range shrink.Steps {
				if marks[s] != string(want[i]) {
					t.Errorf("%s is marked %q, want %q:\n%s", s, marks[s], string(want[i]), strings.Join(lines, "\n"))
				}
			}
			// What a step's row says, where the case cares.
			for s, words := range c.rows {
				if row := stepRow(lines, s); !strings.Contains(row, words) {
					t.Errorf("%s row %q, want %q", s, row, words)
				}
			}
			if c.outcome == "" {
				// Through: what it gave back, the padding, before the time it took.
				gave, _, _ := strings.Cut(fmt.Sprintf(i18n.T("run.freed"), units.Bytes(contractPad), "\x00"), "\x00")
				if !strings.Contains(out, strings.Join(strings.Fields(gave), " ")) {
					t.Errorf("no %q:\n%s", gave, out)
				}
				return
			}
			// Failed: where things stand, in the catalog's own sentence.
			if !strings.Contains(out, said(c.outcome)) {
				t.Errorf("no %q:\n%s", said(c.outcome), out)
			}
			// And no other ending of a failure: each says something else.
			for _, other := range failureSentences() {
				if other != c.outcome && said(other) != said(c.outcome) && strings.Contains(out, said(other)) {
					t.Errorf("also says %q (%s)", said(other), other)
				}
			}
		})
	}
}

// failureSentences are the catalog keys failureState can say, for every
// step and every ending it adds, that the English catalog holds.
func failureSentences() []string {
	var out []string
	for _, s := range shrink.Steps {
		base := "run.state." + string(s)
		out = append(out, base)
		for _, end := range stateEndings() {
			if i18n.Has(i18n.English, base+end) {
				out = append(out, base+end)
			}
		}
	}
	return out
}

// stateEndings are the endings failureState can add to a step's key, once
// each: its table's, and the one for containers left stopped.
func stateEndings() []string {
	seen := map[string]bool{}
	out := []string{stateWarned}
	seen[stateWarned] = true
	for _, end := range stateEnds {
		// LeftStopped and LeftFile share an ending; it counts once.
		if !seen[end] {
			seen[end] = true
			out = append(out, end)
		}
	}
	return out
}

// The catalogs and the code: every key the code can build is in both
// languages, and every key in them is one the code can build. The helpers
// below read the code and the catalog as Go source, so a key or a value
// added there is seen here without anyone listing it.

// keyShape is how a catalog key looks: dotted lower-case words.
var keyShape = regexp.MustCompile(`^[a-z]+(\.[a-z0-9-]+)+$`)

// prefixShape is the start of a key the code completes at run time.
var prefixShape = regexp.MustCompile(`^[a-z]+(\.[a-z0-9-]+)*\.$`)

// parseGo parses one Go file of the module, by its path from this folder.
func parseGo(t *testing.T, path string) (*token.FileSet, *ast.File) {
	t.Helper()
	fs := token.NewFileSet()
	f, err := parser.ParseFile(fs, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return fs, f
}

// catalogKeys reads the keys of the English and Korean catalogs from
// their source, as i18n holds no list of them.
func catalogKeys(t *testing.T) map[i18n.Lang]map[string]bool {
	t.Helper()
	_, f := parseGo(t, filepath.Join("..", "i18n", "catalog.go"))
	// The variable each language's map is written in.
	names := map[string]i18n.Lang{"en": i18n.English, "ko": i18n.Korean}
	out := map[i18n.Lang]map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 {
			return true
		}
		lang, ok := names[vs.Names[0].Name]
		lit, isLit := vs.Values[0].(*ast.CompositeLit)
		if !ok || !isLit {
			return true
		}
		out[lang] = map[string]bool{}
		for _, el := range lit.Elts {
			// Each entry is "key": "words".
			if kv, ok := el.(*ast.KeyValueExpr); ok {
				out[lang][unquote(t, kv.Key)] = true
			}
		}
		return false
	})
	// Each key read must be one i18n answers for: the parse read the
	// catalog the program uses.
	for lang, keys := range out {
		for k := range keys {
			if !i18n.Has(lang, k) {
				t.Fatalf("read %q from the %s catalog's source, but i18n has no such key", k, lang)
			}
		}
	}
	if len(out[i18n.English]) < 100 || len(out[i18n.Korean]) < 100 {
		t.Fatalf("read %d English and %d Korean keys: the catalog's source changed shape", len(out[i18n.English]), len(out[i18n.Korean]))
	}
	return out
}

// unquote is the value of a string literal, "" for anything else.
func unquote(t *testing.T, e ast.Expr) string {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		t.Fatalf("unquote %s: %v", lit.Value, err)
	}
	return s
}

// constsOf lists the values of the string constants of a type declared in
// one file of the engine: the enumerations the screen has words for.
func constsOf(t *testing.T, path, typ string) []string {
	t.Helper()
	_, f := parseGo(t, path)
	var out []string
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, s := range gd.Specs {
			// Only constants written with the type: Name Type = "value".
			vs := s.(*ast.ValueSpec)
			if id, ok := vs.Type.(*ast.Ident); ok && id.Name == typ {
				for _, v := range vs.Values {
					out = append(out, unquote(t, v))
				}
			}
		}
	}
	if len(out) == 0 {
		t.Fatalf("no constant of type %s in %s", typ, path)
	}
	return out
}

// surveyFinds runs the real survey over a fake WSL that reports one of
// everything the survey's own script looks for, and returns what it found:
// every kind of finding the clean list can name.
func surveyFinds(t *testing.T) []survey.Item {
	t.Helper()
	// The script's lines for caches and system folders, read from the
	// script itself: a cache added to the survey is answered here too.
	homeRe := regexp.MustCompile(`p="\$h/([^"]+)";.*?home\|([^|]+)\|`)
	sysRe := regexp.MustCompile(`echo "sys\|([^|]+)\|([^|]+)\|`)
	var lines []string
	fake := &wsl.Fake{List: []wsl.Distro{{Name: "Dev", Version: 2, Running: true}}}
	fake.Script = func(_, _, script string) (string, error) {
		for _, m := range homeRe.FindAllStringSubmatch(script, -1) {
			// A cache of the user ann, 1 GiB in KiB.
			lines = append(lines, "home|"+m[2]+"|/home/ann/"+m[1]+"|1048576")
		}
		for _, m := range sysRe.FindAllStringSubmatch(script, -1) {
			// A system folder of 1 GiB, more than the journal keeps.
			lines = append(lines, "sys|"+m[1]+"|"+m[2]+"|1048576")
		}
		// Docker answers: a build cache, a named image and an unnamed one
		// that prune would remove.
		lines = append(lines, "docker|on", "containers|0", "df|Build Cache|1GB",
			"img|sha256:aaaaaaaaaaaa0000|node:22|1GB|5 weeks ago|0",
			"img|sha256:bbbbbbbbbbbb0000|<none>:<none>|1GB|2 days ago|0",
			"dangling|sha256:bbbbbbbbbbbb0000")
		return strings.Join(lines, "\n") + "\n", nil
	}
	r, err := survey.Run(context.Background(), fake, "Dev")
	if err != nil {
		t.Fatalf("survey: %v", err)
	}
	// Every line given that names something to remove became one item:
	// the caches and folders, the build cache, the named image, and the
	// unnamed image with its dangling line as one; "docker|on" and
	// "containers|0" name nothing. Fewer means the survey reads another
	// format now.
	if want := len(lines) - 3; len(r.Items) != want || want < 10 {
		t.Fatalf("the survey found %d items from %d lines that name one:\n%s", len(r.Items), want, strings.Join(lines, "\n"))
	}
	return r.Items
}

// codeUse is what the code can ask the catalogs for, each key with where
// it is asked.
type codeUse struct {
	// required keys must be in English, and in Korean unless ".one".
	required map[string]string
	// optional keys are looked for with a fallback: either language may
	// lack one, but not only one of them.
	optional map[string]string
	// prefixes are keys completed at run time, as "prefix\x00suffix".
	prefixes map[string]string
	// opaque are i18n calls whose key comes from elsewhere, by the
	// function they are in.
	opaque map[string]string
}

// scanCode reads one source file for the keys it can build: key literals,
// keys built from a prefix, keys heldKey completes, and the ".one" forms
// TN looks for. namespaces are the first words of the catalog's keys,
// which tell a key from any other dotted string.
func scanCode(t *testing.T, path string, namespaces map[string]bool, u *codeUse) {
	t.Helper()
	fs, f := parseGo(t, path)
	// isKey reports whether a literal names a catalog key, by its shape.
	isKey := func(s string) bool {
		return keyShape.MatchString(s) && namespaces[strings.SplitN(s, ".", 2)[0]]
	}
	for _, d := range f.Decls {
		// The function a call is in names an opaque key's source.
		fn := ""
		if fd, ok := d.(*ast.FuncDecl); ok {
			fn = fd.Name.Name
		}
		ast.Inspect(d, func(n ast.Node) bool {
			// Inspect ends every node with a nil.
			if n == nil {
				return false
			}
			at := fmt.Sprintf("%s:%d", filepath.Base(path), fs.Position(n.Pos()).Line)
			switch n := n.(type) {
			case *ast.BasicLit:
				// Any key written out, wherever it is: in a call, a list of
				// keys passed on later, or an argument of heldKey.
				if s := unquote(t, n); isKey(s) {
					u.required[s] = at
				}
			case *ast.BinaryExpr:
				// "prefix." + value, maybe + ".suffix": a key built at run
				// time, as the argument of i18n or kept for it, as
				// failureState's base is.
				pre, suf, ok := keyParts(t, n)
				if !ok || !namespaces[strings.SplitN(pre, ".", 2)[0]] {
					return true
				}
				u.prefixes[pre+"\x00"+suf] = at
				// Its parts are not keys of their own.
				return false
			case *ast.CallExpr:
				sel, ok := n.Fun.(*ast.SelectorExpr)
				if id, isID := n.Fun.(*ast.Ident); isID && id.Name == "heldKey" && len(n.Args) == 2 {
					// heldKey completes its key by the disk's kind: every kind.
					for _, k := range constsOf(t, filepath.Join("..", "..", "engine", "disks", "disks.go"), "Kind") {
						u.required[heldKey(disks.Disk{Kind: disks.Kind(k)}, unquote(t, n.Args[1]))] = at
					}
				}
				if !ok || len(n.Args) == 0 {
					return true
				}
				if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "i18n" || sel.Sel.Name != "T" && sel.Sel.Name != "TN" {
					return true
				}
				switch a := n.Args[0].(type) {
				case *ast.BasicLit:
					// A key given to i18n is one whatever its shape: "small" too.
					u.required[unquote(t, a)] = at
					// TN looks for a ".one" form of its key first.
					if sel.Sel.Name == "TN" {
						u.optional[unquote(t, a)+".one"] = at
					}
				case *ast.BinaryExpr:
					// A key built from a prefix, whatever its first word: a
					// prefix the catalog has never had words for is still one.
					if pre, suf, ok := keyParts(t, a); ok {
						u.prefixes[pre+"\x00"+suf] = at
					} else {
						u.opaque[fn] = at
					}
				case *ast.CallExpr:
					// heldKey's keys are seen at its call.
					if id, ok := a.Fun.(*ast.Ident); !ok || id.Name != "heldKey" {
						u.opaque[fn] = at
					}
				default:
					// A key from a variable: where it comes from is known by
					// the function only.
					u.opaque[fn] = at
				}
			}
			return true
		})
	}
}

// keyParts reads a key built by adding strings: its literal prefix, which
// ends in a dot, and a literal suffix that starts with one, if any.
func keyParts(t *testing.T, e *ast.BinaryExpr) (pre, suf string, ok bool) {
	if e.Op != token.ADD {
		return "", "", false
	}
	// The added parts, left to right.
	var parts []ast.Expr
	var flat func(e ast.Expr)
	flat = func(e ast.Expr) {
		if b, ok := e.(*ast.BinaryExpr); ok && b.Op == token.ADD {
			flat(b.X)
			flat(b.Y)
			return
		}
		parts = append(parts, e)
	}
	flat(e)
	pre = unquote(t, parts[0])
	if !prefixShape.MatchString(pre) {
		return "", "", false
	}
	// A literal at the end that starts with a dot is the key's end.
	if len(parts) > 2 {
		if s := unquote(t, parts[len(parts)-1]); strings.HasPrefix(s, ".") {
			suf = s
		}
	}
	return pre, suf, true
}
