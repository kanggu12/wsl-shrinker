package ext4

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// mkfs makes an ext4 image with e2fsprogs, the reference this package is
// checked against. Tests skip where e2fsprogs is not installed.
func mkfs(t *testing.T, size string, args ...string) string {
	t.Helper()
	for _, tool := range []string{"mkfs.ext4", "debugfs", "dumpe2fs"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed", tool)
		}
	}
	img := filepath.Join(t.TempDir(), "fs.img")
	run(t, "truncate", "-s", size, img)
	run(t, "mkfs.ext4", append(append([]string{"-q", "-F"}, args...), img)...)
	return img
}

// run runs a command and fails the test on error.
func run(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return string(out)
}

// reference returns what dumpe2fs counts: blocks and free blocks.
func reference(t *testing.T, img string) (blocks, free uint64) {
	t.Helper()
	for line := range strings.SplitSeq(run(t, "dumpe2fs", "-h", img), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		n, _ := strconv.ParseUint(strings.TrimSpace(v), 10, 64)
		switch k {
		case "Block count":
			blocks = n
		case "Free blocks":
			free = n
		}
	}
	return blocks, free
}

// read opens and reads an image.
func read(t *testing.T, img string) *FS {
	t.Helper()
	f, err := os.Open(img)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	fs, err := Read(f, st.Size())
	if err != nil {
		t.Fatal(err)
	}
	return fs
}

// check compares this package's count with dumpe2fs's.
func check(t *testing.T, img string) *FS {
	t.Helper()
	fs := read(t, img)
	blocks, free := reference(t, img)
	if fs.Blocks != blocks || fs.FreeBlocks != free {
		t.Fatalf("superblock: %d blocks, %d free; dumpe2fs says %d, %d", fs.Blocks, fs.FreeBlocks, blocks, free)
	}
	// The bitmaps must agree with the free count of a cleanly made image.
	if fs.UsedBlocks != blocks-free {
		t.Fatalf("bitmaps mark %d in use; dumpe2fs says %d", fs.UsedBlocks, blocks-free)
	}
	return fs
}

func TestLayouts(t *testing.T) {
	// The layouts mkfs makes by default and the ones with other features.
	cases := []struct {
		name, size string
		args       []string
	}{
		{"default", "256M", nil},
		{"64bit", "256M", []string{"-O", "64bit"}},
		{"no 64bit", "256M", []string{"-O", "^64bit"}},
		{"1 KiB blocks", "64M", []string{"-b", "1024"}},
		{"no flex_bg", "256M", []string{"-O", "^flex_bg"}},
		{"no sparse_super", "128M", []string{"-O", "^sparse_super,^resize_inode"}},
		{"sparse_super2", "512M", []string{"-O", "sparse_super2"}},
		{"many groups", "2G", nil},
		// Groups whose bitmaps were never written keep their own metadata
		// inside them when flex_bg does not move it.
		{"no flex_bg, uninitialised groups", "1G", []string{"-O", "^flex_bg"}},
		{"uninit_bg checksums, no flex_bg", "1G", []string{"-O", "^flex_bg,^metadata_csum,uninit_bg"}},
		// Without descriptor checksums the kernel ignores BLOCK_UNINIT.
		{"no descriptor checksums", "1G", []string{"-O", "^metadata_csum,^uninit_bg"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { check(t, mkfs(t, c.size, c.args...)) })
	}
}

func TestFilesWrittenAndRemoved(t *testing.T) {
	img := mkfs(t, "256M")
	before := check(t, img)
	// Write a 20 MiB file through debugfs, which needs no mount.
	src := filepath.Join(t.TempDir(), "data")
	if err := os.WriteFile(src, bytes.Repeat([]byte{1}, 20<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, "debugfs", "-w", "-R", "write "+src+" big", img)
	after := check(t, img)
	// 20 MiB of 4 KiB blocks, plus a little for the extent tree.
	grew := after.UsedBlocks - before.UsedBlocks
	if grew < 5120 || grew > 5200 {
		t.Fatalf("writing 20 MiB used %d more blocks", grew)
	}
	// Removing it gives the blocks back.
	run(t, "debugfs", "-w", "-R", "rm big", img)
	if got := check(t, img); got.UsedBlocks != before.UsedBlocks {
		t.Fatalf("after rm: %d in use, %d before", got.UsedBlocks, before.UsedBlocks)
	}
}

func TestAnyInUse(t *testing.T) {
	fs := read(t, mkfs(t, "64M"))
	// Block 0 holds the superblock, so the first block is in use.
	if !fs.InUse(0) || !fs.AnyInUse(0, 1) {
		t.Fatal("block 0 not in use")
	}
	// The word-at-a-time path and the bit path agree over the whole disk.
	for from := uint64(0); from < fs.Blocks; from += 97 {
		to := from + 300
		want := false
		for b := from; b < min(to, fs.Blocks); b++ {
			want = want || fs.InUse(b)
		}
		if got := fs.AnyInUse(from, to); got != want {
			t.Fatalf("AnyInUse(%d, %d) = %v, want %v", from, to, got, want)
		}
	}
	// Ranges past the end hold nothing.
	if fs.AnyInUse(fs.Blocks, fs.Blocks+100) {
		t.Fatal("blocks past the end in use")
	}
}

func TestNotExt4(t *testing.T) {
	// Zeros have no superblock magic.
	if _, err := Read(bytes.NewReader(make([]byte, 8192)), 8192); err == nil {
		t.Fatal("zeros read as ext4")
	}
}

// FuzzRead mutates a real image's first blocks: Read must fail or succeed,
// never panic, and a result must stay within the filesystem's size.
func FuzzRead(f *testing.F) {
	if _, err := exec.LookPath("mkfs.ext4"); err != nil {
		f.Skip("mkfs.ext4 not installed")
	}
	img := filepath.Join(f.TempDir(), "fs.img")
	if err := exec.Command("truncate", "-s", "16M", img).Run(); err != nil {
		f.Fatal(err)
	}
	if out, err := exec.Command("mkfs.ext4", "-q", "-F", img).CombinedOutput(); err != nil {
		f.Fatalf("%v: %s", err, out)
	}
	seed, err := os.ReadFile(img)
	if err != nil {
		f.Fatal(err)
	}
	// Overlays on the superblock and on the descriptor table.
	f.Add(int64(1024), seed[1024:1024+256])
	f.Add(int64(4096), seed[4096:4096+64])
	f.Fuzz(func(t *testing.T, at int64, patch []byte) {
		if at < 0 || at >= int64(len(seed)) {
			return
		}
		b := append([]byte(nil), seed...)
		copy(b[at:], patch)
		fs, err := Read(bytes.NewReader(b), int64(len(b)))
		if err != nil {
			return
		}
		if fs.UsedBlocks > fs.Blocks {
			t.Fatalf("%d blocks in use of %d", fs.UsedBlocks, fs.Blocks)
		}
	})
}
