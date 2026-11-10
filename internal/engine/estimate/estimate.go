// Package estimate works out how small a WSL disk file gets when it is
// compacted, from what the file and its filesystem say.
//
// There are two compactions. WSL's (`wsl --manage --compact`, for a
// registered distribution) trims first: the file after it is the regions
// plus the payload blocks that are stored and hold at least one ext4 block
// in use. Measured against WSL on a disk it made, this matched the result to
// the byte. Windows' own (the virtual disk API, for Docker Desktop's disk and
// other files WSL does not register) runs on a file nobody has attached, so
// nothing trims: it drops the stored blocks that hold only zeros, and keeps
// every other one (measured on a file this tool's tests write).
package estimate

import (
	"io"

	"github.com/kanggu12/wsl-shrinker/internal/machine/ext4"
	"github.com/kanggu12/wsl-shrinker/internal/machine/vhdx"
)

// Result is one disk's numbers.
type Result struct {
	// FileSize is what the file takes on the host now.
	FileSize int64 `json:"file_size_bytes"`
	// After is the file's size after the compaction.
	After int64 `json:"after_bytes"`
	// Reclaim is what that gives back to Windows (never negative).
	Reclaim int64 `json:"reclaim_bytes"`
	// Stored is the payload the file holds now: stored blocks times their size.
	Stored int64 `json:"stored_bytes"`
	// Used is what the filesystem marks in use.
	Used int64 `json:"used_bytes"`
	// Trimmable is stored payload that holds nothing the filesystem uses:
	// space only a trim frees (the guest wrote it, then deleted the files).
	Trimmable int64 `json:"trimmable_bytes"`
	// InUse is set when the file is open (WSL runs on it or holds it); the
	// numbers come from its log, replayed in memory.
	InUse bool `json:"in_use"`
	// Mounted is set when the filesystem is mounted or was not unmounted
	// cleanly: its bitmaps can lag behind the last few seconds of writes.
	Mounted bool `json:"mounted"`
	// FSErrors is set when the kernel recorded errors in the filesystem:
	// e2fsck should look at it before anything else touches it.
	FSErrors bool `json:"fs_errors"`
	// Filesystem is false for a disk that is not ext4: the estimate then
	// counts only what a compaction alone frees.
	Filesystem bool `json:"filesystem"`
	// Trimmed says which compaction the numbers are for: true for WSL's,
	// which trims first.
	Trimmed bool `json:"trimmed"`
	// Fixed is set for a fixed-size disk: its file keeps every block, and
	// Windows compacts only disks that grow. After is the file as it is,
	// and nothing counts as trimmable.
	Fixed bool `json:"fixed"`
}

// zeroScanLimit is how many bytes OfUntrimmed reads, per disk, looking for
// blocks of zeros. A block with data stops the read at its first non-zero
// byte, mostly within the first few KiB; a block of zeros is read whole
// (1 MiB). 4 GiB covers four thousand blocks of zeros, a few seconds of
// reading; blocks past it count as kept, so the estimate gives back less,
// never more. A variable so tests can make it small.
var zeroScanLimit int64 = 4 << 30

// Of returns the estimate for WSL's compaction, which trims first; fs is nil
// when the disk holds no ext4 filesystem this package can read.
func Of(d *vhdx.Disk, fs *ext4.FS, onHost int64) Result {
	r := start(d, fs, onHost)
	r.Trimmed = true
	// A fixed-size disk keeps every block: there is none to count.
	if d.Fixed {
		return finish(r, d, 0)
	}
	keep := 0
	for i, b := range d.Blocks {
		if !b.Present() {
			// A block not stored costs nothing now and nothing after.
			continue
		}
		// Without a filesystem to ask, a stored block is kept; with one, a
		// block whose filesystem blocks are all free is trimmed away.
		if fs == nil || inUse(d, fs, i) {
			keep++
		} else {
			r.Trimmable += int64(d.BlockSize)
		}
	}
	return finish(r, d, keep)
}

// OfUntrimmed returns the estimate for Windows' own compaction of a file
// nobody has attached, which does not trim: a stored block goes only when it
// holds nothing but zeros. Only blocks the filesystem does not use are read
// to find out; a block in use counts as kept even when it is zeros, so the
// estimate errs towards less. Without a filesystem every block is kept.
func OfUntrimmed(d *vhdx.Disk, fs *ext4.FS, onHost int64) Result {
	r := start(d, fs, onHost)
	// A fixed-size disk keeps every block: none is read to find zeros.
	if d.Fixed {
		return finish(r, d, 0)
	}
	keep := 0
	budget := zeroScanLimit
	rd := d.Contents()
	buf := make([]byte, 64<<10)
	for i, b := range d.Blocks {
		if !b.Present() {
			continue
		}
		if fs == nil || inUse(d, fs, i) {
			keep++
			continue
		}
		r.Trimmable += int64(d.BlockSize)
		// A free block with data in it (deleted files) stays without a trim.
		if !zeros(rd, int64(i)*int64(d.BlockSize), int64(d.BlockSize), buf, &budget) {
			keep++
		}
	}
	return finish(r, d, keep)
}

// start fills in what both estimates share.
func start(d *vhdx.Disk, fs *ext4.FS, onHost int64) Result {
	r := Result{FileSize: onHost, InUse: d.LogActive, Fixed: d.Fixed}
	for _, b := range d.Blocks {
		if b.Present() {
			r.Stored += int64(d.BlockSize)
		}
	}
	if fs != nil {
		r.Filesystem = true
		r.Used = int64(fs.UsedBlocks) * fs.BlockSize
		r.Mounted = fs.NeedsRecovery
		r.FSErrors = fs.HasErrors
	}
	return r
}

// finish sets the size after a compaction that keeps keep blocks.
func finish(r Result, d *vhdx.Disk, keep int) Result {
	// A fixed-size disk keeps every block, whatever is in them: Windows'
	// compaction of one reports success and leaves the file as it is.
	if d.Fixed {
		r.After, r.Reclaim = r.FileSize, 0
		return r
	}
	r.After = d.CompactedSize(keep)
	// A file already smaller than the estimate (a sparse file on the host)
	// has nothing to give back.
	r.Reclaim = max(0, r.FileSize-r.After)
	return r
}

// inUse reports whether payload block i holds a filesystem block in use.
func inUse(d *vhdx.Disk, fs *ext4.FS, i int) bool {
	// The filesystem blocks this payload block covers.
	per := uint64(int64(d.BlockSize) / fs.BlockSize)
	return fs.AnyInUse(uint64(i)*per, uint64(i+1)*per)
}

// zeros reports whether the n bytes at off read as zeros, stopping at the
// first byte that is not. The first read is one page, as a block with data
// mostly shows it in its first bytes; the rest goes in buf's size. It
// reports false (kept) when the budget runs out or the read fails.
func zeros(r *vhdx.VirtualReader, off, n int64, buf []byte, budget *int64) bool {
	step := min(4<<10, int64(len(buf)))
	for done := int64(0); done < n; step = int64(len(buf)) {
		chunk := buf[:min(step, n-done)]
		if *budget < int64(len(chunk)) {
			return false
		}
		*budget -= int64(len(chunk))
		got, err := r.ReadAt(chunk, off+done)
		// A short read is the virtual end: what is past it is not data.
		for _, c := range chunk[:got] {
			if c != 0 {
				return false
			}
		}
		// The virtual end: zeros so far, and nothing past it is data.
		if err == io.EOF {
			return true
		}
		if err != nil {
			return false
		}
		done += int64(got)
	}
	return true
}
