// Package vhdxtest writes small VHDX files for tests, laid out the way the
// format describes (MS-VHDX): two headers, two region tables, a log region,
// the metadata region and the BAT, then payload blocks. It writes only what a
// reader needs; it is not a general VHDX writer.
package vhdxtest

import (
	"encoding/binary"
	"hash/crc32"
	"os"
	"testing"
)

// Layout of the files written here; every region starts on a MiB boundary.
const (
	mib         = 1 << 20
	logOffset   = 1 * mib // the log region, empty but present as required
	logLength   = 1 * mib
	metaOffset  = 2 * mib // the metadata region
	metaLength  = 1 * mib
	batOffset   = 3 * mib // the BAT region; payload follows it
	stateZero   = 2       // BAT state for a block that reads as zeros
	statePresnt = 6       // BAT state for a block stored in the file
)

// Spec describes the file to write.
type Spec struct {
	// VirtualSize is the size of the disk the guest sees.
	VirtualSize int64
	// BlockSize is the payload block size (1 MiB unless set).
	BlockSize uint32
	// LogicalSector is 512 or 4096 (512 unless set).
	LogicalSector uint32
	// Blocks maps payload block indexes to their contents, which are padded
	// with zeros to the block size. Listed blocks are stored in the file.
	Blocks map[int64][]byte
	// ZeroBlocks lists blocks recorded as zero (state 2) without data.
	ZeroBlocks []int64
	// LogActive writes a log GUID into the header, as an open file has.
	LogActive bool
	// InLogOnly lists blocks whose data is in the file but whose BAT entries
	// are only in the log, as after a crash before the BAT was written. It
	// implies LogActive and puts one log entry holding those BAT pages.
	InLogOnly []int64
	// LogWrap starts the log entry near the end of the log so it wraps.
	LogWrap bool
	// StaleLogEntry adds an entry of an older log (another GUID) with a
	// higher sequence number, which a reader must ignore.
	StaleLogEntry bool
	// TruncateLog cuts the file shorter than the log says was flushed.
	TruncateLog bool
	// BreakHeader1 spoils the checksum of the first header copy.
	BreakHeader1 bool
	// SeqHigherIn2 gives the second header the higher sequence number.
	SeqHigherIn2 bool
	// BreakRegion1 spoils the first region table copy.
	BreakRegion1 bool
	// Parent marks the disk as differencing.
	Parent bool
	// Fixed marks the disk as fixed-size: its blocks stay allocated.
	Fixed bool
	// HeaderLogOffset, when set, is the log offset both headers name in
	// place of the real one: a damaged header field.
	HeaderLogOffset uint64
	// LastFileOffset, when set, is the file size the log entry claims
	// (LastFileOffset), in place of the file's real end.
	LastFileOffset int64
}

