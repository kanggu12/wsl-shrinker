// Package vhdx reads the parts of a VHDX file that say where its data lives,
// following the published format ([MS-VHDX]): the headers, the region table,
// the metadata and the block allocation table (BAT). It never writes.
//
// A WSL disk is a dynamic VHDX: the virtual disk is cut into payload blocks
// (1 MiB for WSL), and a block takes space in the file only once something
// was written to it. The BAT says, for every payload block, whether it is in
// the file and where. That is what the size of a compacted copy depends on.
package vhdx

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
)

// Fixed places in every VHDX file (MS-VHDX 2.2): the file identifier, the two
// copies of the header and the two copies of the region table.
const (
	headerOffset1 = 64 << 10  // first header copy, at 64 KiB
	headerOffset2 = 128 << 10 // second header copy, at 128 KiB
	headerSize    = 4 << 10   // a header is 4 KiB, checksummed as a whole
	regionOffset1 = 192 << 10 // first region table copy
	regionOffset2 = 256 << 10 // second region table copy
	regionSize    = 64 << 10  // a region table is 64 KiB, checksummed as a whole
	metaTableSize = 64 << 10  // the metadata table at the start of its region
	mib           = 1 << 20   // BAT entries store file offsets in MiB
)

// BAT entry states (MS-VHDX 2.5.1.1). Only the low three bits carry the state.
const (
	stateNotPresent    = 0 // never written: reads as zeros (no parent)
	stateUndefined     = 1 // contents undefined: reads as zeros here
	stateZero          = 2 // explicitly zero
	stateUnmapped      = 3 // trimmed by the guest: reads as zeros
	stateFullyPresent  = 6 // the whole block is in the file
	statePartlyPresent = 7 // differencing disks only: part comes from a parent
)

// Region and metadata item identifiers (MS-VHDX 2.3 and 2.6), written here in
// the order their bytes appear in the file (the first three fields of a GUID
// are little-endian on disk).
var (
	regionBAT      = guid("2DC27766-F623-4200-9D64-115E9BFD4A08")
	regionMetadata = guid("8B7CA206-4790-4B9A-B8FE-575F050F886E")
	itemFileParams = guid("CAA16737-FA36-4D43-B3B6-33F0AA44E76B")
	itemDiskSize   = guid("2FA54224-CD1B-4876-B211-5DBED83BF4B8")
	itemLogical    = guid("8141BF1D-A96F-4709-BA47-F233A8FAAB5F")
	itemPhysical   = guid("CDA348C7-445D-4471-9CC9-E9885251C556")
	itemPage83     = guid("BECA12AB-B2E6-4523-93EF-C309E000C746")
	itemParentLoc  = guid("A8D35F2D-B30B-454D-ABF7-D3D84834AB0C")
)

// castagnoli is the CRC-32C table every VHDX checksum uses.
var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// Disk is what the headers, the metadata and the BAT say about one file.
type Disk struct {
	// FileSize is the size of the file on the host.
	FileSize int64
	// VirtualSize is the size of the disk the guest sees.
	VirtualSize int64
	// BlockSize is the size of a payload block (1 MiB for WSL disks).
	BlockSize uint32
	// LogicalSectorSize and PhysicalSectorSize are the guest's sector sizes.
	LogicalSectorSize, PhysicalSectorSize uint32
	// HasParent is set for a differencing disk, which this package refuses.
	HasParent bool
	// LogActive is set when the header names a log: the file is open (WSL
	// runs on it) or was not closed cleanly. LogEntries counts the entries
	// replayed in memory to read it as Windows would.
	LogActive  bool
	LogEntries int
	// BATOffset and BATLength place the BAT region in the file.
	BATOffset int64
	BATLength uint32
	// MetadataOffset and MetadataLength place the metadata region.
	MetadataOffset int64
	MetadataLength uint32
	// LogOffset and LogLength place the log region.
	LogOffset int64
	LogLength uint32
	// Blocks holds one entry per payload block, in virtual order.
	Blocks []Block
	// src reads the file, with the log replayed when it was active.
	src io.ReaderAt
}

