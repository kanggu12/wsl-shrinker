package history

import (
	"os"
	"testing"
	"time"
)

func TestAddAndCap(t *testing.T) {
	s := Open(t.TempDir())
	// No file yet: no history, no error.
	if all, err := s.All(); err != nil || len(all) != 0 {
		t.Fatalf("%v %v", all, err)
	}
	for i := range maxEntries + 20 {
		if err := s.Add(Entry{At: time.Unix(int64(i), 0), Disk: "d", Freed: int64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.All()
	if err != nil {
		t.Fatal(err)
	}
	// Only the newest maxEntries stay, oldest first.
	if len(all) != maxEntries || all[0].Freed != 20 || all[len(all)-1].Freed != maxEntries+19 {
		t.Fatalf("%d entries, first %d", len(all), all[0].Freed)
	}
	// The file stays small.
	st, _ := os.Stat(s.Path)
	if st.Size() > 1<<20 {
		t.Fatalf("history file is %d bytes", st.Size())
	}
}

func TestDamagedLineSkipped(t *testing.T) {
	s := Open(t.TempDir())
	if err := os.WriteFile(s.Path, []byte("{\"disk\":\"a\"}\nnot json\n{\"disk\":\"b\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	all, err := s.All()
	if err != nil || len(all) != 2 || all[1].Disk != "b" {
		t.Fatalf("%v %v", all, err)
	}
}
