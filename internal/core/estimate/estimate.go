// Package estimate works out how small a WSL disk file gets when it is
// trimmed and compacted, from what the file and its filesystem say.
//
// A compaction keeps the file's regions and every payload block that still
// holds data; the trim before it frees the blocks ext4 no longer uses. So the
// file after both is the regions plus the payload blocks that are stored and
// hold at least one ext4 block in use. Measured against `wsl --manage
// --compact` on a disk WSL made, this matched the result to the byte.
package estimate

import (
	"github.com/kanggu12/wsl-shrinker/internal/machine/ext4"
	"github.com/kanggu12/wsl-shrinker/internal/machine/vhdx"
)

// Result is one disk's numbers.
type Result struct {
	// FileSize is what the file takes on the host now.
	FileSize int64
	// After is the file's size after a trim and a compaction.
	After int64
	// Reclaim is what that gives back to Windows (never negative).
	Reclaim int64
	// Stored is the payload the file holds now: stored blocks times their size.
	Stored int64
	// Used is what the filesystem marks in use.
	Used int64
	// Trimmable is stored payload that holds nothing the filesystem uses:
	// space only a trim frees (the guest wrote it, then deleted the files).
	Trimmable int64
	// InUse is set when the file is open (WSL runs on it or holds it); the
	// numbers come from its log, replayed in memory.
	InUse bool
	// Mounted is set when the filesystem is mounted or was not unmounted
	// cleanly: its bitmaps can lag behind the last few seconds of writes.
	Mounted bool
	// Filesystem is false for a disk that is not ext4: the estimate then
	// counts only what a compaction alone frees.
	Filesystem bool
}

// Of returns the estimate for a disk; fs is nil when the disk holds no ext4
// filesystem this package can read.
func Of(d *vhdx.Disk, fs *ext4.FS, onHost int64) Result {
	r := Result{FileSize: onHost, InUse: d.LogActive}
	bs := int64(d.BlockSize)
	keep := 0
	for i, b := range d.Blocks {
		if !b.Present() {
			// A block not stored costs nothing now and nothing after.
			continue
		}
		r.Stored += bs
		// Without a filesystem to ask, a stored block is kept.
		if fs == nil {
			keep++
			continue
		}
		// The filesystem blocks this payload block covers.
		per := uint64(bs / fs.BlockSize)
		if fs.AnyInUse(uint64(i)*per, uint64(i+1)*per) {
			keep++
		} else {
			r.Trimmable += bs
		}
	}
	if fs != nil {
		r.Filesystem = true
		r.Used = int64(fs.UsedBlocks) * fs.BlockSize
		r.Mounted = fs.NeedsRecovery
	}
	r.After = d.CompactedSize(keep)
	// A file already smaller than the estimate (a sparse file on the host)
	// has nothing to give back.
	r.Reclaim = max(0, r.FileSize-r.After)
	return r
}