// Block is one payload block's entry in the BAT.
type Block struct {
	// State is the raw BAT state (see the state constants).
	State uint8
	// Offset is where the block starts in the file, or -1 when it has none.
	Offset int64
}

// Present reports whether the block takes space in the file.
func (b Block) Present() bool { return b.State == stateFullyPresent }

// CompactedSize is the size of the file after a compaction that keeps n
// payload blocks. A compaction moves blocks down into free space, so the
// file holds its first MiB (identifier, headers, region tables), its
// regions and the blocks, packed. A region placed past that (a file whose
// BAT was moved, as an interrupted compaction can leave it) keeps the file
// at least as long as the region's end.
func (d *Disk) CompactedSize(n int) int64 {
	regions := []struct{ off, length int64 }{
		{d.LogOffset, int64(d.LogLength)},
		{d.MetadataOffset, int64(d.MetadataLength)},
		{d.BATOffset, int64(d.BATLength)},
	}
	packed := int64(mib) // the identifier, headers and region tables
	end := int64(mib)
	for _, r := range regions {
		// Each region takes whole MiB, as payload blocks are MiB-aligned.
		packed += (r.length + mib - 1) / mib * mib
		end = max(end, (r.off+r.length+mib-1)/mib*mib)
	}
	return max(end, packed+int64(n)*int64(d.BlockSize))
}

// PresentBlocks counts the payload blocks that take space in the file.
func (d *Disk) PresentBlocks() int {
	// Count every block whose data is stored in the file.
	n := 0
	for _, b := range d.Blocks {
		if b.Present() {
			n++
		}
	}
	return n
}

// Read parses the file behind r, whose size is size. A file with an active
// log is read with the log replayed in memory. It refuses anything it does
// not fully understand, rather than guess.
func Read(r io.ReaderAt, size int64) (*Disk, error) {
	// The file identifier comes first; anything else is not a VHDX file.
	sig := make([]byte, 8)
	if _, err := r.ReadAt(sig, 0); err != nil {
		return nil, fmt.Errorf("vhdx: reading the file identifier: %w", err)
	}
	if string(sig) != "vhdxfile" {
		return nil, errors.New("vhdx: not a VHDX file")
	}
	// Pick the current header: of the copies with a valid checksum, the one
	// with the higher sequence number (MS-VHDX 2.2.2.1).
	h, err := currentHeader(r)
	if err != nil {
		return nil, err
	}
	d := &Disk{FileSize: size, BATOffset: -1, MetadataOffset: -1,
		LogOffset: int64(binary.LittleEndian.Uint64(h[72:80])), LogLength: binary.LittleEndian.Uint32(h[68:72])}
	// The log region is required, in whole MiB, after the first MiB and
	// inside the file (MS-VHDX 2.2.2), whether or not a log is in use: its
	// place counts in the compacted size, so a damaged one is refused here
	// rather than turned into a made-up estimate.
	if d.LogLength == 0 || d.LogLength%mib != 0 || d.LogOffset < mib || d.LogOffset%mib != 0 || d.LogOffset > size-int64(d.LogLength) {
		return nil, fmt.Errorf("vhdx: the log region (%d bytes at %d) is not usable", d.LogLength, d.LogOffset)
	}
	// A non-zero log GUID means the log may hold writes not yet in their
	// place: read everything after the header through the replayed log.
	if !allZero(h[48:64]) {
		logLen := int64(binary.LittleEndian.Uint32(h[68:72]))
		logOff := int64(binary.LittleEndian.Uint64(h[72:80]))
		replayed, newSize, n, err := replay(r, size, h[48:64], logOff, logLen)
		if err != nil {
			return nil, err
		}
		r, size = replayed, newSize
		d.LogActive, d.LogEntries, d.FileSize = true, n, newSize
	}
	d.src = r
	// Both region table copies must be identical when valid; the first valid
	// one is used, as Windows does.
	rt, err := regionTable(r)
	if err != nil {
		return nil, err
	}
	// Walk the region table entries: 16 bytes of header, then 32 per entry.
	count := binary.LittleEndian.Uint32(rt[8:12])
	if count > (regionSize-16)/32 {
		return nil, fmt.Errorf("vhdx: region table claims %d entries", count)
	}
	for i := range count {
		// Each entry: GUID, file offset, length, required flag.
		e := rt[16+i*32 : 16+(i+1)*32]
		id := [16]byte(e[0:16])
		off := int64(binary.LittleEndian.Uint64(e[16:24]))
		length := binary.LittleEndian.Uint32(e[24:28])
		required := binary.LittleEndian.Uint32(e[28:32])&1 == 1
		switch id {
		case regionBAT:
			d.BATOffset, d.BATLength = off, length
		case regionMetadata:
			d.MetadataOffset, d.MetadataLength = off, length
		default:
			// An unknown region marked required must not be ignored (2.3.1.2).
			if required {
				return nil, fmt.Errorf("vhdx: unknown required region %x", id)
			}
		}
	}
	// Both regions are required in every VHDX file.
	if d.BATOffset < 0 || d.MetadataOffset < 0 {
		return nil, errors.New("vhdx: the BAT or the metadata region is missing")
	}
	// Regions must lie inside the file, or the reads below would fail late.
	// An offset past 2^63 reads as negative here: that is damage too.
	if d.BATOffset < 0 || d.MetadataOffset < 0 || d.BATOffset > size-int64(d.BATLength) || d.MetadataOffset > size-int64(d.MetadataLength) {
		return nil, errors.New("vhdx: a region lies past the end of the file")
	}
	if err := d.readMetadata(r); err != nil {
		return nil, err
	}
	if err := d.readBAT(r); err != nil {
		return nil, err
	}
	return d, nil
}

