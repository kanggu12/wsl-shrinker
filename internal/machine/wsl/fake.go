package wsl

import (
	"context"
	"fmt"
	"slices"
	"sync"
)

// Fake is a System held in memory, for the tests and the demo. Calls that
// change something are recorded in Calls, in order.
type Fake struct {
	mu sync.Mutex
	// List is what Distros returns.
	List []Distro
	// Version and NoCompact shape what Info returns.
	Version   string
	NoCompact bool
	// Script answers Run; nil answers with empty output.
	Script func(distro, user, script string) (string, error)
	// OnCompact runs inside Compact, to change the disk file as WSL would.
	OnCompact func(distro string) error
	// Fail makes the named calls fail ("compact:Ubuntu", "start:Ubuntu").
	Fail map[string]error
	// Calls records "terminate:X", "start:X", "compact:X" and "run:X".
	Calls []string
}

// Distros returns the list.
func (f *Fake) Distros(context.Context) ([]Distro, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.List), nil
}

// Info returns the version.
func (f *Fake) Info(context.Context) (Info, error) {
	v := f.Version
	if v == "" {
		v = "3.0.3.0"
	}
	return Info{Version: v, CanCompact: !f.NoCompact}, nil
}

// Run answers with Script, and refuses a stopped distribution as WSL's
// real Run does.
func (f *Fake) Run(ctx context.Context, distro, user, script string) (string, error) {
	if err := f.record("run", distro); err != nil {
		return "", err
	}
	// A cancelled context fails the call, as it kills wsl.exe for real.
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !f.running(distro) {
		return "", fmt.Errorf("%s is not running", distro)
	}
	if f.Script == nil {
		return "", nil
	}
	return f.Script(distro, user, script)
}

// Start marks the distribution running.
func (f *Fake) Start(ctx context.Context, distro string) error {
	if err := f.record("start", distro); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	f.set(distro, true)
	return nil
}

// Terminate marks it stopped.
func (f *Fake) Terminate(_ context.Context, distro string) error {
	if err := f.record("terminate", distro); err != nil {
		return err
	}
	f.set(distro, false)
	return nil
}

// Compact stops the distribution, as WSL does, then calls OnCompact.
func (f *Fake) Compact(ctx context.Context, distro string) error {
	if err := f.record("compact", distro); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	f.set(distro, false)
	if f.OnCompact != nil {
		return f.OnCompact(distro)
	}
	return nil
}

// record notes a call and returns the failure set up for it.
func (f *Fake) record(op, distro string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := op + ":" + distro
	f.Calls = append(f.Calls, key)
	return f.Fail[key]
}

// running reports whether a distribution runs.
func (f *Fake) running(distro string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, d := range f.List {
		if d.Name == distro {
			return d.Running
		}
	}
	return false
}

// set changes whether a distribution runs.
func (f *Fake) set(distro string, on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.List {
		if f.List[i].Name == distro {
			f.List[i].Running = on
		}
	}
}

// CallList returns a copy of the calls so far.
func (f *Fake) CallList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.Calls)
}
