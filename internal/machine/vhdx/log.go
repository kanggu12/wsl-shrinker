package vhdx

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"sort"
)

// The log (MS-VHDX 2.3) holds metadata writes (headers excepted: BAT and
// metadata pages) that Windows has made durable in the log but may not have
// written to their place in the file yet. A file in use has an active log.
// Reading such a file correctly means replaying the log, and this package
// does that in memory only: the file is never written.

const (
	logSector    = 4096      // the log works in 4 KiB sectors
	entryHeader  = 64        // bytes of an entry header before its descriptors
	descSize     = 32        // bytes per descriptor
	dataPayload  = 4084      // bytes of a data sector that carry the write
	maxLogLength = 64 << 20  // WSL's logs are 1 MiB; far larger is not a log
	maxZeroRun   = 256 << 20 // a zero descriptor covers metadata, never more
	maxPages     = 1 << 18   // pages a replay may hold: 1 GiB of writes
)

// logEntry is one entry of the log, as read and checked.
type logEntry struct {
	at, length, tail int64  // place in the log, size, and its sequence's start
	seq              uint64 // sequence number, one more than the entry before
	flushed, last    int64  // file size before and after (FlushedFileOffset, LastFileOffset)
	writes           []logWrite
}

// logWrite is one 4 KiB write, or a run of zeros, at a place in the file.
type logWrite struct {
	off    int64
	data   []byte // nil for zeros
	length int64
}

// overlay is the file as the replayed log makes it: writes on top of the
// bytes in the file, and a size that may be larger.
type overlay struct {
	f     io.ReaderAt
	size  int64
	pages map[int64][]byte // 4 KiB pages written by the log, by file offset; nil for zero pages
}

// ReadAt reads the file with the log's writes applied.
func (o *overlay) ReadAt(p []byte, off int64) (int, error) {
	if off >= o.size {
		return 0, io.EOF
	}
	// Cut at the replayed size, and say so as any file's end would.
	var tail error
	if rest := o.size - off; int64(len(p)) > rest {
		p, tail = p[:rest], io.EOF
	}
	// Read the file first; bytes past its real end read as zeros.
	n, err := o.f.ReadAt(p, off)
	if err != nil && err != io.EOF {
		return n, err
	}
	clear(p[n:])
	// Lay the log's pages over what was read.
	for page := off &^ (logSector - 1); page < off+int64(len(p)); page += logSector {
		data, ok := o.pages[page]
		if !ok {
			continue
		}
		// The part of this page that falls inside p.
		from, to := max(page, off), min(page+logSector, off+int64(len(p)))
		if data == nil {
			clear(p[from-off : to-off])
		} else {
			copy(p[from-off:to-off], data[from-page:to-page])
		}
	}
	return len(p), tail
}

