// Package history keeps one line per shrink run in a JSON Lines file, so the
// screen can show what was freed and when. The file stays small: past
// maxEntries runs, the oldest go.
package history

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// maxEntries is how many runs the file keeps; a run is a few hundred bytes,
// so the file stays well under a megabyte.
const maxEntries = 500

// Entry is one run.
type Entry struct {
	At     time.Time `json:"at"`
	Disk   string    `json:"disk"`
	Before int64     `json:"before"`
	After  int64     `json:"after"`
	Freed  int64     `json:"freed"`
	Took   float64   `json:"took_seconds"`
	Err    string    `json:"error,omitempty"`
	// Cleaned is what was removed inside the distribution first.
	Cleaned int64 `json:"cleaned,omitempty"`
}

// Store is the history file.
type Store struct{ Path string }

// Open returns the store in dir (created on the first write).
func Open(dir string) Store { return Store{Path: filepath.Join(dir, "history.jsonl")} }

// Add appends an entry, dropping the oldest past maxEntries.
func (s Store) Add(e Entry) error {
	all, _ := s.All()
	all = append(all, e)
	if len(all) > maxEntries {
		all = all[len(all)-maxEntries:]
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	// Write a new file and rename it over the old one, so a crash leaves
	// one whole file or the other.
	tmp := s.Path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	for _, x := range all {
		if err := enc.Encode(x); err != nil {
			f.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path)
}

// All returns every entry, oldest first. A missing file is no history; a
// damaged line is skipped.
func (s Store) All() ([]Entry, error) {
	f, err := os.Open(s.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []Entry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			out = append(out, e)
		}
	}
	return out, sc.Err()
}
