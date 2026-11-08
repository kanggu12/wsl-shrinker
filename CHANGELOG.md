# Changelog

All notable changes are listed here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and
versions follow [semantic versioning](https://semver.org/): a release that adds something raises the minor number, one
that only fixes raises the patch number. Each release's section here is its release notes.

## [Unreleased]

## [0.2.0] - 2026-11-08

### Added

- A run that starts containers again says how long they run. WSL stops a distribution once no terminal or other
  program from Windows runs in it, 15 seconds later by default (`instanceIdleTimeout` in `.wslconfig`), and Docker and
  its containers with it; the compaction closes the terminals that kept the distribution running, so after a run it
  stops again unless something else runs in it. The note follows the result on the screen, where it shows whole or
  not at all, and in `wsl-shrinker shrink`, and the JSON counts the containers in `containers_started`.
  Troubleshooting says what brings them back.

### Changed

- CI shrinks the throwaway distribution on a real WSL 2 with Docker in it, two containers running and one stopped:
  the running ones must be stopped and started again, the stopped one left alone. It then compacts copies of that disk
  where Docker Desktop keeps its disk, where Docker's settings move it, and as a file from `extra_disks`, each held
  against its estimate.

## [0.1.0] - 2026-11-08

The first public release.

### Added

- One terminal screen for every WSL 2 disk on the PC: each distribution's, Docker Desktop's data disk (also where its
  settings moved it), and any VHDX file listed in the settings, with the file's size now, its size after a shrink, and
  what that gives back to Windows.
  The numbers come from reading the disk itself, the VHDX block table (its log replayed in memory while WSL holds the
  file) and ext4's block bitmaps; nothing is started, mounted or written to read them.
- What fills a running distribution: package and language caches, model and browser downloads, the trash, the system
  journal, Docker's build cache, unnamed images and images no container uses. Chosen items are removed just before
  the disk is compacted; Docker volumes and containers never are.
- A shrink in six steps the screen follows: check, clean what was chosen, stop the containers with their own timeouts
  and run `/etc/wsl-shrinker/pre-stop.d`, compact with WSL's own `wsl --manage --compact` (Windows' virtual disk API
  for files WSL does not register), read the disk again to check it holds what it held, and start the distribution
  again, writable, with the containers that were running. Several disks go one after another.
- A journal of each run: a run cut off by a closed window or a crash is finished the next time, with `R` on the
  screen or `wsl-shrinker recover`, and two runs never go at once. The history keeps each run's steps.
- TUI and CLI modes. Started bare in a terminal, wsl-shrinker opens the screen, or prints the disks as a table with the
  commands to go on with. A setup wizard chooses the language, the colors, the mode, the size worth shrinking and the
  disks to protect, a page each with a preview of what the choice does, at the first start and from the installer;
  `wsl-shrinker setup` opens it again and takes the same choices as flags.
- Commands for scripts: `list`, `inside`, `shrink`, `recover`, `history`, `doctor` and `config`, with `--json` where it
  applies and exit codes a script can tell apart. English and Korean.
- Inside WSL, `wsl-shrinker` draws the screen in the Linux terminal itself, tmux and zellij included, and asks
  `wsl-shrinker.exe` for the rest; a shrink of the distribution the terminal runs in continues in a new window. A
  `wsl-shrinker.exe` built for another processor is refused there, with the command that installs the right one.
- A crash says `stopped on a bug`, with what a report needs, and an error Go cannot recover from is kept in a file a
  later run points at.
- Windows 10, 11 and Server 2022 and 2025, on x64 and ARM64, with WSL as Windows built it in or as `wsl --update`
  installs it. The Windows console's own window, whose fonts lack `✓`, `✗`, `⏎` and `↻`, gets plain marks, each as
  wide as its mark, and the word Enter; on an East Asian code page, all ASCII. `WSL_SHRINKER_SYMBOLS` chooses for any
  terminal.
- A one-line install for Windows PowerShell 5.1 and PowerShell 7 that checks the download against the release's
  checksums, adds the program to the user's PATH and opens setup; the same script removes it again. Each release
  also has a Scoop manifest and build provenance for its zips, and `docker run --rm -it ghcr.io/kanggu12/wsl-shrinker-demo`
  tries the screen on a made-up PC.

[Unreleased]: https://github.com/kanggu12/wsl-shrinker/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/kanggu12/wsl-shrinker/releases/tag/v0.2.0
[0.1.0]: https://github.com/kanggu12/wsl-shrinker/releases/tag/v0.1.0