// replay finds the active sequence in the log at logOff (length logLen) for
// the log GUID logID, and returns the file as it reads after replaying it,
// with the number of entries replayed. No valid sequence means nothing was
// left to replay, and the file is returned as it is.
func replay(r io.ReaderAt, size int64, logID []byte, logOff, logLen int64) (io.ReaderAt, int64, int, error) {
	// The log is whole sectors inside the file.
	if logLen <= 0 || logLen > maxLogLength || logLen%logSector != 0 || logOff%logSector != 0 || logOff+logLen > size {
		return nil, 0, 0, fmt.Errorf("vhdx: the log region (%d bytes at %d) is not usable", logLen, logOff)
	}
	log := make([]byte, logLen)
	if _, err := r.ReadAt(log, logOff); err != nil {
		return nil, 0, 0, fmt.Errorf("vhdx: reading the log: %w", err)
	}
	// Every sector may start an entry; keep the ones that check out. In a
	// real log the entries do not overlap, so checksumming them all reads
	// the log about once (twice with an older log's entries left between).
	// A damaged log can make every sector claim an entry as long as the log,
	// which would read it once per sector: past four times the log, it is
	// refused instead.
	entries := map[int64]*logEntry{}
	budget := 4 * logLen
	for at := int64(0); at < logLen; at += logSector {
		e, ok := parseEntry(log, at, logID, &budget)
		if !ok {
			return nil, 0, 0, errors.New("vhdx: the log is damaged (its entries overlap far more than a log's do)")
		}
		if e != nil {
			entries[at] = e
		}
	}
	best, err := activeSequence(entries, logLen)
	if err != nil {
		return nil, 0, 0, err
	}
	if best == nil {
		return r, size, 0, nil
	}
	// The file must still hold everything the log says was flushed to it;
	// a shorter file was cut after the log was written, and is damaged.
	if head := best[len(best)-1]; size < head.flushed {
		return nil, 0, 0, fmt.Errorf("vhdx: the file is %d bytes, shorter than the %d its log says were flushed", size, head.flushed)
	}
	o := &overlay{f: r, size: size, pages: map[int64][]byte{}}
	// A log asking for more pages than any real one is damage: refuse it
	// rather than spend the memory.
	pages := int64(0)
	for _, e := range best {
		for _, w := range e.writes {
			pages += w.length / logSector
		}
	}
	if pages > maxPages {
		return nil, 0, 0, fmt.Errorf("vhdx: the log writes %d pages, more than a log holds", pages)
	}
	for _, e := range best {
		// The file was at least this long when the entry was written. A log
		// may extend the file by what it writes, never by more: a larger
		// claim is damage, and taken as the size it would let a BAT entry
		// point anywhere past the real end.
		if e.last > size+maxPages*logSector {
			return nil, 0, 0, fmt.Errorf("vhdx: the log says the file is %d bytes, far past its %d", e.last, size)
		}
		o.size = max(o.size, e.last)
		for _, w := range e.writes {
			// Apply the write page by page, in log order.
			for p := int64(0); p < w.length; p += logSector {
				if w.data == nil {
					o.pages[w.off+p] = nil
				} else {
					o.pages[w.off+p] = w.data
				}
			}
		}
	}
	return o, o.size, len(best), nil
}

// activeSequence is the log's active sequence, the entries to replay; nil
// when there is none. It is the valid sequence whose head has the highest
// sequence number: from the head's tail, each entry follows the one before
// it in the log with the next sequence number, up to the head. Heads are
// tried from the highest number down, so the first valid sequence is the
// one wanted and the rest are never walked.
func activeSequence(entries map[int64]*logEntry, logLen int64) ([]*logEntry, error) {
	heads := make([]*logEntry, 0, len(entries))
	for _, e := range entries {
		heads = append(heads, e)
	}
	sort.Slice(heads, func(i, j int) bool { return heads[i].seq > heads[j].seq })
	// A real log's newest head is valid, and found in one walk of its
	// sequence. A damaged one can make every head walk most of the log and
	// fail at its end: the walks share a budget of four times the entries.
	steps := 4 * len(entries)
	for _, head := range heads {
		seqs, ok := sequence(entries, head, logLen, &steps)
		if !ok {
			return nil, errors.New("vhdx: the log is damaged (its entries form no sequence it can replay)")
		}
		if seqs != nil {
			return seqs, nil
		}
	}
	return nil, nil
}

// sequence returns the entries from head's tail up to head, or nil when
// they do not form an unbroken run.
func sequence(entries map[int64]*logEntry, head *logEntry, logLen int64, steps *int) ([]*logEntry, bool) {
	var run []*logEntry
	at := head.tail
	// A run is at most as many entries as fit in the log.
	for range logLen / logSector {
		// Each step is charged to the search's budget; spent, it stops.
		if *steps--; *steps < 0 {
			return nil, false
		}
		e, ok := entries[at]
		if !ok {
			return nil, true
		}
		// Each entry's sequence number follows the one before it.
		if len(run) > 0 && e.seq != run[len(run)-1].seq+1 {
			return nil, true
		}
		run = append(run, e)
		if e == head {
			return run, true
		}
		// The next entry starts right after this one, wrapping around.
		at = (at + e.length) % logLen
	}
	return nil, true
}