// currentHeader returns the valid header copy with the higher sequence number.
func currentHeader(r io.ReaderAt) ([]byte, error) {
	var best []byte
	var bestSeq uint64
	for _, off := range []int64{headerOffset1, headerOffset2} {
		// Read the whole 4 KiB copy: the checksum covers all of it.
		h := make([]byte, headerSize)
		if _, err := r.ReadAt(h, off); err != nil {
			return nil, fmt.Errorf("vhdx: reading the header at %d: %w", off, err)
		}
		// A torn or foreign copy simply does not count.
		if string(h[0:4]) != "head" || !checksumOK(h, 4) {
			continue
		}
		// Keep the copy written last.
		seq := binary.LittleEndian.Uint64(h[8:16])
		if best == nil || seq > bestSeq {
			best, bestSeq = h, seq
		}
	}
	if best == nil {
		return nil, errors.New("vhdx: neither header copy is valid")
	}
	// Version 1 is the only one published.
	if v := binary.LittleEndian.Uint16(best[66:68]); v != 1 {
		return nil, fmt.Errorf("vhdx: header version %d", v)
	}
	return best, nil
}

// regionTable returns the first region table copy with a valid checksum.
func regionTable(r io.ReaderAt) ([]byte, error) {
	for _, off := range []int64{regionOffset1, regionOffset2} {
		// The checksum covers the full 64 KiB copy.
		t := make([]byte, regionSize)
		if _, err := r.ReadAt(t, off); err != nil {
			return nil, fmt.Errorf("vhdx: reading the region table at %d: %w", off, err)
		}
		if string(t[0:4]) == "regi" && checksumOK(t, 4) {
			return t, nil
		}
	}
	return nil, errors.New("vhdx: no valid region table")
}

