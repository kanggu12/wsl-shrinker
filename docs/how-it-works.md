# How it works

## Finding the disks

- **Distributions**: every WSL 2 distribution registered for your user, from
  `HKCU\Software\Microsoft\Windows\CurrentVersion\Lxss`: its `BasePath` and `VhdFileName` (`ext4.vhdx` when unset), so
  a distribution moved to another drive is found where it is. WSL 1 distributions have no disk file and are not listed.
- **Docker Desktop**: `%LOCALAPPDATA%\Docker\wsl\disk\docker_data.vhdx` (and `Docker\wsl\data\ext4.vhdx` from older
  releases), which WSL does not register.
- **Other files**: the VHDX files listed under `extra_disks` in the [settings](settings.md).

Which distributions run comes from `wsl --list --running`. A stopped distribution is never started to look at it.

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
`wsl --manage --compact` to the byte, and so did the estimate for a file compacted through Windows' virtual disk API.

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
fail, an unknown region or metadata item marked required, a differencing disk, an ext4 filesystem using the `meta_bg`
layout. The disk is then listed with the reason and no estimate.

## A shrink

| Step | Reads | Changes |
|---|---|---|
| check | WSL's version, the file's attributes, whether something holds it | nothing |
| clean | | removes the items you chose, with the one command each was listed with |
| stop | the running containers | stops them (`docker stop`, each with its own timeout), runs `/etc/wsl-shrinker/pre-stop.d/*`, syncs |
| compact | | `wsl --manage <distro> --compact`: WSL stops the distribution, trims it, and compacts the file |
| verify | the disk file again | nothing |
| start again | the root's mount options | starts the distribution, and the containers that were running |

**Clean** builds every command itself from a fixed list in `internal/engine/survey`: `rm -rf` of a known cache folder
under a home directory, `apt-get clean`, `journalctl --vacuum-size=100M`, `docker builder prune --all --force`,
`docker image prune --force` (unnamed images only) and `docker image rm <id>` for an image no container uses. A test
fails if a command there could remove a volume, a container or anything else. Ids passed on the command line are
looked up in a fresh look inside, so only commands built this way run.

**Verify** reads the file once Windows has let go of it: it must be a valid VHDX with no log left to replay, its
filesystem must read without recorded errors, and the blocks in use may have dropped by what was cleaned and a little
more (the journal settling, 0.5 % plus 32 MiB), never by more. A larger drop fails the run and says so.

**Start again** checks the root is mounted read-write. When it is not, the distribution is stopped and started once
more; if it stays read-only, the run says `wsl --shutdown` clears it, and leaves that to you, since it stops every
distribution. Containers come back with `docker start` on the ids that were running before the stop; a container that
was already stopped stays stopped.

A failed compaction starts the distribution again before reporting. A failed check changes nothing and is not recorded
in the history.

## From inside WSL

`wsl-shrinker` in WSL is a small Linux program that hands the command line to `wsl-shrinker.exe` and passes the name
of its distribution along (`WSLENV`). Shrinking that distribution stops it, and the terminal with it, so the run
continues in a new window (`start`, which opens the default terminal), already confirmed. Shrinking any other
distribution stays in the terminal.
