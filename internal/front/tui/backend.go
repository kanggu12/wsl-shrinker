package tui

import (
	"context"

	"github.com/kanggu12/wsl-shrinker/internal/engine/disks"
	"github.com/kanggu12/wsl-shrinker/internal/engine/history"
	"github.com/kanggu12/wsl-shrinker/internal/engine/shrink"
	"github.com/kanggu12/wsl-shrinker/internal/engine/survey"
	"github.com/kanggu12/wsl-shrinker/internal/machine/wsl"
)

// Backend is what the screen asks of the rest of the program. The real one
// reaches WSL and Windows; the demo one plays a made-up PC.
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
}
