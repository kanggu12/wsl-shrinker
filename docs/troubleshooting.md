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

## "more went than was cleaned"

After compacting, the filesystem uses noticeably less than before, beyond what you chose to clean. wsl-shrinker stops
there and does not start the distribution. Do not use it until you know why: attach the disk to WSL without mounting
it (`wsl --mount --vhd <file> --bare`, as administrator), check it from another distribution with
`sudo e2fsck -fn /dev/sdX` (`lsblk` names it), and open an issue with `wsl-shrinker history --json`.

## The window closed during a run

Closing the window, or the terminal it runs in, ends wsl-shrinker, but not what WSL is doing: a compaction that had
started finishes on its own. What wsl-shrinker would have done after it does not happen: the distribution is not
started again, and its containers stay stopped. `wsl -d <distro>` starts it; containers with a restart policy come back
with Docker. `wsl-shrinker history` has no line for that run.

## A new window opened

Started from a terminal inside the distribution being shrunk, the run moves to a new window: stopping the
distribution closes the terminal it runs in. Shrinking another distribution, or starting `wsl-shrinker` from
PowerShell, stays in the terminal.

## Still stuck

[Open an issue](https://github.com/kanggu12/wsl-shrinker/issues) with the output of `wsl-shrinker doctor`,
`wsl-shrinker list --json` and `wsl --version`.
