package tui

import (
	"context"

	"github.com/kanggu12/wsl-shrinker/internal/engine/disks"
	"github.com/kanggu12/wsl-shrinker/internal/engine/history"
	"github.com/kanggu12/wsl-shrinker/internal/engine/journal"
	"github.com/kanggu12/wsl-shrinker/internal/engine/shrink"
	"github.com/kanggu12/wsl-shrinker/internal/engine/survey"
	"github.com/kanggu12/wsl-shrinker/internal/machine/wsl"
)

// Backend is what the screen asks of the rest of the program. app.Backend
// reaches WSL and Windows itself; remote.Backend asks wsl-shrinker.exe,
// for the screen in a WSL terminal; the demo plays a made-up PC.
type Backend interface {
	// Info is WSL's version and whether it can compact.
	Info(ctx context.Context) wsl.Info
	// Disks lists the disks with their estimates.
	Disks(ctx context.Context) ([]disks.Disk, error)
	// Survey looks inside a running distribution.
	Survey(ctx context.Context, distro string) (survey.Result, error)
	// Shrink runs one disk, reporting steps through emit.
	Shrink(ctx context.Context, d disks.Disk, opt shrink.Options, emit func(shrink.Event)) shrink.Result
	// History returns past runs, oldest first.
	History() []history.Entry
	// Protected reports whether the settings protect a disk.
	Protected(name string) bool
	// MinGain is the least worth shrinking, in bytes.
	MinGain() int64
	// Hosts reports whether this program's terminal runs inside the named
	// distribution, which a shrink would stop, closing the terminal.
	Hosts(distro string) bool
	// Reopen continues in a new window with these arguments.
	Reopen(args []string) error
	// Pending is a run that has not ended: going on in another process,
	// or cut off and waiting to be finished.
	Pending() (journal.Entry, journal.State)
	// Recover finishes a run that was cut off, and says what it did. A
	// start that failed comes back as app.StillCutOff: the run stays cut
	// off, and Forget is the other way out of it.
	Recover(ctx context.Context) (string, error)
	// Forget closes the run that was cut off, as it is, starting nothing
	// again: run, the one the screen asked about, and no other.
	Forget(ctx context.Context, run journal.Entry) error
}
