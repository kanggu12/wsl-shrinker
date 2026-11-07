package estimate

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/kanggu12/wsl-shrinker/internal/machine/ext4"
	"github.com/kanggu12/wsl-shrinker/internal/machine/vhdx"
	"github.com/kanggu12/wsl-shrinker/internal/testutil/vhdxtest"
)

// disk writes a VHDX holding the blocks given and reads it back.
func disk(t *testing.T, s vhdxtest.Spec) (*vhdx.Disk, int64) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "d.vhdx")
	size := vhdxtest.Write(t, path, s)
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	d, err := vhdx.Read(f, size)
	if err != nil {
		t.Fatal(err)
	}
	return d, size
}

func TestWithoutFilesystem(t *testing.T) {
	// Three stored blocks; a compaction alone keeps them all.
	d, size := disk(t, vhdxtest.Spec{VirtualSize: 16 << 20, Blocks: map[int64][]byte{0: {1}, 4: {1}, 9: {1}}})
	r := Of(d, nil, size+(40<<20))
	if r.Filesystem || r.Stored != 3<<20 || r.After != d.CompactedSize(3) {
		t.Fatalf("%+v", r)
	}
	// The host file was 40 MiB larger than its blocks: that comes back.
	if r.Reclaim != size+(40<<20)-r.After {
		t.Fatalf("reclaim %d", r.Reclaim)
	}
}

func TestStoredButFree(t *testing.T) {
	if _, err := exec.LookPath("mkfs.ext4"); err != nil {
		t.Skip("mkfs.ext4 not installed")
	}
	// A real ext4 filesystem, stored block by block in a VHDX; then two
	// extra stored blocks past everything the filesystem uses.
	img := filepath.Join(t.TempDir(), "fs.img")
	for _, c := range [][]string{{"truncate", "-s", "64M", img}, {"mkfs.ext4", "-q", "-F", "-E", "lazy_itable_init=0", img}} {
		if out, err := exec.Command(c[0], c[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	raw, err := os.ReadFile(img)
	if err != nil {
		t.Fatal(err)
	}
	blocks := map[int64][]byte{}
	for i := int64(0); i < 64; i++ {
		// Store only blocks that are not all zeros, as a guest's writes would.
		chunk := raw[i<<20 : (i+1)<<20]
		for _, b := range chunk {
			if b != 0 {
				blocks[i] = chunk
				break
			}
		}
	}
	free := []int64{}
	for i := int64(63); i >= 0 && len(free) < 2; i-- {
		if _, ok := blocks[i]; !ok {
			blocks[i], free = []byte("deleted data"), append(free, i)
		}
	}
	d, size := disk(t, vhdxtest.Spec{VirtualSize: 64 << 20, Blocks: blocks})
	fs, err := ext4.Read(d.Contents(), d.VirtualSize)
	if err != nil {
		t.Fatal(err)
	}
	r := Of(d, fs, size)
	// The two blocks hold nothing in use: a trim frees them.
	if r.Trimmable != 2<<20 {
		t.Fatalf("trimmable %d, want 2 MiB (%+v)", r.Trimmable, r)
	}
	if r.After != d.CompactedSize(len(blocks)-2) || r.Reclaim != size-r.After {
		t.Fatalf("%+v", r)
	}
	if !r.Filesystem || r.Used != int64(fs.UsedBlocks)*fs.BlockSize {
		t.Fatalf("filesystem numbers: %+v", r)
	}
}

func TestNeverNegative(t *testing.T) {
	// A host file smaller than the estimate (sparse on the host).
	d, _ := disk(t, vhdxtest.Spec{VirtualSize: 4 << 20, Blocks: map[int64][]byte{0: {1}}})
	if r := Of(d, nil, 1<<20); r.Reclaim != 0 {
		t.Fatalf("reclaim %d", r.Reclaim)
	}
}
