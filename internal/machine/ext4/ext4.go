// Package ext4 reads which blocks of an ext4 filesystem are in use, from its
// superblock, group descriptors and block bitmaps. It never writes, and it
// reads through any io.ReaderAt, such as the contents of a VHDX file.
//
// That is the other half of how much a WSL disk can shrink: a payload block
// of the VHDX that holds no block in use by ext4 is space a trim and a
// compaction give back.
package ext4

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/bits"
)

// Feature flags this package looks at (see the kernel's ext4.h).
const (
	compatSparseSuper2 = 0x200 // at most two backup superblocks
	incompatRecover    = 0x4   // the journal holds changes not yet written
	incompatMetaBG     = 0x10  // group descriptors spread over the disk
	incompat64Bit      = 0x80  // 64-bit block numbers, larger descriptors
	roCompatSparse     = 0x1   // backups only in groups 0, 1 and powers of 3, 5, 7
	roCompatGDTCsum    = 0x10  // group descriptors carry checksums (uninit_bg)
	roCompatMetaCsum   = 0x400 // metadata checksums, which include the above
	bgBlockUninit      = 0x2   // the group's bitmap was never written
	superblockOffset   = 1024  // the primary superblock's place on the disk
	superblockMagic    = 0xEF53
)

// FS is what the superblock says, and the blocks in use.
type FS struct {
	// BlockSize is the filesystem block size in bytes.
	BlockSize int64
	// Blocks is the number of blocks; FreeBlocks what the superblock counts free.
	Blocks, FreeBlocks uint64
	// UsedBlocks counts the blocks the bitmaps mark in use.
	UsedBlocks uint64
	// NeedsRecovery is set when the journal holds changes the bitmaps do not
	// show yet: the filesystem is mounted, or was not unmounted cleanly.
	NeedsRecovery bool
	// HasErrors is set when the kernel recorded filesystem errors (s_state
	// bit 2): fsck should look at it before anything else touches it.
	HasErrors bool
	// per is how many blocks a chunk holds: the chunk Read was given, in
	// blocks, a power of two.
	per uint64
	// used has one bit per chunk, set when a block in it is in use.
	used []uint64
}

// InUse reports whether block b's chunk holds a block in use: block b
// itself when Read was given chunks of one block.
func (fs *FS) InUse(b uint64) bool {
	// Blocks past the end are not part of the filesystem.
	if b >= fs.Blocks {
		return false
	}
	c := b / fs.per
	return fs.used[c/64]&(1<<(c%64)) != 0
}

// AnyInUse reports whether any chunk that overlaps blocks [from, to) holds
// a block in use: exactly whether a block in [from, to) is in use when the
// range is whole chunks, as a VHDX block is.
func (fs *FS) AnyInUse(from, to uint64) bool {
	// Clip the range to the filesystem, then to the chunks it touches.
	to = min(to, fs.Blocks)
	if from >= to {
		return false
	}
	first, last := from/fs.per, (to-1)/fs.per+1
	for c := first; c < last; {
		// Test a whole word at once when the range covers it.
		if c%64 == 0 && c+64 <= last {
			if fs.used[c/64] != 0 {
				return true
			}
			c += 64
			continue
		}
		if fs.used[c/64]&(1<<(c%64)) != 0 {
			return true
		}
		c++
	}
	return false
}

