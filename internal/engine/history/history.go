// Package history keeps one line per shrink run in a JSON Lines file, so the
// screen can show what was freed and when. The file stays small: past
// maxEntries runs, the oldest go.
package history

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

// maxEntries is how many runs the file keeps; a run is a few hundred bytes,
// so the file stays well under a megabyte.
const maxEntries = 500

// maxLine is the longest line read as an entry. An entry is a few hundred
// bytes; anything this long is damage (NULs left by a crash, say), skipped
// without holding it in memory.
const maxLine = 64 << 10

// Entry is one run.
type Entry struct {
	At     time.Time `json:"at"`
	Disk   string    `json:"disk"`
	Before int64     `json:"before_bytes"`
	After  int64     `json:"after_bytes"`
	Freed  int64     `json:"freed_bytes"`
	Took   float64   `json:"took_seconds"`
	Err    string    `json:"error,omitempty"`
	// Cleaned is what was removed inside the distribution first.
	Cleaned int64 `json:"cleaned_bytes,omitempty"`
	// Version is the build that ran it, for a bug report.
	Version string `json:"version,omitempty"`
	// FailedAt, Compacted and Left say how a run ended, as the engine
	// reported it: the step that failed, whether the disk was compacted,
	// and how the distribution was left.
	FailedAt  string `json:"failed_at,omitempty"`
	Compacted bool   `json:"compacted,omitempty"`
	Left      string `json:"left,omitempty"`
	// Events are the run's steps as they went, kept as the engine wrote
	// them (this store does not read them).
	Events json.RawMessage `json:"events,omitempty"`
}

// UnmarshalJSON reads an entry, also from files written before the byte
// counts' keys ended in "_bytes", so an older history keeps its numbers.
func (e *Entry) UnmarshalJSON(b []byte) error {
	// The current keys, through a type without this method (no recursion).
	type current Entry
	var c current
	if err := json.Unmarshal(b, &c); err != nil {
		return err
	}
	// The older keys, read alongside; each is used only where the current
	// key is missing.
	var old struct {
		Before, After, Freed, Cleaned *int64
	}
	if err := json.Unmarshal(b, &old); err != nil {
		return err
	}
	// Which current keys the line has, so a real zero is not replaced.
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(b, &keys); err != nil {
		return err
	}
	fill := func(dst *int64, key string, v *int64) {
		if _, ok := keys[key]; !ok && v != nil {
			*dst = *v
		}
	}
	fill(&c.Before, "before_bytes", old.Before)
	fill(&c.After, "after_bytes", old.After)
	fill(&c.Freed, "freed_bytes", old.Freed)
	fill(&c.Cleaned, "cleaned_bytes", old.Cleaned)
	*e = Entry(c)
	return nil
}

// Store is the history file.
type Store struct{ Path string }

// Open returns the store in dir (created on the first write).
func Open(dir string) Store { return Store{Path: filepath.Join(dir, "history.jsonl")} }

// Add appends an entry, dropping the oldest past maxEntries.
func (s Store) Add(e Entry) error {
	// A history that cannot be read is left as it is: rewriting it from
	// what was read would drop every entry past the problem.
	all, err := s.All()
	if err != nil {
		return err
	}
	all = append(all, e)
	if len(all) > maxEntries {
		all = all[len(all)-maxEntries:]
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	// Write a new file and rename it over the old one, so a crash leaves
	// one whole file or the other. The name is this write's own: two
	// programs adding at once never write into the same temporary file.
	f, err := os.CreateTemp(filepath.Dir(s.Path), "history-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	// Whatever goes wrong below, the temporary file does not stay behind.
	done := false
	defer func() {
		if !done {
			f.Close()
			os.Remove(tmp)
		}
	}()
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	for _, x := range all {
		if err := enc.Encode(x); err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	// On the drive before the rename, so a power cut never leaves the new
	// name on an empty file.
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.Path); err != nil {
		os.Remove(tmp)
		done = true
		return err
	}
	done = true
	return nil
}

// Clear removes every run: the file, and a temporary one a write that was
// cut short left beside it. No history is nothing to clear, not an error.
func (s Store) Clear() error {
	if err := os.Remove(s.Path); err != nil && !os.IsNotExist(err) {
		return err
	}
	// Add removes its temporary file itself; only a crash leaves one.
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(s.Path), "history-*.tmp"))
	for _, p := range left {
		_ = os.Remove(p)
	}
	return nil
}

// All returns every entry, oldest first. A missing file is no history; a
// damaged line, however long, is skipped.
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
	r := bufio.NewReaderSize(f, maxLine)
	for {
		// ReadSlice returns at most a buffer's worth: a longer line comes in
		// pieces, all of which are dropped.
		line, err := r.ReadSlice('\n')
		long := errors.Is(err, bufio.ErrBufferFull)
		for errors.Is(err, bufio.ErrBufferFull) {
			_, err = r.ReadSlice('\n')
		}
		if !long {
			var e Entry
			if t := bytes.TrimSpace(line); len(t) > 0 && json.Unmarshal(t, &e) == nil {
				out = append(out, e)
			}
		}
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return out, err
		}
	}
}
