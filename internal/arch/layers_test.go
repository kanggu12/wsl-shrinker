// Package arch_test checks the layering .github/CONTRIBUTING.md describes:
// the packages under internal/ sit in layers, and an import goes down them,
// never up. It reads the source with go/parser rather than `go list`, so it
// also runs as a Windows test binary, where no Go toolchain is installed.
package arch_test

import (
	"bufio"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The layers, highest first, each with the folders (relative to the module
// root) whose packages belong to it. A package may import one in its own
// layer or any lower one: front may use machine directly, and engine may
// use settings, as the table's order allows (no engine package does yet).
// Nothing may import upward, and settings may import machine, not the
// reverse. Placed outside CONTRIBUTING's table:
//   - cmd/ is the program's entry point, above front: nothing imports it.
//   - internal/version holds the build's version string and imports
//     nothing, so it sits with core, where every layer may reach it.
//   - internal/testutil/ serves tests only (see testOnly).
var layers = []struct {
	// name is the layer's name in CONTRIBUTING.md.
	name string
	// dirs are the folders of its packages; a folder covers its subfolders.
	dirs []string
}{
	{"cmd", []string{"cmd"}},
	{"front", []string{"internal/front"}},
	{"app", []string{"internal/app"}},
	{"engine", []string{"internal/engine"}},
	{"settings", []string{"internal/settings"}},
	{"machine", []string{"internal/machine"}},
	{"core", []string{"internal/core", "internal/version"}},
}

// testOnly is the folder of packages only tests may import. Its own code is
// not ranked: it is reachable from tests alone.
const testOnly = "internal/testutil"

// rank is the layer index of a package folder (0 is the highest), or -1
// when the table does not place it.
func rank(dir string) int {
	for i, l := range layers {
		for _, d := range l.dirs {
			// A folder covers itself and everything below it.
			if dir == d || strings.HasPrefix(dir, d+"/") {
				return i
			}
		}
	}
	return -1
}

// under reports whether dir is folder top or inside it.
func under(dir, top string) bool { return dir == top || strings.HasPrefix(dir, top+"/") }

// moduleRoot walks up from the working directory (the package folder, where
// `go test` and scripts/test-windows.sh start a test) to go.mod, and returns
// that folder with the module path go.mod declares.
func moduleRoot(t *testing.T) (root, module string) {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		f, err := os.Open(filepath.Join(dir, "go.mod"))
		if err == nil {
			defer f.Close()
			// The first "module" line names the module.
			sc := bufio.NewScanner(f)
			for sc.Scan() {
				if rest, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "module "); ok {
					return dir, strings.Trim(strings.TrimSpace(rest), `"`)
				}
			}
			// A read that failed says so, rather than "no module line".
			if err := sc.Err(); err != nil {
				t.Fatalf("%s: %v", f.Name(), err)
			}
			t.Fatalf("%s has no module line", f.Name())
		}
		// At the top of the file system with no go.mod: run from elsewhere.
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the working directory; run the test from its package folder")
		}
		dir = parent
	}
}

// edge is one import of a module package by another, from one file.
type edge struct {
	// from and to are package folders relative to the module root.
	from, to string
	// file is the importing file, relative to the module root.
	file string
}

// moduleImports parses every non-test .go file under internal/ and cmd/,
// whatever its build constraints (Windows-only files included), and returns
// its imports of this module's packages, plus how many files it read.
func moduleImports(t *testing.T, root, module string) ([]edge, int) {
	t.Helper()
	var edges []edge
	files := 0
	fset := token.NewFileSet()
	for _, top := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			// The go tool ignores testdata and folders starting with . or _.
			if d.IsDir() {
				if n := d.Name(); n == "testdata" || strings.HasPrefix(n, ".") || strings.HasPrefix(n, "_") {
					return filepath.SkipDir
				}
				return nil
			}
			// Only the code that ships: tests may reach anything they test.
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			// Only the import block is needed.
			f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			files++
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			// Folders compare with forward slashes on every system.
			rel = filepath.ToSlash(rel)
			for _, imp := range f.Imports {
				p, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					return err
				}
				// Imports outside this module are not layered.
				to, ok := strings.CutPrefix(p, module+"/")
				if !ok {
					continue
				}
				edges = append(edges, edge{from: filepath.ToSlash(filepath.Dir(rel)), to: to, file: rel})
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return edges, files
}

