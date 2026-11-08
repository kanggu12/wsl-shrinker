package survey

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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

// removal is the command for an image of id, run against made-up image
// lines: docker image ls answers with lines, rm says what it would remove.
func removal(t *testing.T, id string, lines ...string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the command is for Linux")
	}
	it := image([]string{"sha256:" + id, "x", "1GB", "1 week ago", "0"})
	cmd := strings.Replace(it.Command, "docker image ls --no-trunc --format '{{.Repository}}:{{.Tag}} {{.ID}}'", "printf '%s\\n' "+"'"+strings.Join(lines, "' '")+"'", 1)
	cmd = strings.Replace(cmd, "xargs -r docker image rm", "xargs -r echo rm", 1)
	out, err := exec.Command("sh", "-c", cmd).CombinedOutput()
	if err != nil {
		t.Fatalf("%v %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestAnImageKnownOnlyByItsDigestIsRemovedByID(t *testing.T) {
	// "name:<none>" names nothing docker can remove: the id is used.
	id := "4f8a2c1d9e7b"
	full := "sha256:4f8a2c1d9e7b" + strings.Repeat("0", 52)
	if got := removal(t, id, "ghcr.io/a/b:<none> "+full); got != "rm "+full {
		t.Fatalf("%q", got)
	}
	// With tags of its own, the tags are removed, which removes the image;
	// the id beside them would fail once they are gone.
	if got := removal(t, id, "node:22 "+full, "node:<none> "+full); got != "rm node:22" {
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
	script := "mount --bind " + data + " " + filepath.Join(cache, "inner") + " && " + removeContents(cache)
	out, err := exec.Command("unshare", "-m", "--propagation", "private", "sh", "-c", script).CombinedOutput()
	if strings.Contains(string(out), "Operation not permitted") {
		t.Skipf("no mount namespace here: %s", out)
	}
	if err == nil || !strings.Contains(string(out), "something is mounted inside") {
		t.Fatalf("went on: %v %s", err, out)
	}
	for _, f := range []string{filepath.Join(data, "keep.txt"), filepath.Join(cache, "a.whl")} {
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("%s went", f)
		}
	}
}
