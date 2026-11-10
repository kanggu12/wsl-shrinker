package survey

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/kanggu12/wsl-shrinker/internal/testutil/shtest"
)

// scriptAt is the script with every folder it looks into moved under root:
// a test reads nothing of the machine it runs on.
func scriptAt(root string) string {
	return strings.NewReplacer("for h in /root /home/*", "for h in "+root+"/home/*", "/var/", root+"/var/").Replace(script())
}

// aCache makes a pip cache with one file under a fake root and returns the
// folder and the file.
func aCache(t *testing.T) (pip, file string) {
	t.Helper()
	pip = filepath.Join(t.TempDir(), "home", "ann", ".cache", "pip")
	file = filepath.Join(pip, "wheels", "a.whl")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, make([]byte, 64<<10), 0o644); err != nil {
		t.Fatal(err)
	}
	return pip, file
}

func TestTheLookInsideNeedsOnlyTheCoreUtilities(t *testing.T) {
	// A distribution imported from a small container image can come without
	// awk, sed and grep: the look inside needs none of them. Only what it
	// does need is on the PATH here, so one more tool in the script fails
	// this test.
	core := []string{"df", "du", "cut", "stat", "readlink"}
	pip, _ := aCache(t)
	root := strings.TrimSuffix(pip, "/home/ann/.cache/pip")
	s := scriptAt(root)
	look := func(tools ...string) string {
		t.Helper()
		out, err := shtest.Run(t, shtest.Tools(t, tools...), s)
		// A tool the script names and the PATH lacks would be said here.
		if err != nil || strings.Contains(out, "not found") {
			t.Fatalf("the script needs a tool beyond %v: %v\n%s", tools, err, out)
		}
		return out
	}
	// The root filesystem's numbers and its mount options, with or without
	// find.
	fs, cache := []string{`(?m)^fs\|\d+\|\d+\|\d+$`, `(?m)^opts\|r[ow]`}, `(?m)^home\|pip\|`+regexp.QuoteMeta(pip)+`\|\d+$`
	// Without find, which empties a cache, a cache that could not be
	// emptied is not offered. This half comes first: it also runs where
	// there is no find to link.
	out := look(core...)
	for _, want := range fs {
		if !regexp.MustCompile(want).MatchString(out) {
			t.Errorf("no line like %s in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "home|") {
		t.Errorf("listed a cache nothing here can empty:\n%s", out)
	}
	// With it, the cache is listed too.
	out = look(append(core, "find")...)
	for _, want := range append(fs, cache) {
		if !regexp.MustCompile(want).MatchString(out) {
			t.Errorf("no line like %s in:\n%s", want, out)
		}
	}
}

func TestTheRootsOptionsAreReadByTheShellAlone(t *testing.T) {
	// Nothing at all is on the PATH: the shell's own read does it.
	none := shtest.Tools(t)
	out, err := shtest.Run(t, none, RootOptions)
	if opts := strings.TrimSpace(out); err != nil || !strings.HasPrefix(opts, "rw") && !strings.HasPrefix(opts, "ro") {
		t.Fatalf("%v %q", err, out)
	}
	// With several mounts on "/", the last line is the one on top.
	mounts := filepath.Join(t.TempDir(), "mounts")
	from := func(lines string) string {
		t.Helper()
		if err := os.WriteFile(mounts, []byte(lines), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := shtest.Run(t, none, strings.Replace(RootOptions, "/proc/mounts", mounts, 1))
		if err != nil {
			t.Fatalf("%v %s", err, out)
		}
		return out
	}
	if got := from("rootfs / rootfs rw 0 0\n/dev/sdc / ext4 ro,relatime,discard 0 0\n/dev/sdd /mnt/data ext4 rw,relatime 0 0\n"); got != "ro,relatime,discard\n" {
		t.Fatalf("%q", got)
	}
	// None for "/": an empty line, which the script leaves out.
	if got := from("/dev/sdd /mnt/data ext4 rw,relatime 0 0\n"); got != "\n" {
		t.Fatalf("%q", got)
	}
}

func TestEmptyingACacheNeedsOnlyFindAndStat(t *testing.T) {
	// The command runs as Clean runs it, inside its line of the script.
	pip, file := aCache(t)
	clean := cleanScript([]Item{{ID: "pip", What: "pip", Command: removeContents(pip)}})
	// Without stat nothing can be checked: the line says so (after the
	// shell's own word that stat is missing) and what is there stays.
	out, err := shtest.Run(t, shtest.Tools(t, "find"), clean)
	if err != nil || !strings.HasPrefix(out, "== pip\n") || !strings.HasSuffix(out, "skipped "+pip+": stat gave no answer\nfailed: pip\n") {
		t.Fatalf("%v %q", err, out)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal("removed with nothing checked")
	}
	// With both, the folder is emptied and stays.
	out, err = shtest.Run(t, shtest.Tools(t, "find", "stat"), clean)
	if err != nil || out != "== pip\n" {
		t.Fatalf("%v %q", err, out)
	}
	if _, err := os.Stat(file); err == nil {
		t.Fatal("the file is still there")
	}
	if left, err := os.ReadDir(pip); err != nil || len(left) != 0 {
		t.Fatalf("the folder: %v %v", left, err)
	}
}

func TestACheckThatCannotBeMadeRemovesNothing(t *testing.T) {
	stays := func(file, out string, err error, says string) {
		t.Helper()
		if err == nil || !strings.Contains(out, says) {
			t.Errorf("went on: %v %s", err, out)
		}
		if _, err := os.Stat(file); err != nil {
			t.Fatalf("removed %s", file)
		}
	}
	// No stat: whether the folder is on the root filesystem cannot be told.
	// Two empty answers are not "the same filesystem".
	pip, file := aCache(t)
	out, err := shtest.Run(t, shtest.Tools(t, "find"), removeContents(pip))
	stays(file, out, err, "stat gave no answer")
	// The mounts cannot be read: whether something is mounted inside
	// cannot be told.
	cmd := strings.Replace(removeContents(pip), "/proc/self/mountinfo", filepath.Join(pip, "..", "no-such-file"), 1)
	if cmd == removeContents(pip) {
		t.Fatal("the command reads the mounts from somewhere else now")
	}
	out, err = shtest.Run(t, shtest.Tools(t, "find", "stat"), cmd)
	stays(file, out, err, "its mounts could not be read")
	// A mount inside, as the mounts list it: the fifth field under the folder.
	listed := filepath.Join(t.TempDir(), "mountinfo")
	line := "36 35 8:32 / / rw,relatime - ext4 /dev/sdc rw\n77 36 8:32 /data " + pip + "/wheels rw,relatime - ext4 /dev/sdc rw\n"
	if err := os.WriteFile(listed, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = shtest.Run(t, shtest.Tools(t, "find", "stat"), strings.Replace(removeContents(pip), "/proc/self/mountinfo", listed, 1))
	stays(file, out, err, "something is mounted inside")
	// One beside it, whose path only starts the same, is not inside.
	line = "36 35 8:32 / / rw,relatime - ext4 /dev/sdc rw\n77 36 8:32 /data " + pip + "-old rw,relatime - ext4 /dev/sdc rw\n"
	if err := os.WriteFile(listed, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := shtest.Run(t, shtest.Tools(t, "find", "stat"), strings.Replace(removeContents(pip), "/proc/self/mountinfo", listed, 1)); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	if _, err := os.Stat(file); err == nil {
		t.Fatal("a mount beside the folder kept it from being emptied")
	}
}