// violations returns, for each import, why it breaks the layering, if it
// does. Kept apart from the reading so the rules can be tested on made-up
// imports.
func violations(edges []edge) []string {
	var out []string
	for _, e := range edges {
		switch {
		// Shipped code never imports test helpers.
		case under(e.to, testOnly):
			out = append(out, fmt.Sprintf("%s imports %s, which is for tests only", e.file, e.to))
		// Test helpers themselves are not ranked.
		case under(e.from, testOnly):
		// A package the table does not place has to be placed first.
		case rank(e.from) < 0:
			out = append(out, fmt.Sprintf("%s is in no layer: add %s to the table", e.file, e.from))
		case rank(e.to) < 0:
			out = append(out, fmt.Sprintf("%s imports %s, which is in no layer", e.file, e.to))
		// A lower index is a higher layer: importing one is going up.
		case rank(e.to) < rank(e.from):
			out = append(out, fmt.Sprintf("%s (%s) imports %s (%s), a higher layer",
				e.file, layers[rank(e.from)].name, e.to, layers[rank(e.to)].name))
		}
	}
	return out
}

func TestImportsGoDownTheLayers(t *testing.T) {
	root, module := moduleRoot(t)
	edges, files := moduleImports(t, root, module)
	// A walk that read nothing would pass every rule: make sure it read the tree.
	if files < 20 || len(edges) < 20 {
		t.Fatalf("read %d files with %d module imports under %s; expected the whole tree", files, len(edges), root)
	}
	t.Logf("checked %d imports in %d files", len(edges), files)
	for _, v := range violations(edges) {
		t.Error(v)
	}
}

func TestEveryPackageHasALayer(t *testing.T) {
	root, _ := moduleRoot(t)
	// A package that imports nothing of this module makes no edge, so the
	// folders are checked on their own: each with Go code must be placed.
	for _, top := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			// Folders hold packages; their files are looked at below.
			if !d.IsDir() {
				return nil
			}
			if n := d.Name(); n == "testdata" || strings.HasPrefix(n, ".") || strings.HasPrefix(n, "_") {
				return filepath.SkipDir
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			// This package and the test helpers are outside the table.
			if rel == "internal/arch" || under(rel, testOnly) {
				return nil
			}
			entries, err := os.ReadDir(path)
			if err != nil {
				return err
			}
			// Only folders with shipped Go code are packages to place.
			hasCode := slices.ContainsFunc(entries, func(e fs.DirEntry) bool {
				return !e.IsDir() && strings.HasSuffix(e.Name(), ".go") && !strings.HasSuffix(e.Name(), "_test.go")
			})
			if hasCode && rank(rel) < 0 {
				t.Errorf("%s is in no layer: add it to the table in this test and to CONTRIBUTING.md", rel)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestTheRulesCatchWhatTheyAreFor(t *testing.T) {
	// Each made-up import below breaks one rule, and each must be reported.
	bad := []edge{
		// Up one layer.
		{from: "internal/machine/wsl", to: "internal/settings/config", file: "a.go"},
		// Up several layers.
		{from: "internal/core/units", to: "internal/front/ui", file: "b.go"},
		// Engine reaching the app that puts it together.
		{from: "internal/engine/shrink", to: "internal/app", file: "c.go"},
		// The version string importing anything above core.
		{from: "internal/version", to: "internal/machine/wsl", file: "d.go"},
		// Shipped code using a test helper.
		{from: "internal/machine/vhdx", to: "internal/testutil/vhdxtest", file: "e.go"},
		// A package the table does not place.
		{from: "internal/extra", to: "internal/core/units", file: "f.go"},
	}
	for _, e := range bad {
		if len(violations([]edge{e})) != 1 {
			t.Errorf("%s -> %s not reported", e.from, e.to)
		}
	}
	// And these follow the rules: down, sideways in a layer, from cmd.
	good := []edge{
		{from: "internal/front/tui", to: "internal/machine/wsl", file: "g.go"},
		{from: "internal/engine/shrink", to: "internal/engine/disks", file: "h.go"},
		{from: "internal/engine/survey", to: "internal/settings/config", file: "i.go"},
		{from: "internal/front/cli", to: "internal/version", file: "j.go"},
		{from: "cmd/wsl-shrinker", to: "internal/front/cli", file: "k.go"},
		{from: "internal/testutil/vhdxtest", to: "internal/machine/vhdx", file: "l.go"},
	}
	if v := violations(good); len(v) != 0 {
		t.Errorf("allowed imports reported: %q", v)
	}
}
