# Troubleshooting

Start with `wsl-shrinker doctor`: it checks WSL, the settings and every disk, and says what to do about each problem.

## "this WSL cannot compact"

Shrinking a distribution uses `wsl --manage <distro> --compact`, in WSL 3.0.3 and later. `wsl --update` installs it.
Docker Desktop's disk does not need it.

## A disk gave back less than its estimate

- **It was read while running.** The estimate of a running distribution can be off by the last few seconds of
  writes, and the stop step writes a little too. A difference of more than a few hundred MB is worth an issue.
- **Something wrote between the estimate and the shrink.** The estimate is taken when the screen reads the disks;
  `r` reads them again.
- **The file is sparse.** A sparse disk (`wsl --manage <distro> --set-sparse true`) already gives space back on its
  own; the screen marks it.

## "the disk file is encrypted"

Windows cannot compact a file encrypted with EFS (error 0x80071772). Turn encryption off in the file's Properties,
Advanced, then shrink it.

## Docker Desktop's disk is "in use"

Docker Desktop holds its data disk while it runs. Quit it from its tray icon, then shrink the disk.

## "the root filesystem stays read-only"

After compacting, WSL has been seen to leave a distribution's root mounted read-only. wsl-shrinker stops and starts the
distribution once more; when that is not enough, `wsl --shutdown` restarts WSL and clears it. It stops every running
distribution, so wsl-shrinker leaves it to you.

## "the filesystem records errors"

The kernel noted errors in the disk's filesystem before any shrink, and wsl-shrinker leaves the disk alone until e2fsck
has checked it. Stop the distribution (`wsl --terminate <distro>`; for Docker Desktop's disk, quit Docker Desktop),
attach the disk without mounting it (`wsl --mount --vhd <file> --bare`, as administrator), run `sudo e2fsck -f /dev/sdX`
from another distribution (`lsblk` names it), then detach it with `wsl --unmount <file>`.

## "more went than was cleaned"

After compacting, the disk does not read as before: the filesystem uses noticeably less than before, beyond what you
chose to clean, blocks it uses are no longer stored, or it records new errors. wsl-shrinker stops there and leaves the
distribution stopped. Do not use it until you know why: attach the disk to WSL without mounting it
(`wsl --mount --vhd <file> --bare`, as administrator), check it from another distribution with
`sudo e2fsck -fn /dev/sdX` (`lsblk` names it), and open an issue with `wsl-shrinker history --json`.

When the file only could not be opened again ("cannot be opened after compacting"), nothing says the disk changed: the
distribution is started again, and `r` on the screen reads the disk again once the program that had it lets go.

## The window closed during a run

Closing the window, or the terminal it runs in, ends wsl-shrinker, but not what WSL is doing: a compaction that had
started finishes on its own. What wsl-shrinker would have done after it waits in its journal: the next time it starts,
the screen says "The run of … was cut off" and `R` starts the distribution again with the containers it stopped, as
does `wsl-shrinker recover`. If WSL is still compacting, it says so; try again when it is done. `wsl-shrinker doctor`
reports such a run too, and no other run starts before it is finished. `wsl-shrinker history` has no line for it.

## "another run is not finished"

A shrink refuses to start while another wsl-shrinker is shrinking a disk (wait for it), or while a run that was cut off
waits to be finished (`wsl-shrinker recover`, or `R` on the screen).

## A new window opened

Started from a terminal inside the distribution being shrunk, the run moves to a new window: stopping the
distribution closes the terminal it runs in. Shrinking another distribution, or starting `wsl-shrinker` from
PowerShell, stays in the terminal. `wsl-shrinker.exe` typed in WSL, rather than `wsl-shrinker`, cannot tell which
distribution it runs in, so it moves a shrink of any running distribution to a new window.

## "the new window did not start"

A shrink moved to a new window waits up to 15 seconds for it. When no window opened, nothing was stopped: run the
shrink from PowerShell, where it stays in the terminal.

## Still stuck

[Open an issue](https://github.com/kanggu12/wsl-shrinker/issues). Its bug form asks for the output of
`wsl-shrinker doctor`, `wsl-shrinker list --json`, `wsl-shrinker history --json` for a run that went wrong, and the
versions (`wsl-shrinker version`, `wsl --version`).
