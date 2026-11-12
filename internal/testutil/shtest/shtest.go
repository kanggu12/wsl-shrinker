// Package shtest runs the scripts this program sends into a distribution
// under the sh of the machine a test runs on, with only named tools on the
// PATH: a distribution with nothing else installed. A script that grows to
// need one more tool fails there, as it would in such a distribution.
package shtest

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// Tools is a new PATH folder holding just the named tools, linked from
// where this machine has them. The test is skipped where one is missing,
// and on anything but Linux: the scripts read /proc and run Linux tools.
func Tools(t testing.TB, tools ...string) string {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the scripts are for Linux")
	}
	dir := t.TempDir()
	for _, tool := range tools {
		at, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("no %s here", tool)
		}
		if err := os.Symlink(at, filepath.Join(dir, tool)); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// sh is this machine's sh, found before any PATH is cut down.
func sh(t testing.TB) string {
	t.Helper()
	at, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	return at
}

// Run runs script with sh, as wsl.exe does, with only the tools in dir on
// its PATH. It returns what the script printed, errors included.
func Run(t testing.TB, dir, script string) (string, error) {
	t.Helper()
	cmd := exec.Command(sh(t), "-c", script)
	// Nothing of the test's own environment reaches the script.
	cmd.Env = []string{"PATH=" + dir}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// textTool finds a call of a tool that picks text apart, at the start of a
// command (not inside an option such as --no-trunc).
var textTool = regexp.MustCompile(`(^|[^\w.-])(awk|sed|grep|xargs|wc|tr|head|tail|sort|uniq)\s`)

// TextTool names the first such tool a script calls, or nothing. The shell
// does that work in these scripts: a distribution made from a small image
// may have none of them, and the tests that run a script without them see
// only the branches they take.
func TextTool(script string) string {
	if m := textTool.FindStringSubmatch(script); m != nil {
		return m[2]
	}
	return ""
}

// Stub puts a made-up tool called name into dir, a folder Tools made: body
// is what it runs, with nothing but the shell.
func Stub(t testing.TB, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!"+sh(t)+"\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// Docker is a made-up docker command: what it answers to each question
// this program asks of one.
type Docker struct {
	// OS is the operating system `docker info` names: "Docker Desktop" for
	// Docker Desktop's integration, the distribution's own name otherwise.
	OS string
	// Running are the ids `docker ps -q` (or --quiet) prints.
	Running []string
	// DF are the lines of `docker system df` ("Build Cache|3.4GB").
	DF []string
	// Dangling are the full ids of the images with no name.
	Dangling []string
	// Images are the lines of `docker system df -v` for the images
	// ("sha256:…|node:20|1.1GB|2 months ago|0").
	Images []string
	// Tags are the lines of `docker image ls` ("node:20 sha256:…").
	Tags []string
	// NoStart are the ids `docker start` fails for.
	NoStart []string
}

// Put writes the made-up docker into dir, a folder Tools made, and returns
// the file it writes down every change it is asked for, one per line
// ("image rm node:20", "start 0123456789ab"). It uses nothing but the
// shell, so it runs with any PATH.
func (d Docker) Put(t testing.TB, dir string) (asked string) {
	t.Helper()
	asked = filepath.Join(dir, "docker.asked")
	// lines prints its lines, or nothing for none (printf with no argument
	// would print an empty one).
	lines := func(ls []string) string {
		if len(ls) == 0 {
			return ":"
		}
		quoted := make([]string, len(ls))
		for i, l := range ls {
			// The answers are made up by tests: none holds a quote.
			if strings.Contains(l, "'") {
				t.Fatalf("a quote in %q", l)
			}
			quoted[i] = "'" + l + "'"
		}
		return `printf '%s\n' ` + strings.Join(quoted, " ")
	}
	// Docker ends its list of images with an empty line.
	images := d.Images
	if len(images) > 0 {
		images = append(append([]string{}, images...), "")
	}
	Stub(t, dir, "docker", `case "$*" in
info) ;;
'info --format {{.OperatingSystem}}') `+lines([]string{d.OS})+` ;;
'ps -q' | 'ps --quiet') `+lines(d.Running)+` ;;
'system df --format {{.Type}}|{{.Reclaimable}}') `+lines(d.DF)+` ;;
'image ls --filter dangling=true --quiet --no-trunc') `+lines(d.Dangling)+` ;;
'system df -v --format '*) `+lines(images)+` ;;
'image ls --no-trunc --format {{.Repository}}:{{.Tag}} {{.ID}}') `+lines(d.Tags)+` ;;
'start '*) case ' `+strings.Join(d.NoStart, " ")+` ' in *" $2 "*) exit 1 ;; esac; printf '%s\n' "$*" >>'`+asked+`' ;;
'image rm '* | 'stop '* | 'builder prune --all --force' | 'image prune --force') printf '%s\n' "$*" >>'`+asked+`' ;;
*) printf 'docker: not expected: %s\n' "$*" >&2; exit 64 ;;
esac`)
	return asked
}

// Asked reads what the made-up docker was asked to change, one per line;
// nothing when it was asked for no change.
func Asked(t testing.TB, asked string) []string {
	t.Helper()
	b, err := os.ReadFile(asked)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}
