//go:build windows

package winapi

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

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