// Write creates the file at path and returns its size.
func Write(t testing.TB, path string, s Spec) int64 {
	t.Helper()
	// Fill in the defaults the caller left out.
	if s.BlockSize == 0 {
		s.BlockSize = mib
	}
	if s.LogicalSector == 0 {
		s.LogicalSector = 512
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	at := func(b []byte, off int64) {
		// Every write goes to a fixed place; holes stay sparse on disk.
		if _, err := f.WriteAt(b, off); err != nil {
			t.Fatal(err)
		}
	}
	// File identifier: signature, then the creator in UTF-16 (left empty).
	at([]byte("vhdxfile"), 0)
	// Lay out the BAT: payload entries with a sector bitmap entry after each
	// full chunk, as the reader expects.
	bs := int64(s.BlockSize)
	chunk := int64(1<<23) * int64(s.LogicalSector) / bs
	blocks := (s.VirtualSize + bs - 1) / bs
	entries := blocks + (blocks-1)/chunk
	batLen := roundUp(entries*8, mib)
	bat := make([]byte, batLen)
	// Payload blocks go after the BAT in index order.
	next := roundUp(batOffset+batLen, mib)
	// The BAT as the log makes it, and as the file holds it.
	logBAT := make([]byte, batLen)
	for i := range blocks {
		var e uint64
		if data, ok := s.Blocks[i]; ok {
			// Store the block and point its entry at it.
			at(data, next)
			e = uint64(next/mib)<<20 | statePresnt
			next += bs
		}
		for _, z := range s.ZeroBlocks {
			if z == i {
				e = stateZero
			}
		}
		binary.LittleEndian.PutUint64(logBAT[(i+i/chunk)*8:], e)
		// Entries kept for the log stay empty in the file.
		if !contains(s.InLogOnly, i) {
			binary.LittleEndian.PutUint64(bat[(i+i/chunk)*8:], e)
		}
	}
	at(bat, batOffset)
	// The log: one entry with the BAT pages the file is missing.
	logID := make([]byte, 16)
	if s.LogActive || len(s.InLogOnly) > 0 {
		logID[0] = 0x5a
		var pages []page
		for _, i := range s.InLogOnly {
			off := (i + i/chunk) * 8 / 4096 * 4096
			pages = append(pages, page{batOffset + off, logBAT[off : off+4096]})
		}
		if len(pages) > 0 {
			start := int64(0)
			if s.LogWrap {
				// Two sectors before the end: the entry runs past it.
				start = logLength - 2*4096
			}
			flushed := next
			if s.TruncateLog {
				flushed = next + 10*mib
			}
			last := next
			if s.LastFileOffset != 0 {
				last = s.LastFileOffset
			}
			entry := logEntry(logID, 100, start, flushed, last, pages)
			for j := range int64(len(entry)) {
				at(entry[j:j+1], logOffset+(start+j)%logLength)
			}
			if s.StaleLogEntry {
				// An older log's entry, with a higher sequence number, at
				// the next free place; it rewrites the BAT to all zeros.
				old := append([]byte(nil), logID...)
				old[0] = 0x11
				stale := logEntry(old, 500, (start+int64(len(entry)))%logLength, next, next,
					[]page{{batOffset, make([]byte, 4096)}})
				at(stale, logOffset+(start+int64(len(entry)))%logLength)
			}
		}
	}
	// Metadata: the table, then the items' values from 64 KiB on.
	meta := make([]byte, metaLength)
	copy(meta[0:8], "metadata")
	items := []struct {
		id    string
		value []byte
		flags uint32 // bit 1: belongs to the virtual disk; bit 2: required
	}{
		{"CAA16737-FA36-4D43-B3B6-33F0AA44E76B", fileParams(s), 4},
		{"2FA54224-CD1B-4876-B211-5DBED83BF4B8", u64(uint64(s.VirtualSize)), 6},
		{"BECA12AB-B2E6-4523-93EF-C309E000C746", make([]byte, 16), 6},
		{"8141BF1D-A96F-4709-BA47-F233A8FAAB5F", u32(s.LogicalSector), 6},
		{"CDA348C7-445D-4471-9CC9-E9885251C556", u32(4096), 6},
	}
	binary.LittleEndian.PutUint16(meta[10:12], uint16(len(items)))
	valueAt := uint32(64 << 10)
	for i, it := range items {
		// Each entry: GUID, offset, length, flags.
		e := meta[32+i*32:]
		copy(e[0:16], guid(it.id))
		binary.LittleEndian.PutUint32(e[16:20], valueAt)
		binary.LittleEndian.PutUint32(e[20:24], uint32(len(it.value)))
		binary.LittleEndian.PutUint32(e[24:28], it.flags)
		copy(meta[valueAt:], it.value)
		valueAt += 64
	}
	at(meta, metaOffset)
	// Region table: BAT and metadata, both required.
	rt := make([]byte, 64<<10)
	copy(rt[0:4], "regi")
	binary.LittleEndian.PutUint32(rt[8:12], 2)
	region := func(i int, id string, off int64, length int64) {
		e := rt[16+i*32:]
		copy(e[0:16], guid(id))
		binary.LittleEndian.PutUint64(e[16:24], uint64(off))
		binary.LittleEndian.PutUint32(e[24:28], uint32(length))
		binary.LittleEndian.PutUint32(e[28:32], 1)
	}
	region(0, "2DC27766-F623-4200-9D64-115E9BFD4A08", batOffset, batLen)
	region(1, "8B7CA206-4790-4B9A-B8FE-575F050F886E", metaOffset, metaLength)
	sum(rt, 4)
	at(rt, 256<<10)
	// The first copy can be spoiled to test the fallback to the second.
	if s.BreakRegion1 {
		bad := append([]byte(nil), rt...)
		bad[100] ^= 0xff
		at(bad, 192<<10)
	} else {
		at(rt, 192<<10)
	}
	// Headers: two copies with their own sequence numbers.
	for i, off := range []int64{64 << 10, 128 << 10} {
		h := make([]byte, 4<<10)
		copy(h[0:4], "head")
		seq := uint64(10 + i)
		if !s.SeqHigherIn2 {
			seq = uint64(11 - i)
		}
		binary.LittleEndian.PutUint64(h[8:16], seq)
		// A non-zero GUID marks the log as in use.
		copy(h[48:64], logID)
		binary.LittleEndian.PutUint16(h[64:66], 0) // log version
		binary.LittleEndian.PutUint16(h[66:68], 1) // format version
		binary.LittleEndian.PutUint32(h[68:72], logLength)
		binary.LittleEndian.PutUint64(h[72:80], logOffset)
		if s.HeaderLogOffset != 0 {
			binary.LittleEndian.PutUint64(h[72:80], s.HeaderLogOffset)
		}
		sum(h, 4)
		if i == 0 && s.BreakHeader1 {
			h[200] ^= 0xff
		}
		at(h, off)
	}
	// The file ends after the last payload block.
	if err := f.Truncate(next); err != nil {
		t.Fatal(err)
	}
	return next
}

// page is one 4 KiB write in a log entry.
type page struct {
	off  int64
	data []byte
}

// logEntry encodes a log entry (MS-VHDX 2.3.1) with one data descriptor per
// page, whose sequence starts at tail.
func logEntry(id []byte, seq uint64, tail, flushed, last int64, pages []page) []byte {
	// Descriptors fill whole sectors after the 64-byte header.
	descSectors := (64 + 32*int64(len(pages)) + 4095) / 4096
	e := make([]byte, (descSectors+int64(len(pages)))*4096)
	copy(e[0:4], "loge")
	binary.LittleEndian.PutUint32(e[8:12], uint32(len(e)))
	binary.LittleEndian.PutUint32(e[12:16], uint32(tail))
	binary.LittleEndian.PutUint64(e[16:24], seq)
	binary.LittleEndian.PutUint32(e[24:28], uint32(len(pages)))
	copy(e[32:48], id)
	binary.LittleEndian.PutUint64(e[48:56], uint64(flushed))
	binary.LittleEndian.PutUint64(e[56:64], uint64(last))
	for i, p := range pages {
		// Data descriptor: trailing 4 bytes, leading 8, offset, sequence.
		d := e[64+i*32:]
		copy(d[0:4], "desc")
		copy(d[4:8], p.data[4092:4096])
		copy(d[8:16], p.data[0:8])
		binary.LittleEndian.PutUint64(d[16:24], uint64(p.off))
		binary.LittleEndian.PutUint64(d[24:32], seq)
		// Data sector: sequence high, the middle 4084 bytes, sequence low.
		sec := e[(descSectors+int64(i))*4096:]
		copy(sec[0:4], "data")
		binary.LittleEndian.PutUint32(sec[4:8], uint32(seq>>32))
		copy(sec[8:4092], p.data[8:4092])
		binary.LittleEndian.PutUint32(sec[4092:4096], uint32(seq))
	}
	sum(e, 4)
	return e
}

// contains reports whether xs holds x.
func contains(xs []int64, x int64) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// fileParams encodes the block size and the two flags: bit 0 for a
// fixed-size disk, bit 1 for one with a parent.
func fileParams(s Spec) []byte {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint32(b[0:4], s.BlockSize)
	var flags uint32
	if s.Fixed {
		flags |= 1
	}
	if s.Parent {
		flags |= 2
	}
	binary.LittleEndian.PutUint32(b[4:8], flags)
	return b
}

// sum writes the CRC-32C of b, taken with its checksum field zero, at at.
func sum(b []byte, at int) {
	clear(b[at : at+4])
	c := crc32.Checksum(b, crc32.MakeTable(crc32.Castagnoli))
	binary.LittleEndian.PutUint32(b[at:at+4], c)
}

// u64 and u32 encode little-endian values.
func u64(v uint64) []byte { b := make([]byte, 8); binary.LittleEndian.PutUint64(b, v); return b }
func u32(v uint32) []byte { b := make([]byte, 4); binary.LittleEndian.PutUint32(b, v); return b }

// roundUp rounds n up to a multiple of m.
func roundUp(n, m int64) int64 { return (n + m - 1) / m * m }

// guid returns a GUID's bytes in VHDX order (first three fields little-endian).
func guid(s string) []byte {
	var raw [16]byte
	j := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '-' {
			continue
		}
		var v byte
		switch c := s[i]; {
		case c >= '0' && c <= '9':
			v = c - '0'
		case c >= 'a' && c <= 'f':
			v = c - 'a' + 10
		default:
			v = c - 'A' + 10
		}
		raw[j/2] = raw[j/2]<<4 | v
		j++
	}
	return []byte{raw[3], raw[2], raw[1], raw[0], raw[5], raw[4], raw[7], raw[6],
		raw[8], raw[9], raw[10], raw[11], raw[12], raw[13], raw[14], raw[15]}
}
