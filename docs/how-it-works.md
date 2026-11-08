# How it works

## Finding the disks

- **Distributions**: every WSL 2 distribution registered for your user, from
  `HKCU\Software\Microsoft\Windows\CurrentVersion\Lxss`: its `BasePath` and `VhdFileName` (`ext4.vhdx` when unset), so
  a distribution moved to another drive is found where it is. WSL 2 is told from WSL 1 as WSL tells them, by the VM
  mode bit of the registration's `Flags` (its `Version` is the version of WSL 1's file system, 2 for a WSL 1
  distribution made today). WSL 1 distributions have no disk file and are not listed.
- **Docker Desktop**: `%LOCALAPPDATA%\Docker\wsl\disk\docker_data.vhdx` (and `Docker\wsl\data\ext4.vhdx` from older
  releases), which WSL does not register, or where Docker Desktop's settings moved it: `CustomWslDistroDir` in
  `%APPDATA%\Docker\settings-store.json`, with the disk under its `disk` folder.
- **Other files**: the VHDX files listed under `extra_disks` in the [settings](settings.md).

Which distributions run comes from `wsl --list --running`; a shrink asks again at its check, as the list may be old by
then. A stopped distribution is never started to look at it.

## The estimate

A VHDX file is a set of regions (headers, a log, metadata, the block allocation table) and payload blocks of 1 MiB. The
block table says, for every block of the virtual disk, whether the file stores it and where. ext4's block bitmaps say
which filesystem blocks are in use.

A trim tells the virtual disk which blocks the filesystem freed, and a compaction then drops every stored block that
holds nothing in use and moves the others down into the space left. So the file after both is:

- its first MiB (file identifier, headers, region tables), plus its regions, plus
- one block size for every stored block that holds at least one filesystem block in use,

and never shorter than the end of a region placed further into the file (an interrupted compaction can leave the block
table there). The difference from the file's size on the drive now is what comes back.

Measured on disks WSL made with `wsl --import`, then written to and cleaned inside: the estimate matched the size after
`wsl --manage --compact` to the byte.

### Docker Desktop's disk and other files

WSL compacts only the distributions it registers. Docker Desktop's disk and the files under `extra_disks` go through
Windows' virtual disk API instead, on a file nobody has attached, so nothing trims them first: the compaction drops the
stored blocks that hold only zeros and keeps every other one, data or not (measured on a file the tests write: the
blocks of zeros went, the blocks with data stayed byte for byte). Their estimate keeps every block the filesystem uses,
and reads the free ones to find the blocks of zeros, up to 4 GiB per disk; past that, a block counts as kept. So it
errs towards giving back more than it says, not less.

### Reading a disk WSL holds

While WSL has a disk attached, its header names an active log: metadata changes (block table, metadata) are written to
the log first and to their place later. Reading the block table as it is on the drive would miss them. wsl-shrinker
replays the log in memory the way Windows does on open (MS-VHDX 2.3): it finds the newest unbroken sequence of entries
for the current log, checks every entry's CRC-32C checksum and sequence numbers, and lays their writes over what it
reads. The file is opened for reading only, next to the process that holds it.

A filesystem in use also keeps recent changes in its journal; its bitmaps on disk can lag behind by the last few
seconds of writes. The screen marks such a disk "read while running".

### What can stop the read

The reader refuses what it does not fully understand rather than guess: a header or region table whose checksums
fail, an unknown region or metadata item marked required, a differencing disk. The disk is then listed with the reason
and no estimate. A filesystem it cannot read (not ext4, or ext4 with the `meta_bg` layout) does not stop the read: the
estimate then keeps every stored block, so it can only come out low, and the screen says so.

## A shrink

