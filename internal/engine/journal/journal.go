// Package journal keeps the shrink in progress on disk. A run that ends,
// however it went, has settled what it stopped and removes the journal; a
// run cut off (the window closed, a crash, the power) leaves it behind, and
// the next start of the program reads in it what to start again. The same
// file keeps two runs from going at once.
package journal

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Entry is the run in progress, as far as it got.
type Entry struct {
	// PID and Exe name the process running it: a run still going is told
	// from one cut off by both, as a process id alone is given out again.
	PID int    `json:"pid"`
	Exe string `json:"exe"`
	// Started is when it began; Version, which build began it.
	Started time.Time `json:"started"`
	Version string    `json:"version,omitempty"`
	// Disk is the disk's name; Distro its distribution, "" for a file.
	Disk   string `json:"disk"`
	Distro string `json:"distro,omitempty"`
	// Step is the last step that began.
	Step string `json:"step"`
	// Stopped is set once the run began to stop the distribution's
	// containers: from then on it ran, and must run again afterwards.
	Stopped bool `json:"stopped"`
	// Containers are the ones the run stopped, to start again.
	Containers []string `json:"containers,omitempty"`
}

// State is what the journal on disk means.
type State int

const (
	// None: no run is going on, and none was cut off.
	None State = iota
	// Going: a run is going on in another process.
	Going
	// CutOff: a run ended without settling what it stopped.
	CutOff
)

// ErrBusy refuses a run while another is going on, or while a run that was
// cut off waits to be finished: a new run would lose what it stopped.
var ErrBusy = errors.New("another run is not finished")

// Store is the journal file.
type Store struct {
	// Path is the file, run.json next to the history.
	Path string
	// Alive reports whether process pid runs the program named exe; the
	// app gives the system's way, tests their own.
	Alive func(pid int, exe string) bool
	// Since says when process pid started, when the system says; nil
	// leaves it unasked. A process that started after a run began is not
	// the one that began it, but one given its id again, as after a
	// restart of Windows: that run was cut off.
	Since func(pid int) (time.Time, bool)
}

// Open returns the journal kept in dir.
func Open(dir string, alive func(pid int, exe string) bool) Store {
	return Store{Path: filepath.Join(dir, "run.json"), Alive: alive}
}

// Read returns the journal's entry and what it means. A journal that does
// not parse (cut off as it was written) is a run cut off with nothing
// known about it.
func (s Store) Read() (Entry, State, error) {
	b, err := os.ReadFile(s.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return Entry{}, None, nil
	}
	if err != nil {
		return Entry{}, None, err
	}
	var e Entry
	if json.Unmarshal(b, &e) != nil {
		// A journal just made is a run starting, whose entry is on its way
		// (Begin writes it whole where the drive allows; elsewhere it comes
		// a moment after the file). Only an old one is a run cut off.
		if st, err := os.Stat(s.Path); err == nil && time.Since(st.ModTime()) < youngJournal {
			return Entry{}, Going, nil
		}
		return Entry{}, CutOff, nil
	}
	// A run still going is one whose process still runs this program, and
	// started before the run began.
	if e.PID > 0 && s.Alive != nil && s.Alive(e.PID, e.Exe) && !s.reused(e) {
		return e, Going, nil
	}
	return e, CutOff, nil
}

// reused reports whether e's process id now belongs to a process that
// started after e's run began. A second of leeway covers the clocks'
// rounding: the process that began a run started before it, always.
func (s Store) reused(e Entry) bool {
	if s.Since == nil || e.Started.IsZero() {
		return false
	}
	t, ok := s.Since(e.PID)
	return ok && t.After(e.Started.Add(time.Second))
}

// Begin claims the journal for a run. It is refused (ErrBusy) when another
// run is going on, or when one was cut off and has not been finished.
func (s Store) Begin(e Entry) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	// Another run's journal found there may be gone before it is read: that
	// run just ended, and the start is tried again, a few times.
	for range 3 {
		if err := s.create(b); !errors.Is(err, errEnded) {
			return err
		}
	}
	return fmt.Errorf("%w: other runs started and ended while this one tried to; try again", ErrBusy)
}

