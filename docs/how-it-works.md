# How it works

## Finding the disks

- **Distributions**: every WSL 2 distribution registered for your user, at its `BasePath` and `VhdFileName`
  (`ext4.vhdx` when unset) in `HKCU\Software\Microsoft\Windows\CurrentVersion\Lxss`, so one moved to another drive is
  found there. The VM mode bit of the registration's `Flags` tells WSL 2 from WSL 1, as WSL tells them (`Version` is
  WSL 1's file system version: 2 for a new WSL 1 distribution). WSL 1 distributions have no disk file and are not
  listed.
- **Docker Desktop**: `%LOCALAPPDATA%\Docker\wsl\disk\docker_data.vhdx` (and `Docker\wsl\data\ext4.vhdx` from older
  releases), which WSL does not register, or where Docker Desktop's settings moved it: the `disk` folder under
  `CustomWslDistroDir` in `%APPDATA%\Docker\settings-store.json`.
- **Other files**: the VHDX files listed under `extra_disks` in the [settings](settings.md).

`wsl --list --running` says which distributions run; a shrink asks again at its check, in case that changed. A stopped
distribution is never started to look at it.

## The estimate

A VHDX file is a set of regions (headers, a log, metadata, the block allocation table) and payload blocks of a size the
file sets: 1 MiB on WSL's disks, up to 256 MiB on others. The block table says whether and where the file stores each
block of the virtual disk. ext4's block bitmaps say which
filesystem blocks are in use.

A trim tells the virtual disk which blocks the filesystem freed. A compaction then drops every stored block that holds
nothing in use and moves the others down into the space left. After both, the file is:

- its first MiB (file identifier, headers, region tables), plus its regions, plus
- one block size for every stored block that holds at least one filesystem block in use,

and never shorter than the end of a region placed further into the file (an interrupted compaction can leave the block
table there). What comes back is the difference from the file's size on the drive now.

Measured on disks WSL made with `wsl --import`, then written to and cleaned inside: the estimate matched the size after
`wsl --manage --compact` to the byte.

### Docker Desktop's disk and other files

WSL compacts only the distributions it registers. Docker Desktop's disk and the `extra_disks` files go through Windows'
virtual disk API instead, unattached, so nothing trims them first. That compaction drops the stored blocks holding only
zeros and keeps all others, data or not. Measured on a file the tests write: the blocks of zeros went, the blocks with
data stayed byte for byte. Their estimate keeps every block the filesystem uses and reads the free ones to find blocks
of zeros, up to 4 GiB per disk; past that, a block counts as kept. So it errs towards giving back more than it says,
not less.

### Reading a disk WSL holds

While WSL has a disk attached, its header names an active log. Metadata changes (block table, metadata) go to the log
first and to their place later, so the block table on the drive can lack them. wsl-shrinker replays the log in memory
as Windows does on open (MS-VHDX 2.3). It finds the newest unbroken sequence of entries for the current log, checks
every entry's CRC-32C checksum and sequence numbers, and lays their writes over what it reads. It opens the file
read-only, next to the process that holds it.

A filesystem in use also keeps recent changes in its journal, so its bitmaps on disk can miss the last few seconds of
writes. The screen marks such a disk "read while running".

### What can stop the read

The reader refuses what it does not fully understand: a header or region table whose checksums fail, an unknown region
or metadata item marked required, a differencing disk. Such a disk is listed with the reason and no estimate. A
filesystem it cannot read (not ext4, or ext4 with the `meta_bg` layout) does not stop the read. The estimate then keeps
every stored block, so it can only come out low, and the screen says so.

## A shrink

