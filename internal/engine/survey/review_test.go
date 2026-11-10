package survey

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kanggu12/wsl-shrinker/internal/testutil/shtest"
)

// One image with two tags: docker system df -v lists it once per tag.
func TestReviewImageWithTwoTags(t *testing.T) {
	out := "docker|on\n" +
		"img|sha256:4f8a2c1d9e7b0000000000000000000000000000000000000000000000000000|node:22|1.1GB|5 weeks ago|0\n" +
		"img|sha256:4f8a2c1d9e7b0000000000000000000000000000000000000000000000000000|node:lts|1.1GB|5 weeks ago|0\n"
	r := parse(out)
	ids := map[string]int{}
	var total int64
	for _, it := range r.Items {
		ids[it.ID]++
		total += it.Bytes
	}
	for id, n := range ids {
		if n > 1 {
			t.Errorf("id %s appears %d times (total counted %d bytes for one 1.1 GB image); command %q", id, n, total, r.Items[0].Command)
		}
	}
}

// removal is what the command for an image of id asks docker to remove,
// where docker image ls answers with lines.
func removal(t *testing.T, id string, lines ...string) string {
	t.Helper()
	it := image([]string{"sha256:" + id, "x", "1GB", "1 week ago", "0"})
	// Nothing but a made-up docker is on the PATH: the shell picks the names
	// out by itself, with no awk and no xargs, which a small distribution
	// may not have, and the Docker of the machine the test runs on cannot
	// be reached.
	dir := shtest.Tools(t)
	asked := shtest.Docker{OS: "Ubuntu 24.04.1 LTS", Tags: lines}.Put(t, dir)
	if out, err := shtest.Run(t, dir, it.Command); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	return strings.Join(shtest.Asked(t, asked), "\n")
}

func TestAnImageKnownOnlyByItsDigestIsRemovedByID(t *testing.T) {
	// "name:<none>" names nothing docker can remove: the id is used.
	id := "4f8a2c1d9e7b"
	full := "sha256:4f8a2c1d9e7b" + strings.Repeat("0", 52)
	if got := removal(t, id, "ghcr.io/a/b:<none> "+full); got != "image rm "+full {
		t.Fatalf("%q", got)
	}
	// With tags of its own, the tags are removed, which removes the image;
	// the id beside them would fail once they are gone.
	if got := removal(t, id, "node:22 "+full, "node:<none> "+full); got != "image rm node:22" {
		t.Fatalf("%q", got)
	}
	// Every tag of the image goes in the one call, and no other image's.
	other := "sha256:5a1b" + strings.Repeat("0", 60)
	if got := removal(t, id, "node:22 "+full, "redis:7 "+other, "node:lts "+full); got != "image rm node:22 node:lts" {
		t.Fatalf("%q", got)
	}
	// An image that is gone by now: nothing is asked of docker.
	if got := removal(t, id, "redis:7 "+other); got != "" {
		t.Fatalf("%q", got)
	}
}

func TestAMountInsideACacheLeavesItWhole(t *testing.T) {
	// A bind mount of the same filesystem inside a cache folder: find -xdev
	// would go into it, so nothing of the folder is removed. The mount is
	// made in a mount namespace of the test's own, which nothing else sees.
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Skip("needs root on Linux for a mount namespace")
	}
	if _, err := exec.LookPath("unshare"); err != nil {
		t.Skip("no unshare")
	}
	root := t.TempDir()
	cache := filepath.Join(root, "home", "ann", ".cache", "pip")
	data := filepath.Join(root, "data")
	for _, d := range []string{filepath.Join(cache, "inner"), data} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(data, "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "a.whl"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The removal runs with only find and stat on its PATH, as in a small
	// distribution: when awk read the mounts, one without awk took "no awk"
	// for "nothing mounted inside" and removed what was mounted.
	tools := shtest.Tools(t, "find", "stat")
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	remove := filepath.Join(root, "remove.sh")
	if err := os.WriteFile(remove, []byte(removeContents(cache)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Inside the folder, and on the folder itself: emptying the folder
	// would empty what is mounted there.
	for _, at := range []string{filepath.Join(cache, "inner"), cache} {
		script := "mount --bind " + data + " " + at + " && PATH=" + tools + " " + sh + " " + remove
		out, err := exec.Command("unshare", "-m", "--propagation", "private", "sh", "-c", script).CombinedOutput()
		if strings.Contains(string(out), "Operation not permitted") {
			t.Skipf("no mount namespace here: %s", out)
		}
		if err == nil || !strings.Contains(string(out), "something is mounted inside") {
			t.Fatalf("mounted at %s: went on: %v %s", at, err, out)
		}
		for _, f := range []string{filepath.Join(data, "keep.txt"), filepath.Join(cache, "a.whl")} {
			if _, err := os.Stat(f); err != nil {
				t.Fatalf("mounted at %s: %s went", at, f)
			}
		}
	}
}