// parseEntry reads the entry at offset at of the log. It returns nil if
// there is no valid entry there for this log GUID, and false when checking
// it would take the bytes it may still checksum (budget) below zero.
func parseEntry(log []byte, at int64, logID []byte, budget *int64) (*logEntry, bool) {
	logLen := int64(len(log))
	h := log[at : at+entryHeader]
	if string(h[0:4]) != "loge" {
		return nil, true
	}
	length := int64(binary.LittleEndian.Uint32(h[8:12]))
	tail := int64(binary.LittleEndian.Uint32(h[12:16]))
	count := int64(binary.LittleEndian.Uint32(h[24:28]))
	// Lengths are whole sectors that fit in the log, the tail is a sector.
	if length < logSector || length%logSector != 0 || length > logLen || tail%logSector != 0 || tail >= logLen {
		return nil, true
	}
	// An entry of another log (an older one) does not count.
	if string(h[32:48]) != string(logID) {
		return nil, true
	}
	// Checksumming the entry reads all of it: charge it to the budget.
	if *budget -= length; *budget < 0 {
		return nil, false
	}
	// The entry may wrap past the end of the log back to its start: it is
	// at most two stretches of the log, checksummed in place. The checksum
	// field (bytes 4 to 8, never split, as the entry starts on a sector) is
	// taken as zero.
	first := log[at:min(at+length, logLen)]
	rest := log[:length-int64(len(first))]
	sum := crc32.Update(0, castagnoli, first[0:4])
	sum = crc32.Update(sum, castagnoli, make([]byte, 4))
	sum = crc32.Update(sum, castagnoli, first[8:])
	sum = crc32.Update(sum, castagnoli, rest)
	if sum != binary.LittleEndian.Uint32(h[4:8]) {
		return nil, true
	}
	// A valid entry is copied out whole, for its descriptors and data.
	whole := append(append(make([]byte, 0, length), first...), rest...)
	// Descriptors fill whole sectors after the header; data sectors follow.
	descSectors := (entryHeader + count*descSize + logSector - 1) / logSector
	if count > (length/logSector)*(logSector/descSize) || descSectors*logSector > length {
		return nil, true
	}
	e := &logEntry{
		at: at, length: length, tail: tail,
		seq:     binary.LittleEndian.Uint64(h[16:24]),
		flushed: int64(binary.LittleEndian.Uint64(h[48:56])),
		last:    int64(binary.LittleEndian.Uint64(h[56:64])),
	}
	dataAt := descSectors * logSector
	for i := range count {
		d := whole[entryHeader+i*descSize : entryHeader+(i+1)*descSize]
		off := int64(binary.LittleEndian.Uint64(d[16:24]))
		// Every descriptor carries the entry's sequence number.
		if binary.LittleEndian.Uint64(d[24:32]) != e.seq || off < 0 || off%logSector != 0 {
			return nil, true
		}
		switch string(d[0:4]) {
		case "zero":
			// A run of zeros, in whole sectors.
			n := int64(binary.LittleEndian.Uint64(d[8:16]))
			if n <= 0 || n%logSector != 0 || n > maxZeroRun {
				return nil, true
			}
			e.writes = append(e.writes, logWrite{off: off, length: n})
		case "desc":
			// One 4 KiB write: 8 leading bytes in the descriptor, 4084 in the
			// data sector, 4 trailing bytes in the descriptor.
			if dataAt+logSector > length {
				return nil, true
			}
			s := whole[dataAt : dataAt+logSector]
			// The data sector repeats the sequence number at both its ends.
			if string(s[0:4]) != "data" ||
				binary.LittleEndian.Uint32(s[4:8]) != uint32(e.seq>>32) ||
				binary.LittleEndian.Uint32(s[4092:4096]) != uint32(e.seq) {
				return nil, true
			}
			page := make([]byte, logSector)
			copy(page[0:8], d[8:16])
			copy(page[8:8+dataPayload], s[8:8+dataPayload])
			copy(page[8+dataPayload:], d[4:8])
			e.writes = append(e.writes, logWrite{off: off, data: page, length: logSector})
			dataAt += logSector
		default:
			return nil, true
		}
	}
	return e, true
}