// Read reads the filesystem at the start of r, a device of size bytes. It
// records what is in use per chunk of chunk bytes, the unit the caller asks
// about: a VHDX block, so a disk of any size takes at most a bit per VHDX
// block of memory. A chunk below the block size is one block. The size
// bounds what a damaged superblock can make it read.
func Read(r io.ReaderAt, size, chunk int64) (*FS, error) {
	// The superblock is 1024 bytes at offset 1024, whatever the block size.
	sb := make([]byte, 1024)
	if _, err := r.ReadAt(sb, superblockOffset); err != nil {
		return nil, fmt.Errorf("ext4: reading the superblock: %w", err)
	}
	if binary.LittleEndian.Uint16(sb[0x38:]) != superblockMagic {
		return nil, errors.New("ext4: no ext2/3/4 superblock")
	}
	// The block size is 1024 shifted left by s_log_block_size (at most 64 KiB).
	logBS := binary.LittleEndian.Uint32(sb[0x18:])
	if logBS > 6 {
		return nil, fmt.Errorf("ext4: block size 2^%d KiB", logBS)
	}
	fs := &FS{BlockSize: 1024 << logBS}
	// A chunk is a power of two and at least a block: a VHDX block is both.
	if chunk < fs.BlockSize || chunk&(chunk-1) != 0 {
		chunk = fs.BlockSize
	}
	fs.per = uint64(chunk / fs.BlockSize)
	compat := binary.LittleEndian.Uint32(sb[0x5C:])
	incompat := binary.LittleEndian.Uint32(sb[0x60:])
	roCompat := binary.LittleEndian.Uint32(sb[0x64:])
	is64 := incompat&incompat64Bit != 0
	// Block counts: the high halves exist only with the 64-bit feature.
	fs.Blocks = uint64(binary.LittleEndian.Uint32(sb[0x4:]))
	fs.FreeBlocks = uint64(binary.LittleEndian.Uint32(sb[0xC:]))
	if is64 {
		fs.Blocks |= uint64(binary.LittleEndian.Uint32(sb[0x150:])) << 32
		fs.FreeBlocks |= uint64(binary.LittleEndian.Uint32(sb[0x158:])) << 32
	}
	fs.NeedsRecovery = incompat&incompatRecover != 0
	fs.HasErrors = binary.LittleEndian.Uint16(sb[0x3A:])&2 != 0
	// Spread-out descriptors are rare (filesystems over 256 TiB, or resized
	// that way); reading them is not worth the risk of getting them wrong.
	if incompat&incompatMetaBG != 0 {
		return nil, errors.New("ext4: the meta_bg layout is not supported")
	}
	firstData := uint64(binary.LittleEndian.Uint32(sb[0x14:]))
	perGroup := uint64(binary.LittleEndian.Uint32(sb[0x20:]))
	// A group's bitmap is one block, so it covers at most 8 bits per byte.
	if perGroup == 0 || perGroup > uint64(fs.BlockSize)*8 || fs.Blocks <= firstData {
		return nil, errors.New("ext4: the superblock's geometry is inconsistent")
	}
	// A filesystem larger than its device is a damaged superblock; trusting
	// it would size the bitmap below after a made-up number.
	if size <= 0 || fs.Blocks > uint64(size)/uint64(fs.BlockSize) {
		return nil, fmt.Errorf("ext4: %d blocks of %d bytes do not fit on a %d-byte device", fs.Blocks, fs.BlockSize, size)
	}
	// Descriptors are 32 bytes, or s_desc_size with the 64-bit feature.
	descSize := uint64(32)
	if is64 {
		descSize = uint64(binary.LittleEndian.Uint16(sb[0xFE:]))
		if descSize < 64 || descSize > 1024 || descSize&(descSize-1) != 0 {
			return nil, fmt.Errorf("ext4: descriptor size %d", descSize)
		}
	}
	// Backup superblocks: which groups hold one depends on these features.
	sparse := roCompat&roCompatSparse != 0
	// The kernel honours BLOCK_UNINIT only with descriptor checksums; without
	// them the bitmap is read as it is.
	uninitOK := roCompat&(roCompatGDTCsum|roCompatMetaCsum) != 0
	// An uninitialised group still holds its own bitmaps and inode table when
	// flex_bg does not move them: their size is needed to mark them.
	inodeSize := uint64(128)
	if binary.LittleEndian.Uint32(sb[0x4C:]) >= 1 {
		inodeSize = uint64(binary.LittleEndian.Uint16(sb[0x58:]))
	}
	inodesPerGroup := uint64(binary.LittleEndian.Uint32(sb[0x28:]))
	// An inode is a power of two from 128 bytes up to a block; a group has
	// at most one inode bitmap's worth (8 per byte of a block) and its
	// inode table fits in the group. Past that the superblock is damaged,
	// and trusting it would mark up to the whole filesystem per group.
	if inodeSize < 128 || inodeSize > uint64(fs.BlockSize) || inodeSize&(inodeSize-1) != 0 ||
		inodesPerGroup == 0 || inodesPerGroup > uint64(fs.BlockSize)*8 {
		return nil, fmt.Errorf("ext4: %d inodes of %d bytes per group", inodesPerGroup, inodeSize)
	}
	itableBlocks := (inodesPerGroup*inodeSize + uint64(fs.BlockSize) - 1) / uint64(fs.BlockSize)
	if itableBlocks > perGroup {
		return nil, fmt.Errorf("ext4: an inode table of %d blocks in groups of %d", itableBlocks, perGroup)
	}
	sparse2 := compat&compatSparseSuper2 != 0
	backup1 := uint64(binary.LittleEndian.Uint32(sb[0x24C:]))
	backup2 := uint64(binary.LittleEndian.Uint32(sb[0x250:]))
	reservedGDT := uint64(binary.LittleEndian.Uint16(sb[0xCE:]))
	groups := (fs.Blocks - firstData + perGroup - 1) / perGroup
	// The descriptor table starts in the block after the superblock's. It
	// is read a block at a time as the groups go: its whole size follows a
	// number the superblock makes up when it is damaged.
	gdtBlock := firstData + 1
	gdtBlocks := (groups*descSize + uint64(fs.BlockSize) - 1) / uint64(fs.BlockSize)
	perGDTBlock := uint64(fs.BlockSize) / descSize
	gdt := make([]byte, fs.BlockSize)
	loaded := ^uint64(0)
	// One bit per chunk: at most a bit per VHDX block for any superblock.
	chunks := (fs.Blocks + fs.per - 1) / fs.per
	fs.used = make([]uint64, (chunks+63)/64)
	// Blocks before the first group (block 0 with 1 KiB blocks) are in use.
	for b := range firstData {
		fs.mark(b)
	}
	fs.UsedBlocks = firstData
	bitmap := make([]byte, fs.BlockSize)
	// own marks an uninitialised group's blocks once each, to count them:
	// a group is at most a bitmap's worth of blocks.
	own := make([]uint64, (perGroup+63)/64)
	for g := range groups {
		// This group's first block and its number of blocks (the last one
		// may be short).
		start := firstData + g*perGroup
		count := min(perGroup, fs.Blocks-start)
		// The descriptor table's block that holds this group's.
		if b := g / perGDTBlock; b != loaded {
			if _, err := r.ReadAt(gdt, int64(gdtBlock+b)*fs.BlockSize); err != nil {
				return nil, fmt.Errorf("ext4: reading the group descriptors: %w", err)
			}
			loaded = b
		}
		d := gdt[(g%perGDTBlock)*descSize : (g%perGDTBlock+1)*descSize]
		flags := binary.LittleEndian.Uint16(d[0x12:])
		if flags&bgBlockUninit != 0 && uninitOK {
			// The bitmap was never written: in use are the group's copy of
			// the superblock and descriptors, and the group's own block and
			// inode bitmaps and inode table, wherever they are: in this
			// group without flex_bg, in another with it. In another group
			// they are in that group's bitmap and counted with it; in this
			// one they are counted here, each once. A damaged table can
			// name blocks in another group whose bitmap does not mark them:
			// AnyInUse still sees them, UsedBlocks does not count them. A
			// shrink's check and its verify read the table the same way.
			clear(own)
			mine := func(b uint64) {
				fs.mark(b)
				if b >= start && b < start+count {
					own[(b-start)/64] |= 1 << ((b - start) % 64)
				}
			}
			if hasBackup(g, sparse, sparse2, backup1, backup2) {
				for b := range min(1+gdtBlocks+reservedGDT, count) {
					mine(start + b)
				}
			}
			field := func(lo, hi int) uint64 {
				v := uint64(binary.LittleEndian.Uint32(d[lo:]))
				if descSize >= 64 {
					v |= uint64(binary.LittleEndian.Uint32(d[hi:])) << 32
				}
				return v
			}
			for _, m := range []struct{ at, n uint64 }{{field(0x0, 0x20), 1}, {field(0x4, 0x24), 1}, {field(0x8, 0x28), itableBlocks}} {
				for b := m.at; b < m.at+m.n && b < fs.Blocks; b++ {
					mine(b)
				}
			}
			for _, w := range own {
				fs.UsedBlocks += uint64(bits.OnesCount64(w))
			}
			continue
		}
		// The bitmap's block number, with its high half on 64-bit.
		at := uint64(binary.LittleEndian.Uint32(d[0x0:]))
		if descSize >= 64 {
			at |= uint64(binary.LittleEndian.Uint32(d[0x20:])) << 32
		}
		if at == 0 || at >= fs.Blocks {
			return nil, fmt.Errorf("ext4: group %d's bitmap is at block %d", g, at)
		}
		if _, err := r.ReadAt(bitmap, int64(at)*fs.BlockSize); err != nil {
			return nil, fmt.Errorf("ext4: reading group %d's bitmap: %w", g, err)
		}
		fs.fold(bitmap, start, count)
	}
	return fs, nil
}

