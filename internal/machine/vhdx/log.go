package vhdx

import (
	"encoding/binary"
	"fmt"
	"io"
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
	// Every sector may start an entry; keep the ones that check out.
	entries := map[int64]*logEntry{}
	for at := int64(0); at < logLen; at += logSector {
		if e := parseEntry(log, at, logID); e != nil {
			entries[at] = e
		}
	}
	// The active sequence is the valid one whose head has the highest
	// sequence number: from the head's tail, each entry follows the one
	// before it in the log with the next sequence number, up to the head.
	var best []*logEntry
	for _, head := range entries {
		seqs := sequence(entries, head, logLen)
		if seqs != nil && (best == nil || head.seq > best[len(best)-1].seq) {
			best = seqs
		}
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
		// The file was at least this long when the entry was written.
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

// sequence returns the entries from head's tail up to head, or nil when
// they do not form an unbroken run.
func sequence(entries map[int64]*logEntry, head *logEntry, logLen int64) []*logEntry {
	var run []*logEntry
	at := head.tail
	// A run is at most as many entries as fit in the log.
	for range logLen / logSector {
		e, ok := entries[at]
		if !ok {
			return nil
		}
		// Each entry's sequence number follows the one before it.
		if len(run) > 0 && e.seq != run[len(run)-1].seq+1 {
			return nil
		}
		run = append(run, e)
		if e == head {
			return run
		}
		// The next entry starts right after this one, wrapping around.
		at = (at + e.length) % logLen
	}
	return nil
}

// parseEntry reads the entry at offset at of the log, or returns nil if
// there is no valid entry there for this log GUID.
func parseEntry(log []byte, at int64, logID []byte) *logEntry {
	logLen := int64(len(log))
	// The entry may wrap past the end of the log back to its start.
	read := func(off, n int64) []byte {
		b := make([]byte, n)
		for i := range n {
			b[i] = log[(off+i)%logLen]
		}
		return b
	}
	h := log[at : at+entryHeader]
	if string(h[0:4]) != "loge" {
		return nil
	}
	length := int64(binary.LittleEndian.Uint32(h[8:12]))
	tail := int64(binary.LittleEndian.Uint32(h[12:16]))
	count := int64(binary.LittleEndian.Uint32(h[24:28]))
	// Lengths are whole sectors that fit in the log, the tail is a sector.
	if length < logSector || length%logSector != 0 || length > logLen || tail%logSector != 0 || tail >= logLen {
		return nil
	}
	// An entry of another log (an older one) does not count.
	if string(h[32:48]) != string(logID) {
		return nil
	}
	whole := read(at, length)
	if !checksumOK(whole, 4) {
		return nil
	}
	// Descriptors fill whole sectors after the header; data sectors follow.
	descSectors := (entryHeader + count*descSize + logSector - 1) / logSector
	if count > (length/logSector)*(logSector/descSize) || descSectors*logSector > length {
		return nil
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
			return nil
		}
		switch string(d[0:4]) {
		case "zero":
			// A run of zeros, in whole sectors.
			n := int64(binary.LittleEndian.Uint64(d[8:16]))
			if n <= 0 || n%logSector != 0 || n > maxZeroRun {
				return nil
			}
			e.writes = append(e.writes, logWrite{off: off, length: n})
		case "desc":
			// One 4 KiB write: 8 leading bytes in the descriptor, 4084 in the
			// data sector, 4 trailing bytes in the descriptor.
			if dataAt+logSector > length {
				return nil
			}
			s := whole[dataAt : dataAt+logSector]
			// The data sector repeats the sequence number at both its ends.
			if string(s[0:4]) != "data" ||
				binary.LittleEndian.Uint32(s[4:8]) != uint32(e.seq>>32) ||
				binary.LittleEndian.Uint32(s[4092:4096]) != uint32(e.seq) {
				return nil
			}
			page := make([]byte, logSector)
			copy(page[0:8], d[8:16])
			copy(page[8:8+dataPayload], s[8:8+dataPayload])
			copy(page[8+dataPayload:], d[4:8])
			e.writes = append(e.writes, logWrite{off: off, data: page, length: logSector})
			dataAt += logSector
		default:
			return nil
		}
	}
	return e
}
