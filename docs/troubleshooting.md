# Troubleshooting

Start with `wsl-shrinker doctor`: it checks WSL, the settings and every disk, and says what to do about each problem.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/trouble-dark.svg">
  <img alt="Where to read first. doctor saying WSL cannot compact leads to this WSL cannot compact; a run of a disk cut off at compact, to The window closed during a run; a filesystem that records errors, to the filesystem records errors. The screen saying Docker Desktop's disk is in use leads to Docker Desktop's disk is in use. shrink saying another run is not finished leads to another run is not finished, and a disk that uses less after compacting than before, beyond what was cleaned, to more went than was cleaned." src="assets/trouble-light.svg">
</picture>

Installing, and running it from inside WSL:

- [The install stops](#the-install-stops)
- [`wsl-shrinker` is not found inside WSL](#wsl-shrinker-is-not-found-inside-wsl)
- ["could not start wsl-shrinker.exe"](#could-not-start-wsl-shrinkerexe)
- ["Windows protected your PC"](#windows-protected-your-pc)
- [Boxes where marks should be](#boxes-where-marks-should-be)

## "WSL does not answer wsl --version"

`doctor` fails this check under the WSL built into older Windows 10, which has no `--version`, or with no WSL at all.
`wsl --update` replaces the built-in one with the current WSL; with no WSL, `wsl --install` in an administrator
PowerShell installs it, after a restart. wsl-shrinker lists and reads the disks either way, and shrinks a distribution
once the current WSL is there.

## "WSL did not answer in time"

WSL starts its service with the first call after Windows starts, which takes seconds, and longer while it updates.
wsl-shrinker gives each call to WSL a time limit, a minute for most, then says so, rather than take the silence for an
old WSL or for no distribution running. Try again (`r` on the screen). If it keeps happening, `wsl --shutdown` restarts WSL, stopping
every distribution.

## "this WSL cannot compact"

Shrinking a distribution uses `wsl --manage <distro> --compact`, in WSL 3.0.1 and later (2.9.8 as a pre-release).
`wsl --update` installs it on Windows 10 version 2004 and later. Docker Desktop's disk and files from `extra_disks`
do not need it.

## A disk gave back less than its estimate

- **It was read while running.** A running distribution's estimate can miss the last few seconds of writes, and the
  stop step writes a little too. A difference of more than a few hundred MB is worth an issue.
- **Something wrote between the estimate and the shrink.** The estimate is taken when the screen reads the disks; `r`
  reads them again.
- **The file is sparse.** A sparse disk (`wsl --manage <distro> --set-sparse true`) already gives space back on its
  own; the screen marks it.

## "a fixed-size disk"

A VHDX made with a fixed size keeps every block in its file from the day it was made, and Windows compacts only disks
that grow: it reports a compaction of one as done and leaves the file as it is. wsl-shrinker marks such a disk `fixed`
and does not shrink it, so its distribution is not stopped for nothing. WSL itself makes disks that grow; a fixed-size
one was made by another tool.

## "the disk file is encrypted"

Windows cannot compact a file encrypted with EFS (error 0x80071772). Turn encryption off in the file's Properties,
Advanced, then shrink it.

## Docker Desktop's disk is "in use"

Docker Desktop holds its data disk while it runs. Quit it from its tray icon, then shrink the disk. Rancher Desktop
holds its distributions the same way: quit it too. A Podman machine holds its distribution while it runs:
`podman machine stop`, then shrink it.

## Containers stopped a little after a shrink

The run (or `recover`) started them again, then WSL stopped their distribution. WSL does that once no terminal or other
program from Windows runs in it, 15 seconds later by default. The compaction had closed the terminals that kept it
running. Open a terminal in it: the containers with a restart policy (`unless-stopped`, `always`) start with Docker;
start the others with `docker start`. To keep a distribution running with nothing open in it, set
`instanceIdleTimeout=-1` under `[general]` in `%UserProfile%\.wslconfig` and run `wsl --shutdown` once.

## The stop step names wsl-spawner

With [wsl-spawner](https://github.com/kanggu12/wsl-spawner) installed, it stops the distribution
([How it works](how-it-works.md#a-shrink)). A `!` on the stop step with "a step ran out of time" or "a step failed"
means one of its steps did: `wsl-spawner history` shows which, with its exit code, and `stop_limit` in its settings
gives the services more time. "wsl-spawner stopped nothing, so wsl-shrinker did" says why after it, often that its
settings leave the distribution to WSL (`wsl-spawner set NAME --skip=false` changes that). "another wsl-spawner stop is
running" means the sign-out's stop, or one asked by hand, is ending the distribution: shrink it once that is done.
Earlier wsl-shrinker releases stopped a distribution themselves, so `wsl-spawner check` took the next start for one
after a cut; that record stays in its history.

## "the root filesystem stays read-only"

After compacting, WSL can leave a distribution's root mounted read-only. wsl-shrinker stops and starts the distribution
once more. If that is not enough, `wsl --shutdown` restarts WSL and clears it. It stops every running distribution, so
wsl-shrinker leaves it to you.

## "the filesystem records errors"

The kernel noted errors in the disk's filesystem before any shrink, and wsl-shrinker leaves the disk alone until e2fsck
has checked it. Stop the distribution (`wsl --terminate <distro>`; for Docker Desktop's disk, quit Docker Desktop).
Attach the disk without mounting it (`wsl --mount --vhd <file> --bare`, as administrator). Run
`sudo e2fsck -f /dev/sdX` from another distribution (`lsblk` names it), then detach the disk with
`wsl --unmount <file>`.

## "more went than was cleaned"

After compacting, the disk does not read as before: the filesystem uses noticeably less, beyond what you chose to
clean; blocks it uses are no longer stored; or it records new errors. wsl-shrinker stops there and leaves the
distribution stopped. Do not use it until you know why. Attach the disk to WSL without mounting it
(`wsl --mount --vhd <file> --bare`, as administrator), check it from another distribution with
`sudo e2fsck -fn /dev/sdX` (`lsblk` names it), and open an issue with `wsl-shrinker history --json`.

If the file only could not be opened again ("cannot be opened after compacting"), nothing says the disk changed. The
distribution is started again, and `r` on the screen rereads the disk once the program that had it lets go.

## The window closed during a run

Closing the window, or the terminal it runs in, ends wsl-shrinker but not what WSL is doing: a compaction that had
started finishes on its own. What wsl-shrinker would have done next waits in its journal. On its next start, the screen
says "The run of … was cut off", and `R` (or `wsl-shrinker recover`) starts the distribution again with the containers
it stopped. If WSL is still compacting, it says so; try again when it is done. `wsl-shrinker doctor` reports such a run
too. `wsl-shrinker history` has no line for it.

If the distribution does not start again, or its disk stays held, the message says why, and the run stays cut off:
`R` tries again, and `F` (or `wsl-shrinker recover --forget`) closes the run as it is, starting nothing. Both say first
what stays stopped and how to start it by hand: `wsl -d NAME`, then `docker start` with the container ids inside it.
The command prints this again after forgetting. A root filesystem
that stays read-only clears with `wsl --shutdown`, which stops every distribution.

## "another run is not finished"

A shrink refuses to start while another wsl-shrinker is shrinking a disk (wait for it), or while a run that was cut off
waits to be finished (`wsl-shrinker recover`, or `R` on the screen; `recover --forget`, or `F`, when it cannot be
finished).

## A new window opened

Started from a terminal inside the distribution being shrunk, the run moves to a new window: stopping the distribution
closes the terminal it runs in. Shrinking another distribution, or starting `wsl-shrinker` from PowerShell, stays in
the terminal. `wsl-shrinker.exe` typed in WSL, rather than `wsl-shrinker`, cannot tell which distribution it runs in,
so it moves a shrink of any running distribution to a new window.

## "the new window did not start"

A shrink moved to a new window waits up to 15 seconds for it. When no window opened, nothing was stopped: run the
shrink from PowerShell, where it stays in the terminal.

## The install stops

- **"could not download"**: the PC cannot reach github.com. Download the zip for your processor, `checksums.txt` and
  `install.ps1` elsewhere, then install from them with the saved script ([Setup](setup.md#without-internet-access)).
- **"Method invocation is supported only on core types in this language mode"**: PowerShell runs in ConstrainedLanguage
  mode, as on company PCs under AppLocker or Windows Defender Application Control, and refuses the script's .NET calls
  from its first line on. Install from the zip by hand ([Setup](setup.md#by-hand)).
- **"running scripts is disabled on this system"**: a saved `install.ps1` was run as a file. The one-line install
  (`irm ... | iex`) runs no file and is not stopped by the execution policy; or run the file with
  `powershell -ExecutionPolicy Bypass -File install.ps1`.
- **"is in use; close wsl-shrinker"**: a wsl-shrinker still runs, in another terminal or a window a run moved to. Close
  it, then run the install again. Nothing was replaced.

## `wsl-shrinker` is not found inside WSL

WSL takes Windows' PATH, which holds the install folder, into each distribution. A distribution with
`appendWindowsPath=false` under `[interop]` in `/etc/wsl.conf` takes none of it: run the launcher by its full path,
`/mnt/c/Users/<you>/AppData/Local/Programs/wsl-shrinker/wsl-shrinker` (with Scoop,
`/mnt/c/Users/<you>/scoop/apps/wsl-shrinker/current/wsl-shrinker`), or link it into your own PATH once:
`ln -s "/mnt/c/Users/<you>/AppData/Local/Programs/wsl-shrinker/wsl-shrinker" ~/.local/bin/`. A terminal opened before
the install has the old PATH: open a new one. In WSL, if it is still not found, run `wsl --shutdown` in Windows first.

## "could not start wsl-shrinker.exe"

With "exec format error", the distribution runs no Windows programs: `enabled=false` under `[interop]` in
`/etc/wsl.conf`. Set it to `true` (or remove the line), run `wsl --shutdown` in Windows, and open the distribution
again. wsl-shrinker needs that: the disks are Windows' to read and to compact.

## "Windows protected your PC"

SmartScreen says this the first time a program downloaded with a browser runs, unless a publisher it knows signed it.
The one-line install and Scoop download without the browser's mark, so it does not appear there. For a zip downloaded
by hand, check it first (`checksums.txt`, or `gh attestation verify <zip> -R kanggu12/wsl-shrinker`). Then choose More
info, then Run anyway, or clear the mark before extracting: `Unblock-File <zip>`.

## Boxes where marks should be

The terminal's font has no `✓`, `✗`, `⏎` or `↻`. wsl-shrinker draws plain marks by itself in the Windows console's own
window, and guesses that window inside WSL. Elsewhere, a terminal with a font of its own can still lack them.
`WSL_SHRINKER_SYMBOLS=plain` draws `+`, `x`, `Enter` and `~` in any terminal. Lines that run past their place, with
arrows and dots that look wide, are a console on a Korean, Japanese or Chinese code page drawing them two cells wide:
`WSL_SHRINKER_SYMBOLS=ascii` draws every mark in ASCII. Windows Terminal has fonts for every mark and draws each one
cell wide ([Setup](setup.md#terminals)).

## Still stuck

[Open an issue](https://github.com/kanggu12/wsl-shrinker/issues). Its bug form asks for the output of
`wsl-shrinker doctor`, `wsl-shrinker list --json`, `wsl-shrinker history --json` for a run that went wrong, and the
versions (`wsl-shrinker version`, `wsl --version`).