// readMetadata fills in the sizes from the metadata region.
func (d *Disk) readMetadata(r io.ReaderAt) error {
	// The table of items sits at the start of the region.
	t := make([]byte, metaTableSize)
	if _, err := r.ReadAt(t, d.MetadataOffset); err != nil {
		return fmt.Errorf("vhdx: reading the metadata table: %w", err)
	}
	if string(t[0:8]) != "metadata" {
		return errors.New("vhdx: no metadata table")
	}
	// The entry count is a 16-bit field after two reserved bytes.
	count := int(binary.LittleEndian.Uint16(t[10:12]))
	if count > (metaTableSize-32)/32 {
		return fmt.Errorf("vhdx: metadata table claims %d entries", count)
	}
	for i := range count {
		// Each entry: item GUID, offset within the region, length, flags.
		e := t[32+i*32 : 32+(i+1)*32]
		id := [16]byte(e[0:16])
		off := int64(binary.LittleEndian.Uint32(e[16:20]))
		length := binary.LittleEndian.Uint32(e[20:24])
		required := binary.LittleEndian.Uint32(e[24:28])&4 != 0
		// Read the item's value only for the items used here.
		var want int
		switch id {
		case itemFileParams:
			want = 8
		case itemDiskSize:
			want = 8
		case itemLogical, itemPhysical:
			want = 4
		case itemPage83:
			// The disk's identifier: required in every file, not needed here.
			continue
		case itemParentLoc:
			// Only differencing disks have a parent locator.
			return errors.New("vhdx: differencing disks are not supported")
		default:
			// Unknown required items change how the disk must be read.
			if required {
				return fmt.Errorf("vhdx: unknown required metadata item %x", id)
			}
			continue
		}
		// An item shorter than its type is corrupt.
		if int(length) < want || off+int64(want) > int64(d.MetadataLength) {
			return fmt.Errorf("vhdx: metadata item %x is too short or out of its region", id)
		}
		v := make([]byte, want)
		if _, err := r.ReadAt(v, d.MetadataOffset+off); err != nil {
			return fmt.Errorf("vhdx: reading metadata item %x: %w", id, err)
		}
		switch id {
		case itemFileParams:
			// Block size, then flags: bit 1 says the disk has a parent.
			d.BlockSize = binary.LittleEndian.Uint32(v[0:4])
			d.HasParent = binary.LittleEndian.Uint32(v[4:8])&2 != 0
		case itemDiskSize:
			d.VirtualSize = int64(binary.LittleEndian.Uint64(v))
		case itemLogical:
			d.LogicalSectorSize = binary.LittleEndian.Uint32(v)
		case itemPhysical:
			d.PhysicalSectorSize = binary.LittleEndian.Uint32(v)
		}
	}
	// Without these the BAT cannot be laid out.
	if d.BlockSize == 0 || d.VirtualSize <= 0 || d.LogicalSectorSize == 0 {
		return errors.New("vhdx: the metadata is incomplete")
	}
	// The format's largest disk is 64 TiB; a larger size is damage, and would
	// overflow the block count below.
	if d.VirtualSize > 64<<40 {
		return fmt.Errorf("vhdx: virtual size %d is past the format's 64 TiB", d.VirtualSize)
	}
	// The format allows 1 MiB to 256 MiB blocks, powers of two.
	if d.BlockSize < mib || d.BlockSize > 256*mib || d.BlockSize&(d.BlockSize-1) != 0 {
		return fmt.Errorf("vhdx: block size %d", d.BlockSize)
	}
	// Only 512 and 4096 byte logical sectors exist.
	if d.LogicalSectorSize != 512 && d.LogicalSectorSize != 4096 {
		return fmt.Errorf("vhdx: logical sector size %d", d.LogicalSectorSize)
	}
	// A differencing disk reads part of its data from another file.
	if d.HasParent {
		return errors.New("vhdx: differencing disks are not supported")
	}
	return nil
}

