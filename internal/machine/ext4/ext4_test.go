package ext4

import (
	"bytes"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	fs, err := Read(f, st.Size(), 0)
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
	if _, err := Read(bytes.NewReader(make([]byte, 8192)), 8192, 0); err == nil {
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
		fs, err := Read(bytes.NewReader(b), int64(len(b)), 0)
		if err != nil {
			return
		}
		if fs.UsedBlocks > fs.Blocks {
			t.Fatalf("%d blocks in use of %d", fs.UsedBlocks, fs.Blocks)
		}
	})
}

func TestDamagedInodeCountsAreRefused(t *testing.T) {
	// A superblock whose inode table size is far past a group's blocks:
	// each group without a bitmap would mark the rest of the filesystem one
	// block at a time (the work grows with groups times blocks), and every
	// block would read as in use. It is refused instead.
	for name, patch := range map[string]struct {
		at    int64
		value []byte
	}{
		"inodes per group": {1024 + 0x28, []byte{0xff, 0xff, 0xff, 0xff}},
		"inode size":       {1024 + 0x58, []byte{0x03, 0x01}}, // 259: not a power of two
	} {
		t.Run(name, func(t *testing.T) {
			img := mkfs(t, "8G")
			f, err := os.OpenFile(img, os.O_RDWR, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if _, err := f.WriteAt(patch.value, patch.at); err != nil {
				t.Fatal(err)
			}
			st, _ := f.Stat()
			if fs, err := Read(f, st.Size(), 0); err == nil {
				t.Fatalf("read with %d of %d blocks in use", fs.UsedBlocks, fs.Blocks)
			}
		})
	}
}

func TestChunksAnswerAsBlocksDo(t *testing.T) {
	// The callers ask per VHDX block (1 MiB for WSL's disks, up to 256 MiB
	// for others). Read per chunk of that size, every layout counts the
	// same blocks in use, and says of each chunk what reading per block
	// says of the blocks in it.
	for _, c := range []struct {
		size string
		args []string
	}{
		{"256M", nil},
		{"64M", []string{"-b", "1024"}},
		{"1G", []string{"-O", "^flex_bg"}},
		{"1G", []string{"-O", "^flex_bg,^metadata_csum,uninit_bg"}},
		{"512M", []string{"-O", "sparse_super2"}},
	} {
		img := mkfs(t, c.size, c.args...)
		// A file in it, so in-use blocks lie past the metadata too.
		src := filepath.Join(t.TempDir(), "data")
		if err := os.WriteFile(src, bytes.Repeat([]byte{1}, 3<<20), 0o644); err != nil {
			t.Fatal(err)
		}
		run(t, "debugfs", "-w", "-R", "write "+src+" f", img)
		fine := read(t, img)
		for _, chunk := range []int64{1 << 20, 32 << 20} {
			f, err := os.Open(img)
			if err != nil {
				t.Fatal(err)
			}
			st, _ := f.Stat()
			coarse, err := Read(f, st.Size(), chunk)
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
			if coarse.UsedBlocks != fine.UsedBlocks {
				t.Fatalf("%s %v, chunks of %d: %d in use, per block %d", c.size, c.args, chunk, coarse.UsedBlocks, fine.UsedBlocks)
			}
			per := uint64(chunk / fine.BlockSize)
			for from := uint64(0); from < fine.Blocks; from += per {
				if got, want := coarse.AnyInUse(from, from+per), fine.AnyInUse(from, from+per); got != want {
					t.Fatalf("%s %v, chunks of %d: chunk at block %d in use %v, per block %v", c.size, c.args, chunk, from, got, want)
				}
			}
		}
	}
}

func TestAHugeMadeUpFilesystemTakesLittleMemory(t *testing.T) {
	// A superblock that claims a 64 TiB filesystem of 1 KiB blocks, on a
	// sparse disk of that size: a few bytes of file. Read per block, that
	// is an 8 GiB bitmap and a 512 MiB descriptor table before anything is
	// checked. Read per 1 MiB chunk, as the callers do, a bit per chunk.
	const size = int64(64) << 40
	sb := make([]byte, 1024)
	le := binary.LittleEndian
	le.PutUint16(sb[0x38:], superblockMagic)
	blocks := uint64(size / 1024)
	le.PutUint32(sb[0x4:], uint32(blocks))
	le.PutUint32(sb[0x150:], uint32(blocks>>32))
	le.PutUint32(sb[0x60:], incompat64Bit)
	le.PutUint16(sb[0xFE:], 64)
	le.PutUint32(sb[0x14:], 1)
	le.PutUint32(sb[0x20:], 8192)
	le.PutUint32(sb[0x28:], 128)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, err := Read(sparseDisk{sb}, size, 1<<20)
	runtime.ReadMemStats(&after)
	// Its descriptors are zeros: refused at the first group, and cheaply.
	if err == nil {
		t.Fatal("a made-up filesystem was read")
	}
	if got := after.TotalAlloc - before.TotalAlloc; got > 32<<20 {
		t.Fatalf("reading it allocated %d MiB", got>>20)
	}
}

// sparseDisk reads as zeros everywhere but the superblock.
type sparseDisk struct{ sb []byte }

func (d sparseDisk) ReadAt(p []byte, off int64) (int, error) {
	clear(p)
	// The part of p that overlaps the superblock at 1024.
	for i := range p {
		if at := off + int64(i) - superblockOffset; at >= 0 && at < int64(len(d.sb)) {
			p[i] = d.sb[at]
		}
	}
	return len(p), nil
}

// layout is where an image's group descriptors are, from its superblock:
// the byte the table starts at, a descriptor's size, the blocks per group
// and the number of groups.
func layout(t *testing.T, img string) (gdt, descSize, perGroup, groups int64) {
	t.Helper()
	// The superblock alone: the image is mostly a hole of a gigabyte.
	f, err := os.Open(img)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sb := make([]byte, 1024)
	if _, err := f.ReadAt(sb, superblockOffset); err != nil {
		t.Fatal(err)
	}
	le := binary.LittleEndian
	blockSize := int64(1024) << le.Uint32(sb[0x18:])
	firstData := int64(le.Uint32(sb[0x14:]))
	perGroup = int64(le.Uint32(sb[0x20:]))
	blocks := int64(le.Uint32(sb[0x4:]))
	// 64-bit descriptors say their size; the others are 32 bytes.
	descSize = 32
	if le.Uint32(sb[0x60:])&incompat64Bit != 0 {
		blocks |= int64(le.Uint32(sb[0x150:])) << 32
		descSize = int64(le.Uint16(sb[0xFE:]))
	}
	groups = (blocks - firstData + perGroup - 1) / perGroup
	return (firstData + 1) * blockSize, descSize, perGroup, groups
}

func TestDescriptorsPastTheTablesFirstBlockAreRead(t *testing.T) {
	// Groups of 1024 blocks of 1 KiB: 64 groups, more than one block of
	// the descriptor table holds, so it is read again as the groups go.
	img := mkfs(t, "64M", "-b", "1024", "-g", "1024")
	_, descSize, _, groups := layout(t, img)
	if groups <= 1024/descSize {
		t.Fatalf("%d groups fit in one descriptor block: the case does not test what it says", groups)
	}
	check(t, img)
}

func TestABlockAGroupNamesTwiceIsInUseOnce(t *testing.T) {
	// Without flex_bg, a group whose bitmap was never written keeps its own
	// bitmaps and inode table, which count as in use. Damaged so that its
	// inode bitmap is its block bitmap, it names one block fewer, and the
	// block named twice is counted once.
	img := mkfs(t, "1G", "-O", "^flex_bg")
	gdt, descSize, _, groups := layout(t, img)
	before := read(t, img)
	f, err := os.OpenFile(img, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d := make([]byte, descSize)
	damaged := false
	// The last group whose bitmap was never written.
	for g := groups - 1; g > 0 && !damaged; g-- {
		if _, err := f.ReadAt(d, gdt+g*descSize); err != nil {
			t.Fatal(err)
		}
		if binary.LittleEndian.Uint16(d[0x12:])&bgBlockUninit == 0 {
			continue
		}
		// The inode bitmap's block (low half, and high half on 64-bit)
		// becomes the block bitmap's.
		copy(d[0x4:0x8], d[0x0:0x4])
		if descSize >= 64 {
			copy(d[0x24:0x28], d[0x20:0x24])
		}
		if _, err := f.WriteAt(d, gdt+g*descSize); err != nil {
			t.Fatal(err)
		}
		damaged = true
	}
	if !damaged {
		t.Fatal("no group with an unwritten bitmap: the case does not test what it says")
	}
	if after := read(t, img); after.UsedBlocks != before.UsedBlocks-1 {
		t.Fatalf("%d in use, was %d: the block named twice was not counted once", after.UsedBlocks, before.UsedBlocks)
	}
}
