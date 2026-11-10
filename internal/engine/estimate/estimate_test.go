package estimate

import (
	"encoding/json"
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
	fs, err := ext4.Read(d.Contents(), d.VirtualSize, int64(d.BlockSize))
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

// ext4Disk writes a 64 MiB ext4 filesystem into a VHDX, storing only its
// non-zero blocks, plus the extra blocks given past everything it uses, and
// reads both back.
func ext4Disk(t *testing.T, extra map[int64][]byte) (*vhdx.Disk, *ext4.FS, int64, int) {
	t.Helper()
	return ext4DiskAs(t, extra, nil)
}

// ext4DiskAs is ext4Disk with the file's description passed through shape
// before it is written, for a disk that is not the usual kind; the count it
// returns is of the blocks stored after that.
func ext4DiskAs(t *testing.T, extra map[int64][]byte, shape func(*vhdxtest.Spec)) (*vhdx.Disk, *ext4.FS, int64, int) {
	t.Helper()
	if _, err := exec.LookPath("mkfs.ext4"); err != nil {
		t.Skip("mkfs.ext4 not installed")
	}
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
		chunk := raw[i<<20 : (i+1)<<20]
		for _, b := range chunk {
			if b != 0 {
				blocks[i] = chunk
				break
			}
		}
	}
	// The extra blocks go into the last free places, from the end.
	free := int64(63)
	for _, data := range extra {
		for ; free >= 0; free-- {
			if _, ok := blocks[free]; !ok {
				break
			}
		}
		blocks[free] = data
		free--
	}
	spec := vhdxtest.Spec{VirtualSize: 64 << 20, Blocks: blocks}
	if shape != nil {
		shape(&spec)
	}
	d, size := disk(t, spec)
	fs, err := ext4.Read(d.Contents(), d.VirtualSize, int64(d.BlockSize))
	if err != nil {
		t.Fatal(err)
	}
	return d, fs, size, len(spec.Blocks)
}

func TestWithoutTrimOnlyZeroBlocksGo(t *testing.T) {
	// Windows' own compaction of a file WSL does not register runs without
	// a trim: it drops stored blocks that hold only zeros, nothing else. Two
	// free blocks still hold deleted data, three are stored zeros.
	extra := map[int64][]byte{1: []byte("deleted data"), 2: []byte("more deleted data"), 3: {}, 4: {}, 5: {}}
	d, fs, size, n := ext4Disk(t, extra)
	r := OfUntrimmed(d, fs, size)
	// The deleted data stays (no trim frees it); the zeros go.
	if r.After != d.CompactedSize(n-3) || r.Reclaim != size-r.After {
		t.Fatalf("after %d, want %d (%+v)", r.After, d.CompactedSize(n-3), r)
	}
	// With a trim, all five would go: the two estimates differ.
	if w := Of(d, fs, size); w.After != d.CompactedSize(n-5) {
		t.Fatalf("with a trim: %+v", w)
	}
}

func TestWithoutTrimScanStopsAtItsLimit(t *testing.T) {
	// Past the bytes the scan may read, a block is counted as kept: the
	// estimate errs towards giving back less.
	extra := map[int64][]byte{3: {}, 4: {}, 5: {}}
	d, fs, size, n := ext4Disk(t, extra)
	defer func(old int64) { zeroScanLimit = old }(zeroScanLimit)
	zeroScanLimit = 1 << 20
	if r := OfUntrimmed(d, fs, size); r.After != d.CompactedSize(n-1) {
		t.Fatalf("after %d, want %d with one block scanned", r.After, d.CompactedSize(n-1))
	}
}

func TestWithoutTrimOrFilesystemEveryBlockStays(t *testing.T) {
	// Without a filesystem to say which blocks are free, none is read.
	d, size := disk(t, vhdxtest.Spec{VirtualSize: 16 << 20, Blocks: map[int64][]byte{0: {1}, 4: {}, 9: {1}}})
	if r := OfUntrimmed(d, nil, size); r.After != d.CompactedSize(3) {
		t.Fatalf("%+v", r)
	}
}

func TestAFixedSizeDiskGivesNothingBack(t *testing.T) {
	// Windows' compaction of a fixed-size disk reports success and takes
	// nothing out of the file (measured on Windows, in winapi's tests). So
	// neither estimate may promise anything: not the free blocks with deleted
	// data that a trim drops, not the blocks of zeros.
	extra := map[int64][]byte{1: []byte("deleted data"), 2: []byte("more deleted data"), 3: {}, 4: {}, 5: {}}
	// Every block stored, as in a fixed-size disk: the ones the filesystem
	// never wrote hold zeros.
	whole := func(s *vhdxtest.Spec) {
		for i := int64(0); i < 64; i++ {
			if _, ok := s.Blocks[i]; !ok {
				s.Blocks[i] = []byte{}
			}
		}
	}
	d, fs, size, n := ext4DiskAs(t, extra, func(s *vhdxtest.Spec) { whole(s); s.Fixed = true })
	if !d.Fixed || n != 64 {
		t.Fatalf("fixed %v, %d blocks stored", d.Fixed, n)
	}
	// The file on the host is as large as the disk inside it, and more.
	if size < 64<<20 {
		t.Fatalf("a file of %d bytes", size)
	}
	for name, r := range map[string]Result{"with a trim": Of(d, fs, size), "without one": OfUntrimmed(d, fs, size)} {
		if !r.Fixed || r.After != size || r.Reclaim != 0 || r.Trimmable != 0 {
			t.Errorf("%s: %+v", name, r)
		}
		// What the file holds and the filesystem uses is still said.
		if !r.Filesystem || r.Stored != 64<<20 || r.Used != int64(fs.UsedBlocks)*fs.BlockSize {
			t.Errorf("%s: %+v", name, r)
		}
	}
	// A host file larger than the disk's own does not come back either.
	if r := Of(d, fs, size+(40<<20)); r.After != size+(40<<20) || r.Reclaim != 0 {
		t.Errorf("a padded file: %+v", r)
	}
	// The flag alone decides: the same blocks in a disk that grows come
	// back, with a trim and without.
	g, gfs, gsize, _ := ext4DiskAs(t, extra, whole)
	if r := Of(g, gfs, gsize); r.Fixed || r.Reclaim < 5<<20 || r.Trimmable < 5<<20 {
		t.Errorf("one that grows, with a trim: %+v", r)
	}
	if r := OfUntrimmed(g, gfs, gsize); r.Fixed || r.Reclaim < 3<<20 {
		t.Errorf("one that grows, without: %+v", r)
	}
}

func TestResultJSONKeys(t *testing.T) {
	b, err := json.Marshal(Result{})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"file_size_bytes":0,"after_bytes":0,"reclaim_bytes":0,"stored_bytes":0,"used_bytes":0,"trimmable_bytes":0,"in_use":false,"mounted":false,"fs_errors":false,"filesystem":false,"trimmed":false,"fixed":false}`
	if string(b) != want {
		t.Fatalf("got  %s\nwant %s", b, want)
	}
}