// fold counts the blocks a group's bitmap marks in use, of its first count
// bits, and marks their chunks: a 64-bit word at a time (the bitmap is
// little-endian, bit b of the group in byte b/8), and within a word one
// mark per chunk it touches.
func (fs *FS) fold(bitmap []byte, start, count uint64) {
	for i := uint64(0); i < count; i += 64 {
		var w uint64
		if i+64 <= uint64(len(bitmap))*8 {
			w = binary.LittleEndian.Uint64(bitmap[i/8:])
		} else {
			// A bitmap shorter than a word (a 1 KiB block holds 128 words):
			// the bytes that are there.
			for j := i / 8; j < uint64(len(bitmap)); j++ {
				w |= uint64(bitmap[j]) << (8 * (j - i/8))
			}
		}
		// Bits past the group's end (a short last group) are not blocks.
		if n := count - i; n < 64 {
			w &= 1<<n - 1
		}
		fs.UsedBlocks += uint64(bits.OnesCount64(w))
		// Each chunk the word touches is marked once: past the lowest bit
		// set, the bits of its chunk are cleared, and the next one found.
		base := start + i
		for w != 0 {
			b := base + uint64(bits.TrailingZeros64(w))
			c := b / fs.per
			fs.used[c/64] |= 1 << (c % 64)
			next := (c + 1) * fs.per
			if next-base >= 64 {
				break
			}
			w &^= 1<<(next-base) - 1
		}
	}
}

// mark records block b's chunk as in use.
func (fs *FS) mark(b uint64) {
	c := b / fs.per
	fs.used[c/64] |= 1 << (c % 64)
}

// hasBackup reports whether group g holds a backup of the superblock and
// descriptors (group 0 holds the primary copy).
func hasBackup(g uint64, sparse, sparse2 bool, b1, b2 uint64) bool {
	switch {
	case g == 0:
		return true
	case sparse2:
		// sparse_super2 names its (at most two) backup groups explicitly.
		return g == b1 || g == b2
	case !sparse:
		// Without sparse_super every group has a copy.
		return true
	case g == 1:
		return true
	}
	// Otherwise only powers of 3, 5 and 7.
	for _, base := range []uint64{3, 5, 7} {
		n := base
		for n < g {
			n *= base
		}
		if n == g {
			return true
		}
	}
	return false
}
