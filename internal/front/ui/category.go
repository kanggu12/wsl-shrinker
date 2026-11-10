package ui

import (
	"github.com/kanggu12/wsl-shrinker/internal/engine/disks"
	"github.com/kanggu12/wsl-shrinker/internal/engine/shrink"
)

// CategoryOf is the color a disk takes on the screen, in setup and in CLI
// mode alike, from the first thing that stands in the way of a shrink (b,
// "" for none) and the size worth shrinking: protected, in use by its
// program, cannot be shrunk now, worth it, or giving back little.
func CategoryOf(d disks.Disk, b shrink.Block, minGain int64) Category {
	switch b {
	case shrink.BlockProtected:
		return CatSafe
	case shrink.BlockHeld:
		// In use by its program: it can be shrunk once that program stops.
		return CatLive
	case "":
		// Nothing in the way: what it gives back decides. A disk with no
		// estimate is never one with nothing in the way; read as none.
		if d.Estimate != nil && d.Estimate.Reclaim >= minGain {
			return CatGain
		}
		return CatSmall
	}
	// Any other reason is one it cannot be shrunk for now.
	return CatBad
}
