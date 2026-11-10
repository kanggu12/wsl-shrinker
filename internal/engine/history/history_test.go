package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func TestOverlongLineDoesNotCostTheRest(t *testing.T) {
	// A stretch of a damaged file with no line break (NULs left by a crash,
	// say) is longer than any line: it is skipped like any damaged line, and
	// the next Add keeps every entry around it.
	s := Open(t.TempDir())
	for i := range 3 {
		if err := s.Add(Entry{At: time.Unix(int64(i), 0), Disk: "a"}); err != nil {
			t.Fatal(err)
		}
	}
	good, err := os.ReadFile(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	junk := strings.Repeat("\x00", 3<<20) + "\n"
	if err := os.WriteFile(s.Path, append([]byte(junk), good...), 0o644); err != nil {
		t.Fatal(err)
	}
	if all, err := s.All(); err != nil || len(all) != 3 {
		t.Fatalf("read %d entries, %v", len(all), err)
	}
	if err := s.Add(Entry{At: time.Unix(9, 0), Disk: "b"}); err != nil {
		t.Fatal(err)
	}
	if all, err := s.All(); err != nil || len(all) != 4 {
		t.Fatalf("after Add: %d entries, %v", len(all), err)
	}
}

func TestEntryJSONKeys(t *testing.T) {
	// Every byte count says so in its key, as in the rest of --json.
	b, err := json.Marshal(Entry{At: time.Unix(0, 0).UTC(), Disk: "d", Before: 1, After: 2, Freed: 3, Took: 4.5, Err: "x", Cleaned: 5})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"at":"1970-01-01T00:00:00Z","disk":"d","before_bytes":1,"after_bytes":2,"freed_bytes":3,"took_seconds":4.5,"error":"x","cleaned_bytes":5}`
	if string(b) != want {
		t.Fatalf("got  %s\nwant %s", b, want)
	}
}

func TestOlderKeysStillRead(t *testing.T) {
	// A file written before the keys said "_bytes" keeps its numbers.
	s := Open(t.TempDir())
	line := `{"at":"1970-01-01T00:00:00Z","disk":"d","before":10,"after":4,"freed":6,"took_seconds":1,"cleaned":2}` + "\n"
	if err := os.WriteFile(s.Path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	all, err := s.All()
	if err != nil || len(all) != 1 {
		t.Fatalf("%v %v", all, err)
	}
	if e := all[0]; e.Before != 10 || e.After != 4 || e.Freed != 6 || e.Cleaned != 2 {
		t.Fatalf("%+v", e)
	}
}

func TestClearRemovesTheRunsAndWhatACutShortWriteLeft(t *testing.T) {
	dir := t.TempDir()
	s := Open(dir)
	// No history is nothing to clear, not an error.
	if err := s.Clear(); err != nil {
		t.Fatalf("clearing no history: %v", err)
	}
	for i := range 3 {
		if err := s.Add(Entry{At: time.Unix(int64(i), 0), Disk: "d"}); err != nil {
			t.Fatal(err)
		}
	}
	// A temporary file a write cut short left, and a file that is not the
	// history's: only the first goes with the history.
	left, other := filepath.Join(dir, "history-123.tmp"), filepath.Join(dir, "config.toml")
	for _, p := range []string{left, other} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	if all, err := s.All(); err != nil || len(all) != 0 {
		t.Fatalf("after clearing: %v %v", all, err)
	}
	for p, want := range map[string]bool{s.Path: false, left: false, other: true} {
		if _, err := os.Stat(p); (err == nil) != want {
			t.Errorf("%s: there %v, want %v", filepath.Base(p), err == nil, want)
		}
	}
	// A history cleared takes new runs as before.
	if err := s.Add(Entry{Disk: "again"}); err != nil {
		t.Fatal(err)
	}
	if all, _ := s.All(); len(all) != 1 || all[0].Disk != "again" {
		t.Fatalf("after a new run: %v", all)
	}
}
