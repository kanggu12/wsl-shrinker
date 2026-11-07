# Changelog

All notable changes are listed here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and
versions follow [semantic versioning](https://semver.org/): a release that adds something raises the minor number, one
that only fixes raises the patch number. Each release's section here is its release notes.

## [Unreleased]

## [0.2.0] - 2026-11-08

### Changed

- The clean list is grouped by what removing costs (caches, downloads, Docker images, logs), each group with its size,
  and says which rows of a long list are on screen. `PgUp`, `PgDn`, `Home` and `End` move through it.
- The details of the selected disk show as many findings as fit, and a short window leaves a section out rather than
  show its heading alone.
- A run's screen says in a sentence what the running step does; a failed run says whether the disk changed and whether
  the distribution runs; a run with a warning says to look at it.
- The confirm box says when nothing was chosen to clean inside, and how to choose.
- Docker Desktop's disk in use shows in blue, with what it gives back once Docker Desktop is closed, and is not counted
  as worth shrinking until then.
- With no disk, or when the disks cannot be listed, only the keys that do something are shown; a long error shows
  whole. Docker's "5 weeks ago" is said in Korean on a Korean screen.

### Fixed

- Spinners turn and passing messages go away on their own, without a key press.
- Moving up in a long clean list no longer jumps.
- In a distribution where docker is Docker Desktop's WSL integration, its images and build cache are no longer offered
  for cleaning (they are on Docker Desktop's own disk), and its containers are no longer stopped.
- `c` in the confirm box opens the clean list, as the box says; the box counts what was chosen in the space that comes
  back, and says how many containers stop only when it knows.
- A run of several disks shows each disk's outcome; a disk compacted before a later step failed is said to be shrunk,
  and is no longer left marked.
- The screen no longer stops on: a reload while a distribution was being looked at, a clean list that became shorter or
  empty, a confirm box whose disk became blocked, or a run started while a run moved to a new window was getting ready.
- Ctrl+C before a disk is stopped ends the run there; once it is stopped, every call to WSL has a deadline, so one that
  never answers cannot hold the run forever.
- An image with several tags is removed with all its tags; Docker refused to remove it by id.
- Emptying a cache checks its folder from inside it, so a link put in its way after the check cannot move the removal.

## [0.1.0] - 2026-11-08

### Added

- A terminal screen listing every WSL 2 disk and Docker Desktop's data disk, with the file's size now, its size after a
  shrink, and what that gives back to Windows. The numbers come from reading the disk itself: the VHDX block table,
  with its log replayed in memory while WSL holds the file, and ext4's block bitmaps. Nothing is started or written to
  read them.
- What fills a running distribution: package and language caches, the system journal, Docker's build cache, unnamed
  images and images no container uses, each with the one command that removes it. Choose any of them to remove before
  the disk is compacted. Docker volumes and containers are never touched; the code that would do it does not exist.
- A shrink in steps the screen follows: check, clean what was chosen, stop the containers with their own timeouts and
  run `/etc/wsl-shrinker/pre-stop.d`, compact with `wsl --manage --compact` (or Windows' virtual disk API for files WSL
  does not register), read the disk again to check the filesystem holds the same data, and start the distribution
  again, writable, with its containers.
- Several disks in one run, a history of past runs, protected disks in the settings, English and Korean.
- Commands for scripts: `list`, `inside`, `shrink`, `history`, `doctor` and `config`, with `--json` where it applies.
- Started from inside WSL, it continues in a new window when it shrinks the distribution its terminal runs in.

[Unreleased]: https://github.com/kanggu12/wsl-shrinker/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/kanggu12/wsl-shrinker/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/kanggu12/wsl-shrinker/releases/tag/v0.1.0
