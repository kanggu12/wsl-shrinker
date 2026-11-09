package vhdx

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/kanggu12/wsl-shrinker/internal/testutil/vhdxtest"
)

// A BAT entry whose offset sits just under 2^63: offset+BlockSize wraps
// negative and passes the "inside the file" check.
func TestBATOffsetOverflow(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.vhdx")
	vhdxtest.Write(t, p, vhdxtest.Spec{VirtualSize: 16 << 20, BlockSize: 8 << 20, Blocks: map[int64][]byte{0: {1}}})
	f, _ := os.OpenFile(p, os.O_RDWR, 0)
	st, _ := f.Stat()
	d, err := Read(f, st.Size())
	if err != nil {
		t.Fatal(err)
	}
	e := (uint64(1)<<43-1)<<20 | stateFullyPresent
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, e)
	f.WriteAt(b, d.BATOffset)
	d, err = Read(f, st.Size())
	f.Close()
	if err == nil {
		t.Errorf("accepted block 0 at offset %d (file is %d bytes)", d.Blocks[0].Offset, st.Size())
	}
}
