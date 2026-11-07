package survey

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/kanggu12/wsl-shrinker/internal/machine/wsl"
)

// sample is what the script prints on a distribution with Docker.
const sample = `fs|1055762868|457000000|545000000
opts|rw,relatime,discard,errors=remount-ro,data=ordered
sys|apt|/var/cache/apt/archives|204800
sys|journal|/var/log/journal|1048576
home|pip|/home/ann/.cache/pip|512000
home|go-build|/root/.cache/go-build|0
home|huggingface|/home/ann/.cache/huggingface|20971520
home|pip|/home/x;rm -rf ~/.cache/pip|100
home|evil|/home/ann/.ssh|100
docker|on
containers|3
df|Images|5.2GB (60%)
df|Build Cache|3.4GB
img|sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef|postgres:16|440MB|3 weeks ago|1
img|sha256:ba9876543210fedcba9876543210fedcba9876543210fedcba9876543210fedc|node:20|1.1GB|2 months ago|0
img|sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa|<none>:<none>|200MB|5 days ago|0
img|sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb|<none>:<none>|300MB|5 days ago|0
img|sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc|<none>:<none>|900MB|5 days ago|0
dangling|sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
dangling|sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
df|Build Cache|
img|nothex;rm -rf|evil:1|1GB|now|0
`

func TestParse(t *testing.T) {
	r := parse(sample)
	if r.Size != 1055762868*1024 || r.Used != 457000000*1024 || !strings.Contains(r.Options, "discard") || !r.Docker || r.Containers != 3 {
		t.Fatalf("filesystem: %+v", r)
	}
	byID := map[string]Item{}
	for _, it := range r.Items {
		byID[it.ID] = it
	}
	// Known caches with their commands; empty ones are dropped.
	if it := byID["pip:/home/ann/.cache/pip"]; it.Bytes != 512000*1024 || it.Command != removeContents("/home/ann/.cache/pip") {
		t.Fatalf("pip: %+v", it)
	}
	if _, ok := byID["go-build:/root/.cache/go-build"]; ok {
		t.Fatal("an empty cache was listed")
	}
	// The journal keeps its last 100 MB: only the rest goes.
	if it := byID["journal"]; it.Bytes != 1048576*1024-100<<20 {
		t.Fatalf("journal: %d", it.Bytes)
	}
	if it := byID["huggingface:/home/ann/.cache/huggingface"]; it.Kind != KindDownload {
		t.Fatalf("huggingface: %+v", it)
	}
	// A path with shell syntax, and a directory not on the list, never pass.
	for id := range byID {
		if strings.Contains(id, ";") || strings.Contains(id, ".ssh") || strings.Contains(id, "evil") || strings.Contains(id, "nothex") {
			t.Fatalf("unsafe item %q", id)
		}
	}
	// Docker: the build cache, the two dangling images as one item (the
	// unnamed parent "cccc" is left out: prune keeps it), a named image, and
	// an image in use that cannot be selected. An empty size line is skipped.
	if it := byID["docker-build-cache"]; it.Bytes != 3_400_000_000 {
		t.Fatalf("build cache: %+v", it)
	}
	if it := byID["docker-dangling"]; it.Bytes != 500_000_000 || it.Command != "docker image prune --force" {
		t.Fatalf("dangling: %+v", it)
	}
	if it := byID["image:ba9876543210"]; it.Bytes != 1_100_000_000 || it.Command != "docker image rm ba9876543210" || it.Note != "2 months ago" {
		t.Fatalf("node: %+v", it)
	}
	if _, ok := byID["image:0123456789ab"]; ok {
		t.Fatal("an image in use has no command, so it is not offered")
	}
	// Largest first.
	for i := 1; i < len(r.Items); i++ {
		if r.Items[i].Bytes > r.Items[i-1].Bytes {
			t.Fatalf("not sorted: %v", r.Items)
		}
	}
}

func TestNoCommandTouchesWhatMustStay(t *testing.T) {
	// Every command this package can build, from every source of items.
	r := parse(sample)
	cmds := []string{}
	for _, it := range r.Items {
		cmds = append(cmds, it.Command)
	}
	for _, c := range caches {
		cmds = append(cmds, home(c.what, "/home/u/"+c.rel, 1).Command)
	}
	cmds = append(cmds, system("apt", "", 1).Command, system("journal", "", 1).Command)
	forbidden := regexp.MustCompile(`volume|system prune|container|\brm -f\b|--volumes|unregister|docker rm\b|/mnt/`)
	for _, c := range cmds {
		if forbidden.MatchString(c) {
			t.Fatalf("command %q touches something that must stay", c)
		}
	}
	// And the source has no such command anywhere.
	src, err := os.ReadFile("survey.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"volume prune", "system prune", "container prune", "docker rm "} {
		if strings.Contains(string(src), bad) {
			t.Fatalf("survey.go contains %q", bad)
		}
	}
}

