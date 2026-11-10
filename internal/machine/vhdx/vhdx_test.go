package vhdx

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kanggu12/wsl-shrinker/internal/testutil/vhdxtest"
)

// open writes a test file from s and reads it back.
func open(t *testing.T, s vhdxtest.Spec) (*Disk, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "disk.vhdx")
	size := vhdxtest.Write(t, path, s)
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return Read(f, size)
}

func TestReadsBlocksAndContents(t *testing.T) {
	// Two blocks with data, one recorded as zero, the rest never written.
	a := bytes.Repeat([]byte{0xaa}, 1<<20)
	b := []byte("tail of block five")
	d, err := open(t, vhdxtest.Spec{
		VirtualSize: 8 << 20,
		Blocks:      map[int64][]byte{1: a, 5: b},
		ZeroBlocks:  []int64{2},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Geometry comes from the metadata.
	if d.VirtualSize != 8<<20 || d.BlockSize != 1<<20 || d.LogicalSectorSize != 512 || d.PhysicalSectorSize != 4096 {
		t.Fatalf("geometry: %+v", d)
	}
	// Only the two stored blocks count.
	if got := d.PresentBlocks(); got != 2 || len(d.Blocks) != 8 {
		t.Fatalf("present %d of %d, want 2 of 8", got, len(d.Blocks))
	}
	// Reading across blocks 0 to 2 gives zeros, the data, zeros.
	buf := make([]byte, 3<<20)
	if n, err := d.Contents().ReadAt(buf, 0); err != nil || n != len(buf) {
		t.Fatalf("read %d, %v", n, err)
	}
	if !allZero(buf[:1<<20]) || !bytes.Equal(buf[1<<20:2<<20], a) || !allZero(buf[2<<20:]) {
		t.Fatal("contents of blocks 0 to 2 are wrong")
	}
	// A read that starts inside a block and ends inside another.
	part := make([]byte, len(b)+10)
	if _, err := d.Contents().ReadAt(part, 5<<20-10); err != nil {
		t.Fatal(err)
	}
	if !allZero(part[:10]) || !bytes.Equal(part[10:], b) {
		t.Fatalf("straddling read: %q", part)
	}
}

func TestReadPastTheEnd(t *testing.T) {
	d, err := open(t, vhdxtest.Spec{VirtualSize: 3 << 20})
	if err != nil {
		t.Fatal(err)
	}
	// A read running over the end returns what exists and io.EOF.
	buf := make([]byte, 100)
	n, err := d.Contents().ReadAt(buf, 3<<20-40)
	if n != 40 || err != io.EOF {
		t.Fatalf("got %d, %v; want 40, EOF", n, err)
	}
	// A read starting at the end returns nothing.
	if n, err := d.Contents().ReadAt(buf, 3<<20); n != 0 || err != io.EOF {
		t.Fatalf("got %d, %v at the end", n, err)
	}
}

func TestSectorBitmapEntriesAreSkipped(t *testing.T) {
	// 512-byte sectors and 256 MiB blocks: a bitmap entry after every 16
	// payload entries, so block 20 is BAT entry 21.
	data := []byte("block twenty")
	d, err := open(t, vhdxtest.Spec{
		VirtualSize: 24 * 256 << 20,
		BlockSize:   256 << 20,
		Blocks:      map[int64][]byte{20: data},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !d.Blocks[20].Present() || d.PresentBlocks() != 1 {
		t.Fatalf("block 20 not found where it was written: %+v", d.Blocks[16:22])
	}
	got := make([]byte, len(data))
	if _, err := d.Contents().ReadAt(got, 20*256<<20); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("read %q, %v", got, err)
	}
}

func TestHeaderChoice(t *testing.T) {
	// The copy with the higher sequence number wins, whichever slot it is in.
	for _, second := range []bool{false, true} {
		if _, err := open(t, vhdxtest.Spec{VirtualSize: 2 << 20, SeqHigherIn2: second}); err != nil {
			t.Fatalf("higher in second slot %v: %v", second, err)
		}
	}
	// A copy with a bad checksum is passed over, not trusted.
	if _, err := open(t, vhdxtest.Spec{VirtualSize: 2 << 20, BreakHeader1: true}); err != nil {
		t.Fatalf("first header broken: %v", err)
	}
	// A bad first region table falls back to the second.
	if _, err := open(t, vhdxtest.Spec{VirtualSize: 2 << 20, BreakRegion1: true}); err != nil {
		t.Fatalf("first region table broken: %v", err)
	}
}

func TestRefusals(t *testing.T) {
	// A differencing disk reads from a parent this package cannot follow.
	if _, err := open(t, vhdxtest.Spec{VirtualSize: 2 << 20, Parent: true}); err == nil || !strings.Contains(err.Error(), "differencing") {
		t.Fatalf("parent: %v", err)
	}
	// Not a VHDX file at all.
	path := filepath.Join(t.TempDir(), "plain")
	if err := os.WriteFile(path, make([]byte, 1<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	f, _ := os.Open(path)
	defer f.Close()
	if _, err := Read(f, 1<<20); err == nil {
		t.Fatal("a zero-filled file was read as VHDX")
	}
}

func TestLogReplay(t *testing.T) {
	data := []byte("written, BAT entry still in the log")
	for _, c := range []struct {
		name string
		spec vhdxtest.Spec
	}{
		{"plain", vhdxtest.Spec{}},
		{"wrapping", vhdxtest.Spec{LogWrap: true}},
		{"with an older log's entry", vhdxtest.Spec{StaleLogEntry: true}},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Block 3 is stored, but only the log says so.
			s := c.spec
			s.VirtualSize, s.Blocks, s.InLogOnly = 8<<20, map[int64][]byte{1: []byte("x"), 3: data}, []int64{3}
			d, err := open(t, s)
			if err != nil {
				t.Fatal(err)
			}
			if !d.LogActive || d.LogEntries != 1 {
				t.Fatalf("log active %v, %d entries replayed", d.LogActive, d.LogEntries)
			}
			// Both blocks are found, and block 3 reads back.
			if d.PresentBlocks() != 2 || !d.Blocks[3].Present() {
				t.Fatalf("present: %d, block 3 %+v", d.PresentBlocks(), d.Blocks[3])
			}
			got := make([]byte, len(data))
			if _, err := d.Contents().ReadAt(got, 3<<20); err != nil || !bytes.Equal(got, data) {
				t.Fatalf("block 3 reads %q, %v", got, err)
			}
		})
	}
	// An active log with nothing in it reads as the file is.
	d, err := open(t, vhdxtest.Spec{VirtualSize: 2 << 20, LogActive: true, Blocks: map[int64][]byte{0: []byte("y")}})
	if err != nil || !d.LogActive || d.LogEntries != 0 || d.PresentBlocks() != 1 {
		t.Fatalf("empty log: %+v, %v", d, err)
	}
	// A file shorter than its log says was flushed is damaged.
	if _, err := open(t, vhdxtest.Spec{VirtualSize: 8 << 20, Blocks: map[int64][]byte{3: data}, InLogOnly: []int64{3}, TruncateLog: true}); err == nil {
		t.Fatal("a truncated file was read")
	}
}

func TestDamagedSizesAreRefused(t *testing.T) {
	// A virtual size near the int64 limit: refused, not a panic.
	path := filepath.Join(t.TempDir(), "d.vhdx")
	vhdxtest.Write(t, path, vhdxtest.Spec{VirtualSize: 4 << 20})
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	// The disk size item is the second value, 64 bytes after the first, in
	// the metadata region at 2 MiB whose values start at 64 KiB.
	v := make([]byte, 8)
	binary.LittleEndian.PutUint64(v, 1<<63-1)
	if _, err := f.WriteAt(v, 2<<20+64<<10+64); err != nil {
		t.Fatal(err)
	}
	st, _ := f.Stat()
	if _, err := Read(f, st.Size()); err == nil || !strings.Contains(err.Error(), "64 TiB") {
		t.Fatalf("got %v", err)
	}
}

func TestHugeZeroRunIsRefused(t *testing.T) {
	// One log entry whose single "zero" descriptor claims 4 GiB: a replay
	// would hold a million pages. The entry is not a valid one.
	path := filepath.Join(t.TempDir(), "d.vhdx")
	size := vhdxtest.Write(t, path, vhdxtest.Spec{VirtualSize: 4 << 20, LogActive: true})
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	e := make([]byte, 4096)
	copy(e[0:4], "loge")
	binary.LittleEndian.PutUint32(e[8:12], 4096) // length
	binary.LittleEndian.PutUint64(e[16:24], 1)   // sequence
	binary.LittleEndian.PutUint32(e[24:28], 1)   // one descriptor
	e[32] = 0x5a                                 // the log GUID vhdxtest writes
	binary.LittleEndian.PutUint64(e[48:56], uint64(size))
	binary.LittleEndian.PutUint64(e[56:64], uint64(size))
	d := e[64:96]
	copy(d[0:4], "zero")
	binary.LittleEndian.PutUint64(d[8:16], 4<<30)
	binary.LittleEndian.PutUint64(d[16:24], 1<<40)
	binary.LittleEndian.PutUint64(d[24:32], 1)
	binary.LittleEndian.PutUint32(e[4:8], crc32.Checksum(e, crc32.MakeTable(crc32.Castagnoli)))
	if _, err := f.WriteAt(e, 1<<20); err != nil {
		t.Fatal(err)
	}
	disk, err := Read(f, size)
	// Without a valid entry the log has nothing to replay.
	if err != nil || disk.LogEntries != 0 {
		t.Fatalf("replayed %v entries, %v", disk, err)
	}
}

func TestGUIDByteOrder(t *testing.T) {
	// The BAT region GUID as it appears in real files.
	want := []byte{0x66, 0x77, 0xc2, 0x2d, 0x23, 0xf6, 0x00, 0x42, 0x9d, 0x64, 0x11, 0x5e, 0x9b, 0xfd, 0x4a, 0x08}
	if g := regionBAT; !bytes.Equal(g[:], want) {
		t.Fatalf("got % x", g)
	}
}

// FuzzRead feeds arbitrary bytes behind a valid file's start: Read must
// return an error or a disk, never panic or hang.
func FuzzRead(f *testing.F) {
	path := filepath.Join(f.TempDir(), "seed.vhdx")
	size := vhdxtest.Write(f, path, vhdxtest.Spec{VirtualSize: 4 << 20, Blocks: map[int64][]byte{1: []byte("x"), 2: []byte("z")}, InLogOnly: []int64{2}})
	seed, err := os.ReadFile(path)
	if err != nil {
		f.Fatal(err)
	}
	// Seed with the parts of the file a mutation would hit: headers, region
	// table, metadata table and the BAT, each as an overlay at its place.
	f.Add(int64(64<<10), seed[64<<10:64<<10+256])
	f.Add(int64(192<<10), seed[192<<10:192<<10+128])
	f.Add(int64(2<<20), seed[2<<20:2<<20+256])
	f.Add(int64(3<<20), seed[3<<20:3<<20+64])
	f.Add(int64(1<<20), seed[1<<20:1<<20+128])
	f.Add(int64(1<<20+4096), seed[1<<20+4096:1<<20+4096+64])
	f.Fuzz(func(t *testing.T, at int64, patch []byte) {
		// Overlay the patch on a copy of the valid file.
		if at < 0 || at >= size {
			return
		}
		b := append([]byte(nil), seed...)
		copy(b[at:], patch)
		d, err := Read(bytes.NewReader(b), size)
		if err != nil {
			return
		}
		// A disk that parsed must also be readable end to end.
		buf := make([]byte, 64<<10)
		r := d.Contents()
		for off := int64(0); off < d.VirtualSize; off += int64(len(buf)) {
			if _, err := r.ReadAt(buf, off); err != nil && err != io.EOF {
				t.Fatalf("parsed disk unreadable at %d: %v", off, err)
			}
		}
	})
}

func TestCompactedSize(t *testing.T) {
	// The layout WSL makes: log, metadata and BAT in the first 12 MiB, so a
	// compacted file is 12 MiB plus its blocks (measured: 101,711,872 bytes
	// for 85 blocks of 1 MiB).
	d := &Disk{BlockSize: 1 << 20, LogOffset: 1 << 20, LogLength: 1 << 20, MetadataOffset: 2 << 20, MetadataLength: 1 << 20,
		BATOffset: 3 << 20, BATLength: 9 << 20}
	if got := d.CompactedSize(85); got != 101711872 {
		t.Fatalf("got %d", got)
	}
	// A BAT moved far into the file: the blocks still pack below it, and the
	// file cannot end before the BAT does.
	d.BATOffset = 120 << 30
	if got := d.CompactedSize(85); got != 120<<30+9<<20 {
		t.Fatalf("BAT at 120 GiB, few blocks: got %d", got)
	}
	if got := d.CompactedSize(400 << 10); got != (12<<20)+(400<<30) {
		t.Fatalf("BAT at 120 GiB, many blocks: got %d", got)
	}
}

func TestDamagedLogIsReadInBoundedTime(t *testing.T) {
	// A damaged log where every sector looks like the start of an entry as
	// long as the whole log: each would be checksummed in full, so the work
	// grows with the square of the log's size. It must stay bounded.
	const logLen = 16 << 20
	id := bytes.Repeat([]byte{0xab}, 16)
	log := make([]byte, logLen)
	for at := 0; at < logLen; at += logSector {
		copy(log[at:at+4], "loge")
		binary.LittleEndian.PutUint32(log[at+8:at+12], logLen)
		copy(log[at+32:at+48], id)
	}
	start := time.Now()
	_, _, _, err := replay(bytes.NewReader(log), logLen, id, 0, logLen)
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("took %v (err %v)", took, err)
	}
}

func TestDamagedLogFieldsAreRefused(t *testing.T) {
	// A header naming a log outside the file, with no log in use: refused,
	// rather than counted in the compacted size.
	if _, err := open(t, vhdxtest.Spec{VirtualSize: 4 << 20, HeaderLogOffset: 1 << 62}); err == nil {
		t.Fatal("a log region past the end of the file was accepted")
	}
	// A log entry claiming the file is far longer than it is: refused,
	// rather than taken as the file's size (which would let a BAT entry
	// point anywhere).
	data := []byte("in the log")
	if _, err := open(t, vhdxtest.Spec{VirtualSize: 8 << 20, Blocks: map[int64][]byte{3: data}, InLogOnly: []int64{3}, LastFileOffset: 1 << 50}); err == nil {
		t.Fatal("a log claiming a 1 PiB file was accepted")
	}
}

func TestAFixedSizeDiskIsToldFromOneThatGrows(t *testing.T) {
	// Bit 0 of the file parameters' flags (LeaveBlockAllocated) is what makes
	// a disk fixed-size: the same blocks in the same places read as a disk
	// that grows without it.
	blocks := map[int64][]byte{0: {1}, 1: {}}
	fixed, err := open(t, vhdxtest.Spec{VirtualSize: 2 << 20, Blocks: blocks, Fixed: true})
	if err != nil {
		t.Fatal(err)
	}
	grows, err := open(t, vhdxtest.Spec{VirtualSize: 2 << 20, Blocks: blocks})
	if err != nil {
		t.Fatal(err)
	}
	if !fixed.Fixed || grows.Fixed {
		t.Fatalf("fixed-size reads %v, one that grows %v", fixed.Fixed, grows.Fixed)
	}
	// The bit refuses nothing and is not taken for the one beside it: the
	// disk has no parent, and its blocks read as any other's.
	if fixed.HasParent || fixed.PresentBlocks() != 2 {
		t.Fatalf("parent %v, %d blocks stored", fixed.HasParent, fixed.PresentBlocks())
	}
	got := make([]byte, 1)
	if _, err := fixed.Contents().ReadAt(got, 0); err != nil || got[0] != 1 {
		t.Fatalf("read %v, %v", got, err)
	}
}

func TestTheLogsSequenceSearchHasABudget(t *testing.T) {
	// A damaged log: a long chain of entries from offset 0, and as many
	// heads off the chain that name its start as their tail. Each head
	// walks the whole chain and fails at its end: without a budget that
	// is the chain's length times the heads, quadratic in the log.
	const n = 2048
	entries := map[int64]*logEntry{}
	for i := int64(0); i < n; i++ {
		entries[i*logSector] = &logEntry{at: i * logSector, length: logSector, tail: 0, seq: uint64(i)}
	}
	logLen := int64(4 * n * logSector)
	for i := int64(0); i < n; i++ {
		at := (n + 1 + i) * logSector
		entries[at] = &logEntry{at: at, length: logSector, tail: 0, seq: uint64(10*n + i)}
	}
	// The search gives up, and says the log is damaged, long before every
	// head has walked the chain.
	if run, err := activeSequence(entries, logLen); err == nil || !strings.Contains(err.Error(), "form no sequence") {
		t.Fatalf("the search walked every head to the end: %d entries, %v", len(run), err)
	}
	// A real log: its newest head follows the chain, found in one walk.
	good := map[int64]*logEntry{}
	for i := int64(0); i < n; i++ {
		good[i*logSector] = &logEntry{at: i * logSector, length: logSector, tail: 0, seq: uint64(i)}
	}
	if run, err := activeSequence(good, logLen); err != nil || len(run) != n || run[n-1] != good[(n-1)*logSector] {
		t.Fatalf("a real log: %d entries, %v", len(run), err)
	}
}
