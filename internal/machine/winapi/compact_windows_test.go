//go:build windows

package winapi

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"github.com/kanggu12/wsl-shrinker/internal/engine/estimate"
	"github.com/kanggu12/wsl-shrinker/internal/machine/vhdx"
	"github.com/kanggu12/wsl-shrinker/internal/testutil/vhdxtest"
	"golang.org/x/sys/windows"
)

func TestCompactFileDropsOnlyZeroBlocks(t *testing.T) {
	// What the shrink's estimate and verify step rely on for files WSL does
	// not register: Windows' compaction of a file nobody has attached drops
	// the stored blocks that hold only zeros, and keeps every block with
	// data, whatever it is. The file is this test's own, in its temp folder.
	p := filepath.Join(t.TempDir(), "scratch.vhdx")
	blocks := map[int64][]byte{0: {1}, 9: {1}, 20: {7, 7, 7}}
	for i := int64(1); i < 9; i++ {
		// Stored, but zeros: eight blocks the compaction may drop.
		blocks[i] = []byte{}
	}
	before := vhdxtest.Write(t, p, vhdxtest.Spec{VirtualSize: 64 << 20, Blocks: blocks})
	if err := CompactFile(p); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	d, err := vhdx.Read(f, st.Size())
	if err != nil {
		t.Fatal(err)
	}
	// The eight blocks of zeros went, the three with data stayed.
	if d.PresentBlocks() != 3 || before-st.Size() != 8<<20 {
		t.Fatalf("%d blocks stored, %d bytes freed", d.PresentBlocks(), before-st.Size())
	}
	// And what they hold reads back as it was.
	for i, want := range map[int64][]byte{0: {1}, 9: {1}, 20: {7, 7, 7}} {
		got := make([]byte, len(want))
		if _, err := d.Contents().ReadAt(got, i<<20); err != nil || !bytes.Equal(got, want) {
			t.Fatalf("block %d reads %v, %v", i, got, err)
		}
	}
}

// procCreateVirtualDisk has Windows make a disk: only the tests need one.
var procCreateVirtualDisk = virtdisk.NewProc("CreateVirtualDisk")

// createParams is CREATE_VIRTUAL_DISK_PARAMETERS in its version 2, the one
// that makes a VHDX.
type createParams struct {
	Version uint32
	// The union after the version starts on a multiple of 8 bytes.
	_           uint32
	UniqueID    windows.GUID
	MaximumSize uint64
	// The three sizes, in bytes: a payload block, a logical sector, a
	// physical one (0 leaves it to Windows).
	BlockSize, SectorSize, PhysicalSectorSize uint32
	// No parent and no disk to copy from: both stay nil.
	ParentPath, SourcePath *uint16
	OpenFlags              uint32
	ParentType, SourceType virtualStorageType
	ResiliencyGUID         windows.GUID
}

// windowsDisk has Windows make an empty VHDX of size bytes at path, in 1 MiB
// blocks; fixed-size when asked, one that grows otherwise. The file is the
// caller's own, and is never attached.
func windowsDisk(t *testing.T, path string, size uint64, fixed bool) {
	t.Helper()
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	// VIRTUAL_STORAGE_TYPE_DEVICE_VHDX and the Microsoft vendor GUID.
	st := virtualStorageType{DeviceID: 3, VendorID: windows.GUID{Data1: 0xEC984AEC, Data2: 0xA0F9, Data3: 0x47E9,
		Data4: [8]byte{0x90, 0x1F, 0x71, 0x41, 0x5A, 0x66, 0x34, 0x5B}}}
	params := createParams{Version: 2, MaximumSize: size, BlockSize: 1 << 20, SectorSize: 512}
	// CREATE_VIRTUAL_DISK_FLAG_FULL_PHYSICAL_ALLOCATION is what makes a
	// disk fixed-size.
	flags := uintptr(0)
	if fixed {
		flags = 1
	}
	var h windows.Handle
	// Version 2 takes no access mask (VIRTUAL_DISK_ACCESS_NONE), no security
	// descriptor and no overlapped call.
	r, _, _ := procCreateVirtualDisk.Call(uintptr(unsafe.Pointer(&st)), uintptr(unsafe.Pointer(p)), 0, 0, flags, 0,
		uintptr(unsafe.Pointer(&params)), 0, uintptr(unsafe.Pointer(&h)))
	if r != 0 {
		t.Fatalf("Windows did not make %s: %v", path, windows.Errno(r))
	}
	// Closed here: the compaction opens the file itself, and wants it free.
	if err := windows.CloseHandle(h); err != nil {
		t.Fatal(err)
	}
}