| Step | Reads | Changes |
|:---|:---|:---|
| check | WSL's version, the file's attributes, whether something holds it, whether the distribution runs now, errors the filesystem recorded | nothing |
| clean | | removes the items you chose, with the one command each was listed with |
| stop | the running containers | stops them (`docker stop`, each with its own timeout; not when `docker` there is Docker Desktop's, whose containers run in its own distribution), runs `/etc/wsl-shrinker/pre-stop.d/*`, syncs |
| compact | | `wsl --manage <distro> --compact`: WSL stops the distribution, trims it, and compacts the file; any other file goes through Windows' virtual disk API |
| verify | the disk file again | nothing |
| start again | the root's mount options | starts the distribution, and the containers that were running |

**Clean** builds every command itself from a fixed list in `internal/engine/survey`: emptying a known folder under a
home directory (a tool's cache, or the trash) with `find . -xdev -mindepth 1 -delete`, run from inside the folder once
it has checked that it is still that folder on the root filesystem, and leaving the folder itself; `apt-get clean`;
`journalctl --vacuum-size=100M`; `docker builder prune --all --force`; `docker image prune --force` (unnamed images
only); and `docker image rm` of every tag of an image no container uses. A test fails if a command there could remove
a volume, a container or anything else. Ids passed on the command line are
looked up in a fresh look inside, so only commands built this way run.

**Verify** reads the file once Windows has let go of it, and compares it with the disk as read right before the
compaction, once the stop has settled it, so a file deleted after the disks were listed is not taken for data gone. It
must be a valid VHDX (for Docker Desktop's disk and other files, with no log left to replay), its filesystem must read
without new recorded errors, and the blocks in use may have dropped by what was cleaned and a little more (the journal
settling, 0.5 % plus 32 MiB), never by more.
For a distribution's disk, every block that held data and that the filesystem still uses must still be stored. That
last comparison is left out for the other files: Windows' compaction drops blocks of zeros the filesystem uses too, and
they read back as the zeros they were. A filesystem that recorded errors before the run stops it at the check, for
e2fsck to see first.

**Start again** checks the root is mounted read-write. When it is not, the distribution is stopped and started once
more; if it stays read-only, the run says `wsl --shutdown` clears it, and leaves that to you, since it stops every
distribution. Containers come back with `docker start`, one at a time, on the ids that were running before the stop;
one that does not start is counted, and a container that was already stopped stays stopped. Docker may still be
starting with the distribution (systemd, or a `[boot]` command in `/etc/wsl.conf`): the run waits for it, up to a minute.

How long they run then is WSL's to say. WSL stops a distribution once no terminal or other program from Windows runs
in it, after `instanceIdleTimeout` in `.wslconfig` (15 seconds by default; `-1` turns it off), and everything inside
stops with it: Docker, its containers, systemd's services. The compaction closed the terminals that kept the
distribution running, so unless something else from Windows runs in it, it stops again a little after the run, and
the run says so under its result. A stop step that failed never stopped the distribution: its terminals still run, and
nothing is said. A terminal open in it keeps the containers running; those with a restart policy
(`unless-stopped`, `always`) start again with Docker the next time the distribution starts.

After a failed stop, a failed compaction, or a disk that could not be opened to verify it, the distribution and the
containers that were stopped are started again before the run reports. Only a disk that does not read as before after
the compaction is left stopped, and the run says so (see [Troubleshooting](troubleshooting.md)). A failed check changes
nothing and is not recorded in the history. All of this is decided in one place at the end of a run, also when the
program's own code fails (a panic): the result says how the distribution was left (`left` in the JSON: `running`,
`stopped`, `file`, `suspect` or `down`), and the screen, the command line and the history say what it says.

What can be shrunk is one rule too, which the check applies and the screen, `doctor` and `shrink` show before a run:
a protected disk, a file that could not be read, an encrypted file, recorded filesystem errors, a file a program
holds, a WSL without `--compact`. Protection is enforced by the run itself, whatever asked for it.

### A run cut off

A run keeps a journal, `run.json` next to the history: the disk, the step it reached, and the containers it stopped.
It goes when the run ends, however it went. A run cut off on the way (the window closed, a crash, the power) leaves it
behind, and the next start of wsl-shrinker says so: `R` on the screen, or `wsl-shrinker recover`, starts the
distribution again, writable, with those containers. A compaction WSL is still running is left to end first. Until
then no other run starts, and the journal also keeps two runs, in two windows, from going at once.

## From inside WSL

`wsl-shrinker` in WSL is the Linux build of the same program. The screen (`wsl-shrinker` in TUI mode, or
`wsl-shrinker tui`) runs in it, in the Linux terminal, and asks
`wsl-shrinker.exe` for every answer through WSL's pipes (`list --json`, `inside --json`, `history --json`, and the
hidden `backend` command, which streams a run's steps as JSON lines): the disks are Windows' to read and to compact,
while keys, resizing and colors stay the terminal's own, in tmux or zellij too. It does so only with the
`wsl-shrinker.exe` of its own build; with another, or with `WSL_SHRINKER_SCREEN=windows`, the screen runs in
`wsl-shrinker.exe` as a Windows program, which WSL gives a console of its own. Every other command line is handed to
`wsl-shrinker.exe`, with the name of the distribution (`WSLENV`). On a first start setup runs in `wsl-shrinker.exe`, in
the same terminal, and what it chose decides what comes next; in CLI mode `wsl-shrinker.exe` prints the disks.

Before it starts `wsl-shrinker.exe` at all, the launcher reads the program's header. A build for another processor,
an ARM64 one on an x64 PC, would be stopped by Windows with a dialog on the desktop, far from the terminal; the
launcher says so in the terminal instead, with the command that installs the right build. An x64 build on an ARM64 PC
runs on Windows 11 on ARM, which emulates x64, and not on Windows 10 on ARM, which emulates only 32-bit x86: there the
launcher first asks `cmd.exe`, Windows' own program for the PC's processor, which build of Windows it is.

Shrinking the distribution the terminal runs in stops it, and the terminal with it, so the run continues in a new
window (`start`, which opens the default terminal), already confirmed, and the screen that asked waits until that
window says it started. Shrinking any other distribution stays in the terminal. `wsl-shrinker.exe` run straight from
WSL does not know which distribution it is in, so a shrink of any running distribution continues in a new window.