// errEnded is another run's journal found and then gone: that run ended.
var errEnded = errors.New("the other run ended")

// raced is called between finding another run's journal and reading it;
// a test removes the journal there, as that run's end would.
var raced = func() {}

// create writes b as the journal where none is; busy() when one is.
func (s Store) create(b []byte) error {
	// The entry is written whole first, then linked under the real name:
	// created only where none is, so of two runs starting at once one gets
	// it, and nobody reads it half written.
	tmp, err := s.writeTemp(b)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	switch err := os.Link(tmp, s.Path); {
	case err == nil:
		return nil
	case errors.Is(err, fs.ErrExist):
		raced()
		return s.busy()
	}
	// A drive without links: created, then written; Read takes a journal
	// that young for a run starting.
	f, err := os.OpenFile(s.Path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		raced()
		return s.busy()
	}
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Name is the disk the entry's run shrinks, as a message says it: "a
// disk" when the journal does not say, as a run's that is just starting.
func (e Entry) Name() string {
	if e.Disk == "" {
		return "a disk"
	}
	return e.Disk
}

// Same reports whether e and o are the same run: the same process, started
// at the same moment, on the same disk. Two entries of a journal that
// does not parse are the same too: neither says more.
func (e Entry) Same(o Entry) bool {
	return e.PID == o.PID && e.Started.Equal(o.Started) && e.Disk == o.Disk
}

// youngJournal is how new a journal that does not parse yet must be to be
// a run starting rather than one cut off.
const youngJournal = 10 * time.Second

// writeTemp writes b into a new file beside the journal, on the drive
// before it returns, and gives its path.
func (s Store) writeTemp(b []byte) (string, error) {
	f, err := os.CreateTemp(filepath.Dir(s.Path), "run-*.tmp")
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	if _, err := f.Write(b); err != nil {
		f.Close()
		os.Remove(tmp)
		return "", err
	}
	// On the drive before it takes the journal's name: a power cut never
	// leaves an empty journal under the real name.
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return tmp, nil
}

// Words says, in the command line's English, a run that has not ended and
// what to do about it; "" for none. Every front that prints English says
// it in these words.
func Words(e Entry, st State) (what, fix string) {
	switch {
	case st == Going:
		return e.Name() + " is being shrunk by another wsl-shrinker", "let it finish"
	case st == CutOff && (e.Disk == "" || e.Step == ""):
		return "a run was cut off", "wsl-shrinker recover finishes it"
	case st == CutOff:
		return "the run of " + e.Disk + " was cut off at " + e.Step, "wsl-shrinker recover finishes it"
	}
	return "", ""
}

// busy says why the journal is taken; errEnded when it no longer is.
func (s Store) busy() error {
	old, st, err := s.Read()
	if err != nil {
		return err
	}
	if st == None {
		return errEnded
	}
	what, fix := Words(old, st)
	if st == Going {
		return fmt.Errorf("%w: %s", ErrBusy, what)
	}
	return fmt.Errorf("%w: %s; %s", ErrBusy, what, fix)
}

// Update writes the entry again: the step it reached, the containers it
// stopped. The new text replaces the old whole, through a file of its own,
// so a crash leaves one or the other.
func (s Store) Update(e Entry) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	tmp, err := s.writeTemp(b)
	if err != nil {
		return err
	}
	// Windows refuses to replace a file another process has open, as the
	// screen or doctor reading it does for a moment: a second later it is
	// free again.
	for try := 0; ; try++ {
		err = os.Rename(tmp, s.Path)
		if err == nil || try == updateTries-1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

// updateTries is how often Update tries to replace the journal: 50 ms
// apart, a second in all.
const updateTries = 20

// End removes the journal: the run ended and settled, or a run cut off was
// finished.
func (s Store) End() error {
	err := os.Remove(s.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