// seen is what the tests ask of a disk's file.
type seen struct {
	// fixed is whether the reader takes it for a fixed-size disk.
	fixed bool
	// stored is how many payload blocks the file holds.
	stored int
	// size is the file's own size, in bytes.
	size int64
	// The two estimates: for WSL's compaction, and for Windows' own.
	trimmed, untrimmed estimate.Result
}

// see reads the VHDX at path and closes it again: a file someone still has
// open is not compacted, and a test would pass for that reason alone.
func see(t *testing.T, path string) seen {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	d, err := vhdx.Read(f, st.Size())
	if err != nil {
		t.Fatal(err)
	}
	return seen{fixed: d.Fixed, stored: d.PresentBlocks(), size: st.Size(),
		trimmed: estimate.Of(d, nil, st.Size()), untrimmed: estimate.OfUntrimmed(d, nil, st.Size())}
}

func TestCompactFileLeavesAFixedSizeDiskWhole(t *testing.T) {
	// What the fixed-size rule rests on. Windows' compaction of a fixed-size
	// disk reports success and takes nothing out of the file, even where
	// every block holds zeros. The estimate has to say the same beforehand,
	// or a shrink would stop WSL for a number that never comes.
	const virtual, all = 32 << 20, 32
	// Every block stored, all of them zeros but two: thirty that a disk
	// which grows gives back.
	blocks := map[int64][]byte{0: {1}, 9: {7}}
	for i := int64(0); i < all; i++ {
		if _, ok := blocks[i]; !ok {
			blocks[i] = []byte{}
		}
	}
	for name, write := range map[string]func(t *testing.T, p string){
		// The real thing: a fixed-size disk as Windows itself makes one.
		"made by Windows": func(t *testing.T, p string) { windowsDisk(t, p, virtual, true) },
		// And one with data in it, from this repository's own writer.
		"with data": func(t *testing.T, p string) {
			vhdxtest.Write(t, p, vhdxtest.Spec{VirtualSize: virtual, Blocks: blocks, Fixed: true})
		},
	} {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "fixed.vhdx")
			write(t, p)
			before := see(t, p)
			// The reader tells it from a disk that grows, and it holds every
			// block: the file is larger than the disk inside it.
			if !before.fixed || before.stored != all || before.size < virtual {
				t.Fatalf("%+v", before)
			}
			// The estimate, either way: nothing comes back.
			for _, r := range []estimate.Result{before.trimmed, before.untrimmed} {
				if !r.Fixed || r.After != before.size || r.Reclaim != 0 {
					t.Fatalf("%+v", r)
				}
			}
			if err := CompactFile(p); err != nil {
				t.Fatal(err)
			}
			// As estimated: the same size, with every block still in it.
			if after := see(t, p); after.size != before.size || after.stored != all {
				t.Fatalf("%d bytes before, %d after, %d blocks stored", before.size, after.size, after.stored)
			}
		})
	}
	// The flag alone decides: the same blocks in a disk that grows lose the
	// thirty of zeros, as that disk's estimate said.
	p := filepath.Join(t.TempDir(), "grows.vhdx")
	vhdxtest.Write(t, p, vhdxtest.Spec{VirtualSize: virtual, Blocks: blocks})
	before := see(t, p)
	if err := CompactFile(p); err != nil {
		t.Fatal(err)
	}
	if after := see(t, p); after.fixed || after.stored != 2 || before.size-after.size != 30<<20 {
		t.Fatalf("fixed %v, %d blocks stored, %d bytes freed", after.fixed, after.stored, before.size-after.size)
	}
	// And a disk Windows makes to grow is not taken for a fixed one.
	q := filepath.Join(t.TempDir(), "empty.vhdx")
	windowsDisk(t, q, virtual, false)
	if d := see(t, q); d.fixed || d.stored != 0 {
		t.Fatalf("%+v", d)
	}
}

func TestHeldWhileAnotherProgramWrites(t *testing.T) {
	// A file open for writing elsewhere (as WSL's VM holds an attached disk)
	// reads as held, whether the other program shares it or not; an idle
	// file does not.
	for name, share := range map[string]uint32{"shared": windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE, "exclusive": 0} {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "held.vhdx")
			if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			if fi, err := Stat(p); err != nil || fi.Held {
				t.Fatalf("idle: %+v %v", fi, err)
			}
			u, err := windows.UTF16PtrFromString(p)
			if err != nil {
				t.Fatal(err)
			}
			h, err := windows.CreateFile(u, windows.GENERIC_READ|windows.GENERIC_WRITE, share, nil, windows.OPEN_EXISTING, 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer windows.CloseHandle(h)
			if fi, err := Stat(p); err != nil || !fi.Held {
				t.Fatalf("open for writing: %+v %v", fi, err)
			}
		})
	}
}
