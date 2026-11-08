package internal

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The changelog is each release's notes: the release workflow takes a tag's
// section as its body and refuses a tag without one. So its shape is checked
// like code: Keep a Changelog 1.1.0, newest first, one link per version, and
// the README's release badge naming the newest.
var (
	// versionHead is a release's heading: "## [0.1.0] - 2026-11-08", or a
	// pre-release's, "## [0.2.0-rc.1] - 2026-11-20".
	versionHead = regexp.MustCompile(`^## \[(\d+)\.(\d+)\.(\d+)(-[0-9A-Za-z.]+)?\] - (\d{4}-\d{2}-\d{2})$`)
	// subHead is a heading inside a release.
	subHead = regexp.MustCompile(`^### (.+)$`)
	// linkLine is a link definition at the bottom: "[0.1.0]: https://...".
	linkLine = regexp.MustCompile(`^\[([^\]]+)\]: (\S+)$`)
	// knownSubHead are the kinds of change Keep a Changelog names.
	knownSubHead = map[string]bool{"Added": true, "Changed": true, "Deprecated": true, "Removed": true, "Fixed": true, "Security": true}
)

// repo is where the links point.
const repo = "https://github.com/kanggu12/wsl-shrinker"

// release is one version's section as read.
type release struct {
	// name is the version without its v, as the heading writes it.
	name string
	// num is its three numbers, pre is its pre-release part ("" for none).
	num [3]int
	pre string
	// body is the section's text up to the next "## " heading: the notes.
	body string
	// line is the heading's line number, for messages.
	line int
}

// older reports whether a is older than b: by the numbers, and a pre-release
// before the release it leads to.
func older(a, b release) bool {
	for i := range a.num {
		if a.num[i] != b.num[i] {
			return a.num[i] < b.num[i]
		}
	}
	return a.pre != "" && b.pre == ""
}

// readChangelog reads CHANGELOG.md from the module root and returns its
// releases, newest first as written, with the problems found on the way.
func readChangelog(t *testing.T) (rels []release, links map[string]string, unreleased bool, problems []string) {
	t.Helper()
	root, _ := moduleRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "CHANGELOG.md"))
	if err != nil {
		t.Fatal(err)
	}
	// A checkout on Windows may hold CRLF line ends; the shape is the same.
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	links = map[string]string{}
	// cur is the release whose section the lines go into, -1 before any.
	cur := -1
	for i, l := range lines {
		switch {
		case l == "## [Unreleased]":
			// Unreleased comes once, above every release.
			if len(rels) > 0 || unreleased {
				problems = append(problems, "line "+strconv.Itoa(i+1)+": [Unreleased] must come first, once")
			}
			unreleased, cur = true, -1
		case strings.HasPrefix(l, "## "):
			m := versionHead.FindStringSubmatch(l)
			if m == nil {
				problems = append(problems, "line "+strconv.Itoa(i+1)+": "+strconv.Quote(l)+" is not ## [X.Y.Z] - YYYY-MM-DD")
				cur = -1
				continue
			}
			r := release{name: m[1] + "." + m[2] + "." + m[3] + m[4], pre: m[4], line: i + 1}
			for j := range r.num {
				r.num[j], _ = strconv.Atoi(m[j+1])
			}
			// Newest first: every version is older than the one above it.
			if len(rels) > 0 && !older(r, rels[len(rels)-1]) {
				problems = append(problems, "line "+strconv.Itoa(i+1)+": "+r.name+" is not older than "+rels[len(rels)-1].name+" above it")
			}
			rels = append(rels, r)
			cur = len(rels) - 1
		case linkLine.MatchString(l):
			// The links at the bottom end every section.
			m := linkLine.FindStringSubmatch(l)
			links[m[1]] = m[2]
			cur = -1
		default:
			if m := subHead.FindStringSubmatch(l); m != nil && !knownSubHead[m[1]] {
				problems = append(problems, "line "+strconv.Itoa(i+1)+": ### "+m[1]+" is not a Keep a Changelog heading")
			}
			// Everything else under a release's heading is its notes.
			if cur >= 0 {
				rels[cur].body += l + "\n"
			}
		}
	}
	return rels, links, unreleased, problems
}

