package shrink

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kanggu12/wsl-shrinker/internal/testutil/shtest"
)

// twoIDs are two made-up containers, as docker ps -q prints them.
var twoIDs = []string{"0123456789ab", "fedcba987654"}

// its is the name `docker info` gives for an engine of a distribution's own.
const its = "Ubuntu 24.04.1 LTS"

// listed runs listScript where the only tool is the made-up docker d, and
// returns the containers it names with the changes docker was asked for.
func listed(t *testing.T, d shtest.Docker) (ids, asked []string) {
	t.Helper()
	dir := shtest.Tools(t)
	file := d.Put(t, dir)
	out, err := shtest.Run(t, dir, listScript)
	if err != nil {
		t.Fatalf("%v %s", err, out)
	}
	// Whatever the script prints is a line of its own making.
	if out != "" && !strings.HasPrefix(out, "running|") {
		t.Fatalf("%q", out)
	}
	return runningIDs(out), shtest.Asked(t, file)
}

func TestTheContainersToStopAreNamedByTheShellAlone(t *testing.T) {
	// Nothing but docker is on the PATH: no grep and no tr, which a
	// distribution made from a small image may not have.
	ids, asked := listed(t, shtest.Docker{OS: its, Running: twoIDs})
	if !slices.Equal(ids, twoIDs) || asked != nil {
		t.Fatalf("%q, asked %q", ids, asked)
	}
	// None runs: none is named.
	if ids, _ := listed(t, shtest.Docker{OS: its}); ids != nil {
		t.Fatalf("%q", ids)
	}
	// Docker Desktop's containers are its own, and stay as they are. With
	// grep, a distribution that has none took them for this one's.
	if ids, _ := listed(t, shtest.Docker{OS: "Docker Desktop", Running: twoIDs}); ids != nil {
		t.Fatalf("named Docker Desktop's containers: %q", ids)
	}
	// No docker, and one whose engine does not answer: nothing to name.
	none := shtest.Tools(t)
	if out, err := shtest.Run(t, none, listScript); err != nil || out != "" {
		t.Fatalf("%v %q", err, out)
	}
	shtest.Stub(t, none, "docker", "exit 1")
	if out, err := shtest.Run(t, none, listScript); err != nil || out != "" {
		t.Fatalf("%v %q", err, out)
	}
}

func TestOnlyContainerIDsAreRead(t *testing.T) {
	// A warning wsl.exe put before the line, and something that is no id,
	// are passed over: only ids go into the script that stops them.
	out := "wsl: a warning of its own\nrunning|0123456789ab $(reboot) fedcba987654 ;\nrunning\n"
	if ids := runningIDs(out); !slices.Equal(ids, twoIDs) {
		t.Fatalf("%q", ids)
	}
}

func TestTheStopScriptRunsInAPlainShell(t *testing.T) {
	// docker, timeout for the person's hooks, and a stand-in for sync that
	// says it ran: the script needs nothing else.
	dir := shtest.Tools(t, "timeout")
	asked := shtest.Docker{OS: its, Running: twoIDs}.Put(t, dir)
	marks := t.TempDir()
	shtest.Stub(t, dir, "sync", "echo synced >'"+filepath.Join(marks, "synced")+"'")
	// The hooks, in a folder of the test's own in place of /etc: one that
	// works, one that fails, and a file that is not a program.
	hooks := t.TempDir()
	shtest.Stub(t, hooks, "10-save", "echo saved >'"+filepath.Join(marks, "saved")+"'")
	shtest.Stub(t, hooks, "20-broken", "exit 3")
	if err := os.WriteFile(filepath.Join(hooks, "30-notes.txt"), []byte("not a program\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	at := func(ids []string, folder string) string {
		t.Helper()
		s := strings.ReplaceAll(stopScript(ids), "/etc/wsl-shrinker/pre-stop.d", folder)
		// Never the hooks of the machine the test runs on.
		if strings.Contains(s, "/etc/") || !strings.Contains(s, folder) {
			t.Fatalf("the script looks for its hooks somewhere else now:\n%s", s)
		}
		return s
	}
	out, err := shtest.Run(t, dir, at(twoIDs, hooks))
	if err != nil {
		t.Fatalf("%v %s", err, out)
	}
	if want := "hook|ok|" + hooks + "/10-save\nhook|failed|" + hooks + "/20-broken\n"; out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
	// The containers are stopped in one call, the hook ran, and the sync
	// came after.
	if got := shtest.Asked(t, asked); !slices.Equal(got, []string{"stop " + strings.Join(twoIDs, " ")}) {
		t.Fatalf("docker was asked for %q", got)
	}
	for _, mark := range []string{"saved", "synced"} {
		if _, err := os.Stat(filepath.Join(marks, mark)); err != nil {
			t.Fatalf("no %s", mark)
		}
	}
	// No container and no hooks folder: the sync alone, and docker is not
	// asked for anything.
	dir = shtest.Tools(t)
	asked = shtest.Docker{OS: its}.Put(t, dir)
	shtest.Stub(t, dir, "sync", "echo synced >'"+filepath.Join(marks, "again")+"'")
	if out, err := shtest.Run(t, dir, at(nil, filepath.Join(hooks, "none"))); err != nil || out != "" {
		t.Fatalf("%v %q", err, out)
	}
	if _, err := os.Stat(filepath.Join(marks, "again")); err != nil || shtest.Asked(t, asked) != nil {
		t.Fatalf("%v, docker was asked for %q", err, shtest.Asked(t, asked))
	}
}

func TestTheStartScriptSaysWhichContainerStarted(t *testing.T) {
	// One container that starts and one that does not: each has a line of
	// its own, and the one that failed does not end the script.
	dir := shtest.Tools(t)
	asked := shtest.Docker{OS: its, NoStart: twoIDs[:1]}.Put(t, dir)
	out, err := shtest.Run(t, dir, startScript(twoIDs))
	if err != nil {
		t.Fatalf("%v %s", err, out)
	}
	if want := "failed|" + twoIDs[0] + "\nstarted|" + twoIDs[1] + "\n"; out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
	if got := shtest.Asked(t, asked); !slices.Equal(got, []string{"start " + twoIDs[1]}) {
		t.Fatalf("docker was asked for %q", got)
	}
}

func TestNoScriptNamesATextTool(t *testing.T) {
	// The tests above run the scripts with those tools missing; this one
	// reads them whole, the branches no test takes too.
	for _, s := range []string{listScript, stopScript(twoIDs), startScript(twoIDs)} {
		if tool := shtest.TextTool(s); tool != "" {
			t.Errorf("%s is needed by:\n%s", tool, s)
		}
	}
}