// readBAT reads one entry per payload block, skipping the sector bitmap
// entries interleaved every chunk (2.5).
func (d *Disk) readBAT(r io.ReaderAt) error {
	// A chunk is the stretch of payload blocks one sector bitmap block covers.
	chunkRatio := int64(1<<23) * int64(d.LogicalSectorSize) / int64(d.BlockSize)
	// The last block may be partial, so round up.
	dataBlocks := (d.VirtualSize + int64(d.BlockSize) - 1) / int64(d.BlockSize)
	// One bitmap entry follows every full chunk of payload entries.
	entries := dataBlocks + (dataBlocks-1)/chunkRatio
	if entries*8 > int64(d.BATLength) {
		return errors.New("vhdx: the BAT is shorter than the disk needs")
	}
	// The BAT of a 1 TiB disk with 1 MiB blocks is 8 MiB: read it at once.
	raw := make([]byte, entries*8)
	if _, err := r.ReadAt(raw, d.BATOffset); err != nil {
		return fmt.Errorf("vhdx: reading the BAT: %w", err)
	}
	d.Blocks = make([]Block, dataBlocks)
	for i := range dataBlocks {
		// Skip one bitmap entry per full chunk before this block.
		e := binary.LittleEndian.Uint64(raw[(i+i/chunkRatio)*8:])
		state := uint8(e & 7)
		b := Block{State: state, Offset: -1}
		switch state {
		case stateFullyPresent:
			// The file offset is in MiB, in bits 20 to 63.
			b.Offset = int64(e>>20) * mib
			// A block outside the file means the BAT cannot be trusted.
			if b.Offset < 0 || b.Offset+int64(d.BlockSize) > d.FileSize {
				return fmt.Errorf("vhdx: block %d lies past the end of the file", i)
			}
		case statePartlyPresent:
			return errors.New("vhdx: partially present blocks (a differencing disk) are not supported")
		case stateNotPresent, stateUndefined, stateZero, stateUnmapped:
			// These read as zeros and take no space in the file.
		default:
			return fmt.Errorf("vhdx: block %d has unknown state %d", i, state)
		}
		d.Blocks[i] = b
	}
	return nil
}

// VirtualReader reads the virtual disk's contents through the BAT: a block
// with no data reads as zeros.
type VirtualReader struct {
	d *Disk
	f io.ReaderAt
}

// Contents returns a reader of the virtual disk, through the replayed log
// when there was one.
func (d *Disk) Contents() *VirtualReader { return &VirtualReader{d: d, f: d.src} }

// ReadAt reads len(p) bytes of the virtual disk at off.
func (v *VirtualReader) ReadAt(p []byte, off int64) (int, error) {
	// Reads past the virtual end behave like any file's end.
	if off < 0 {
		return 0, errors.New("vhdx: negative offset")
	}
	if off >= v.d.VirtualSize {
		return 0, io.EOF
	}
	// Cut the read at the virtual end and remember to report it.
	var tail error
	if rest := v.d.VirtualSize - off; int64(len(p)) > rest {
		p, tail = p[:rest], io.EOF
	}
	bs := int64(v.d.BlockSize)
	n := 0
	for n < len(p) {
		// Find the block and the place in it for the next byte.
		pos := off + int64(n)
		b := v.d.Blocks[pos/bs]
		in := pos % bs
		chunk := min(int64(len(p)-n), bs-in)
		if b.Present() {
			// Data in the file: read it from there.
			if _, err := v.f.ReadAt(p[n:n+int(chunk)], b.Offset+in); err != nil {
				return n, fmt.Errorf("vhdx: reading block %d: %w", pos/bs, err)
			}
		} else {
			// No data: the guest reads zeros.
			clear(p[n : n+int(chunk)])
		}
		n += int(chunk)
	}
	return n, tail
}

// checksumOK checks a structure's CRC-32C, computed with its own checksum
// field (at at) taken as zero.
func checksumOK(b []byte, at int) bool {
	want := binary.LittleEndian.Uint32(b[at : at+4])
	// Compute over a copy so the caller's bytes stay as read.
	c := make([]byte, len(b))
	copy(c, b)
	clear(c[at : at+4])
	return crc32.Checksum(c, castagnoli) == want
}

// allZero reports whether every byte of b is zero.
func allZero(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}

// guid turns the usual text form of a GUID into its bytes as stored in a
// VHDX file: the first three fields little-endian, the rest as written.
func guid(s string) [16]byte {
	// Parse the 32 hex digits, ignoring the dashes.
	var raw [16]byte
	j := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '-' {
			continue
		}
		raw[j/2] = raw[j/2]<<4 | hexVal(s[i])
		j++
	}
	// Swap the byte order of the 4-, 2- and 2-byte fields.
	var g [16]byte
	g[0], g[1], g[2], g[3] = raw[3], raw[2], raw[1], raw[0]
	g[4], g[5] = raw[5], raw[4]
	g[6], g[7] = raw[7], raw[6]
	copy(g[8:], raw[8:])
	return g
}

// hexVal returns the value of one hex digit; the GUIDs above are constants.
func hexVal(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	default:
		return c - 'A' + 10
	}
}