| Step | Reads | Changes |
|:---|:---|:---|
| check | WSL's version, the file's attributes, whether the disk is fixed-size, whether something holds it, whether the distribution runs now, recorded filesystem errors | nothing |
| clean | | removes the items you chose, with the one command each was listed with |
| stop | the running containers | stops them (`docker stop`, each with its own timeout; not when `docker` there is Docker Desktop's, whose containers run in its own distribution), runs `/etc/wsl-shrinker/pre-stop.d/*`, syncs; with wsl-spawner installed, runs the scripts, then `wsl-spawner stop` |
| compact | | `wsl --manage <distro> --compact`: WSL stops the distribution, trims it and compacts the file; other files go through Windows' virtual disk API |
| verify | the disk file again | nothing |
| start again | the root's mount options | starts the distribution and the containers that were running |

**Clean** builds every command itself, from a fixed list in `internal/engine/survey`. To empty a known folder under a
home directory (a tool's cache, or the trash), it runs `find . -xdev -mindepth 1 -delete` inside the folder, after
checking that it is still that folder on the root filesystem; the folder itself stays. The other commands are the
package manager's own (`apt-get clean`, `dnf clean all`, `yes | pacman -Scc`, `zypper --non-interactive clean --all`,
each offered only where its tool is), `journalctl --vacuum-size=100M`, `docker builder prune --all --force`,
`docker image prune --force` (unnamed images only), and `docker image rm` of every tag of an image no container uses.
The look inside and these commands need only `sh` and the core utilities (`df`, `du`, `cut`, `stat`, `readlink`), so a
distribution made from a small image, BusyBox's included, works too. Without `find`, no cache under a home directory is
listed, since `find` is what empties it. A test fails if any command
there could remove a volume, a container or anything else. Ids given on the command line are looked up in a fresh look
inside, so only commands built this way run.

**Stop** goes through [wsl-spawner](https://github.com/kanggu12/wsl-spawner) when it is installed (on the PATH, in
`%LOCALAPPDATA%\Programs\wsl-spawner`, or an administrator's install in `%ProgramFiles%\wsl-spawner`). Your pre-stop
scripts run first, then `wsl-spawner stop <distro> --yes --json --events`: its own hooks and units, then Docker and
Podman, each container with its own timeout, within its `stop_limit`. WSL then ends the distribution, and wsl-spawner
records the stop, so its next start counts the one before as stopped in time, not cut off. The screen shows each of its
steps as it begins. A step that ran out of time or failed marks the stop step for a look (`wsl-spawner history` has the
steps); the run goes on, as the distribution is down. When wsl-spawner stops nothing (its settings leave the
distribution to WSL, or it does not start), wsl-shrinker stops the containers itself and says why. When another
wsl-spawner stop of the distribution is running, say the sign-out's, the run stops there and leaves the distribution to
that stop. When its stop did not run to its end, or WSL did not end the distribution after it, a distribution that is
down goes on to the compaction, the step marked for a look with what wsl-spawner said. One that still runs, or whose
state WSL does not say, fails the step: the run ends it, so that it starts afresh, with Docker, its units and
wsl-spawner's own start, then starts it again with its containers.

**Verify** reads the file once Windows lets go of it. It compares the file with the disk as read right before the
compaction, once the stop settled it, so a file deleted after the disks were listed is not taken for data gone. The
file must be a valid VHDX (with no log left to replay, for Docker Desktop's disk and other files). Its filesystem must
read without new recorded errors. The blocks in use may drop by what was cleaned and a little more (the journal
settling, 0.5 % plus 32 MiB), never by more. On a distribution's disk, every block that held data and is still in use
must still be stored. Other files skip that check: Windows' compaction also drops blocks of zeros the filesystem uses,
which read back as the zeros they were. Errors recorded before the run stop it at the check, for e2fsck to see first.

**Start again** checks the root is mounted read-write. WSL can refuse a start for a moment right after a compaction, so
the start is tried three times, seconds apart. If the root is read-only, the distribution is stopped and started once
more. If it stays read-only, the run says `wsl --shutdown` clears it, and leaves that to you, as it stops every
distribution. Containers that were running before the stop come back one at a time, by id, with `docker start`. One that
runs already, started with the distribution by wsl-spawner or by its restart policy, is left as it is and counted as
running again. One that waits on another (its network, a link) that had not started yet gets a second try a few seconds
later. One that still does not start is counted; one that was already stopped stays stopped. Docker may still be
starting with the distribution (systemd, or a `[boot]` command in `/etc/wsl.conf`): the run waits up to a minute for it.

WSL stops a distribution once no terminal or other program from Windows runs in it, after `instanceIdleTimeout` in
`.wslconfig` (15 seconds by default; `-1` turns it off). Everything inside stops with it: Docker, its containers,
systemd's services. The compaction closed the terminals that kept the distribution running. Unless something else from
Windows runs in it, it stops again a little after the run, and the run says so under its result. A failed stop step
never stopped the distribution: its terminals still run, and nothing is said. A terminal open in it keeps the
containers running. Those with a restart policy (`unless-stopped`, `always`) start again with Docker when the
distribution next starts.

After a failed stop, a failed compaction, or a disk that could not be opened to verify it, the distribution and the
containers the run stopped are started again before it reports. Only a disk that does not read as before after the
compaction is left stopped, and the run says so (see [Troubleshooting](troubleshooting.md)). A failed check changes
nothing and is not recorded in the history. All this is decided in one place at the end of a run, also when the
program's own code fails (a panic). The result says how the distribution was left (`left` in the JSON: `running`,
`stopped`, `file`, `suspect`, `down`, or `other-stop` when another wsl-spawner stop ends it), and the screen, the
command line and the history repeat it.

What can be shrunk is one rule too, which the check applies and the screen, `doctor` and `shrink` show before a run: a
protected disk, a file that could not be read, an encrypted file, recorded filesystem errors, a fixed-size disk, a file
a program holds or a running distribution Docker Desktop, Rancher Desktop or Podman keeps, a WSL without `--compact`. The run itself enforces protection, whatever asked for it.

### A run cut off

A run keeps a journal, `run.json` next to the history: the disk, the step it reached, and the containers it stopped. It
goes when the run ends, however it went. A run cut off (the window closed, a crash, the power) leaves it behind, and
the next start of wsl-shrinker says so. `R` on the screen, or `wsl-shrinker recover`, then starts the distribution
again, writable, with those containers. A compaction WSL is still running is left to finish first. Until then no other
run starts. If the distribution does not start, the journal stays for another try; forgetting the run (`F`, or
`wsl-shrinker recover --forget`) removes it and starts nothing. The journal also keeps two runs, in two windows, from
going at once.

## From inside WSL

`wsl-shrinker` in WSL is the Linux build of the same program. Its screen (`wsl-shrinker` in TUI mode, or
`wsl-shrinker tui`) runs in the Linux terminal and asks `wsl-shrinker.exe` for every answer through WSL's pipes:
`list --json`, `inside --json`, `history --json`, and the hidden `backend` command, which streams a run's steps as JSON
lines. The disks are Windows' to read and to compact; keys, resizing and colors stay the terminal's own, in tmux or
zellij too. This works only with the `wsl-shrinker.exe` of its own build. With another, or with
`WSL_SHRINKER_SCREEN=windows`, the screen runs in `wsl-shrinker.exe` as a Windows program, which WSL gives a console of
its own. Setup's screen runs in the Linux terminal the same way and saves through `wsl-shrinker.exe setup` with the
choices as flags; on a first start it comes first, and what it chose decides what comes next. Every other command line
goes to `wsl-shrinker.exe`, with the distribution's name (`WSLENV`). From a terminal, `WSL_SHRINKER_TTY` (which of
stdin, stdout and stderr are the terminal) and the terminal's width go along too: `wsl-shrinker.exe` may see only pipes
there, and so asks `shrink`'s question, colors its lines and prints CLI mode's table as it would in a console.

Before starting `wsl-shrinker.exe`, the launcher reads the program's header. Windows would stop a build for another
processor (an ARM64 one on an x64 PC) with a dialog on the desktop, far from the terminal; the launcher says so in the
terminal instead, with the command that installs the right build. An x64 build on an ARM64 PC runs on Windows 11 on
ARM, which emulates x64, but not on Windows 10 on ARM, which emulates only 32-bit x86. There the launcher first asks
`cmd.exe`, Windows' own program for the PC's processor, which build of Windows it is.

Shrinking the distribution the terminal runs in stops it, and the terminal with it. So the run continues in a new
window (`start`, which opens the default terminal), already confirmed, and the screen that asked waits until that
window says it started. Shrinking any other distribution stays in the terminal. `wsl-shrinker.exe` run straight from
WSL does not know which distribution it is in, so a shrink of any running distribution continues in a new window.