func TestRemoveContentsStaysWhereItWasListed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the command is for Linux")
	}
	root := t.TempDir()
	write := func(p string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A cache whose parent is a link to somewhere else: ~/.cargo/registry
	// pointing at a system folder, as in the review that found this.
	write(filepath.Join(root, "var", "cache", "debconf", "config.dat"))
	if err := os.MkdirAll(filepath.Join(root, "home", "ann", ".cargo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "var"), filepath.Join(root, "home", "ann", ".cargo", "registry")); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(root, "home", "ann", ".cargo", "registry", "cache")
	out, err := exec.Command("sh", "-c", removeContents(linked)).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "moved or linked") {
		t.Fatalf("ran through a link: %v %s", err, out)
	}
	if _, err := os.Stat(filepath.Join(root, "var", "cache", "debconf", "config.dat")); err != nil {
		t.Fatal("removed what the link points to")
	}
	// A real cache folder: its contents go, the folder stays.
	pip := filepath.Join(root, "home", "ann", ".cache", "pip")
	write(filepath.Join(pip, "wheels", "a.whl"))
	if out, err := exec.Command("sh", "-c", removeContents(pip)).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	left, _ := os.ReadDir(pip)
	if len(left) != 0 {
		t.Fatalf("left %v", left)
	}
	if _, err := os.Stat(pip); err != nil {
		t.Fatal("the folder itself went")
	}
	// The survey does not list the linked cache at all.
	s := strings.NewReplacer("for h in /root /home/*", "for h in "+root+"/home/*",
		"/var/", root+"/var/", "command -v docker", "false").Replace(script())
	listed, err := exec.Command("sh", "-c", s).CombinedOutput()
	if err != nil {
		t.Fatalf("%v %s", err, listed)
	}
	if strings.Contains(string(listed), "home|cargo|") {
		t.Fatalf("listed a linked cache:\n%s", listed)
	}
}

func TestDockerSize(t *testing.T) {
	for in, want := range map[string]int64{"0B": 0, "512MB": 512e6, "1.23GB": 1.23e9, "12.5kB": 12500, "2TB": 2e12, "junk": 0} {
		if got := dockerSize(in); got != want {
			t.Errorf("%s: got %d, want %d", in, got, want)
		}
	}
}

func TestScriptRunsInSh(t *testing.T) {
	// The script runs under a plain POSIX sh against a fake root: it must
	// parse, and find a cache in a home directory there.
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	// The script runs inside a Linux distribution; a Windows sh (Git's)
	// would read the test's C:\ paths as escapes.
	if runtime.GOOS == "windows" {
		t.Skip("the script is for Linux")
	}
	root := t.TempDir()
	pip := filepath.Join(root, "home", "ann", ".cache", "pip")
	if err := os.MkdirAll(pip, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pip, "wheel"), make([]byte, 64<<10), 0o644); err != nil {
		t.Fatal(err)
	}
	// Point every place the script reads at the fake root, and leave out
	// Docker, so the test reads nothing of the machine it runs on.
	s := strings.NewReplacer("for h in /root /home/*", "for h in "+root+"/home/*",
		"/var/", root+"/var/", "command -v docker", "false").Replace(script())
	out, err := exec.Command("sh", "-n", "-c", s).CombinedOutput()
	if err != nil {
		t.Fatalf("syntax: %v %s", err, out)
	}
	out, err = exec.Command("sh", "-c", s).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v %s", err, out)
	}
	if !strings.Contains(string(out), "home|pip|"+pip+"|") || !strings.Contains(string(out), "fs|") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestClean(t *testing.T) {
	fake := &wsl.Fake{List: []wsl.Distro{{Name: "U", Running: true}}}
	var got string
	fake.Script = func(_, user, s string) (string, error) {
		if user != "root" {
			t.Fatalf("ran as %s", user)
		}
		got = s
		return "ok", nil
	}
	items := []Item{{ID: "apt", What: "apt", Command: "apt-get clean"}, {ID: "x", What: "x"}}
	if _, err := Clean(context.Background(), fake, "U", items); err != nil {
		t.Fatal(err)
	}
	// Items without a command are skipped; each command reports a failure
	// without stopping the rest.
	if !strings.Contains(got, "apt-get clean 2>&1 || echo 'failed: apt'") || strings.Contains(got, "== x") {
		t.Fatalf("script:\n%s", got)
	}
	// A stopped distribution is not started to clean it.
	fake.List[0].Running = false
	if _, err := Clean(context.Background(), fake, "U", items); err == nil {
		t.Fatal("cleaned a stopped distribution")
	}
}