func TestTheChangelogIsInShape(t *testing.T) {
	rels, links, unreleased, problems := readChangelog(t)
	for _, p := range problems {
		t.Error(p)
	}
	if !unreleased {
		t.Error("no [Unreleased] section")
	}
	if len(rels) == 0 {
		t.Fatal("no release")
	}
	for _, r := range rels {
		// A section with nothing in it would publish a release with no notes;
		// the workflow refuses that, so this says it first.
		if strings.TrimSpace(r.body) == "" {
			t.Errorf("line %d: [%s] has no notes", r.line, r.name)
		}
		// Every version links to its release page.
		if want := repo + "/releases/tag/v" + r.name; links[r.name] != want {
			t.Errorf("[%s] links to %q, want %q", r.name, links[r.name], want)
		}
	}
	// Unreleased compares the newest release with what is on main.
	if want := repo + "/compare/v" + rels[0].name + "...HEAD"; links["Unreleased"] != want {
		t.Errorf("[Unreleased] links to %q, want %q", links["Unreleased"], want)
	}
	// No link is left for a version the file no longer has.
	for name := range links {
		if name == "Unreleased" {
			continue
		}
		found := false
		for _, r := range rels {
			found = found || r.name == name
		}
		if !found {
			t.Errorf("[%s] is linked but has no section", name)
		}
	}
}

func TestTheReleaseBadgeNamesTheNewestRelease(t *testing.T) {
	// The README shows the newest release as a badge, a file make badges
	// writes, and says it again in the badge's alt text.
	rels, _, _, _ := readChangelog(t)
	if len(rels) == 0 {
		t.Fatal("no release")
	}
	// Pre-releases are not what the badge names: the newest release is.
	newest := ""
	for _, r := range rels {
		if r.pre == "" {
			newest = r.name
			break
		}
	}
	root, _ := moduleRoot(t)
	svg, err := os.ReadFile(filepath.Join(root, "docs", "assets", "badges", "release.svg"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(svg), "release: v"+newest) {
		t.Errorf("docs/assets/badges/release.svg does not say v%s: run make badges", newest)
	}
	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(readme), `alt="Release v`+newest+`"`) {
		t.Errorf("README.md's release badge does not say v%s: run make badges", newest)
	}
}

func TestTheChangelogCheckCatchesWhatItIsFor(t *testing.T) {
	// The ordering rule on made-up versions: newer above older, and a
	// pre-release below the release it leads to.
	v := func(a, b, c int, pre string) release { return release{num: [3]int{a, b, c}, pre: pre} }
	for _, c := range []struct {
		a, b release
		want bool
	}{
		{v(0, 1, 0, ""), v(0, 2, 0, ""), true},
		{v(0, 2, 0, ""), v(0, 1, 0, ""), false},
		{v(0, 1, 9, ""), v(0, 2, 0, ""), true},
		{v(0, 2, 0, "-rc.1"), v(0, 2, 0, ""), true},
		{v(0, 2, 0, ""), v(0, 2, 0, "-rc.1"), false},
		{v(0, 1, 0, ""), v(0, 1, 0, ""), false},
	} {
		if got := older(c.a, c.b); got != c.want {
			t.Errorf("older(%v%s, %v%s) = %v", c.a.num, c.a.pre, c.b.num, c.b.pre, got)
		}
	}
	// And the heading pattern takes what the workflow takes, and nothing
	// else.
	for l, want := range map[string]bool{
		"## [0.1.0] - 2026-11-08":       true,
		"## [0.2.0-rc.1] - 2026-11-20":  true,
		"## 0.1.0 - 2026-11-08":         false,
		"## [0.1.0] - 2026-11-08 (old)": false,
		"## [v0.1.0] - 2026-11-08":      false,
	} {
		if got := versionHead.MatchString(l); got != want {
			t.Errorf("%q matches: %v", l, got)
		}
	}
}
