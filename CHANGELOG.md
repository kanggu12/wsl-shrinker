# Changelog

All notable changes are listed here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and
versions follow [semantic versioning](https://semver.org/): a release that adds something raises the minor number, one
that only fixes raises the patch number. Each release's section here is its release notes.

## [Unreleased]

## [0.5.1] - 2026-11-10

### Fixed

- A journal update another program held off is written with the next step, so a run cut off still knows what it
  stopped. A run is no longer taken for one going on when Windows has given its process id to another process since,
  as after a restart: it reads as cut off, and `recover` finishes it.
- A disk from `extra_disks` keeps its name, and with it its protection, when Docker Desktop leaves a second data disk
  behind: that one is listed as `Docker Desktop data 2`, never under a file's name. `doctor` names a protected name no
  disk has.
- The verify step allows for what the clean step removed, not for items it skipped. A distribution that stops by itself
  during the clean step is reported as stopped, not running.
- A settings file with a wrong value is put right with the setup flag the error names, like
  `wsl-shrinker setup --language auto`; it used to fail on the same file.
- `help NAME` for a name that is no command says so, with the command meant, and exits with 2. Help shows what each
  flag takes (`--min-gain GB`, `--lang auto|en|ko`), and `version` and `config` no longer list `--lang`.
- The installer says why it failed in one red line in a PowerShell window; a script that runs it still gets the error,
  and exit code 1.
- The screen says a look inside failed where there is no room for its list, with `r` to try again, and wraps a message
  too long for the status row onto the row above rather than cut what to do. In a small window a failed step's reason
  is cut before what the run left, and the note about WSL stopping the distribution fits. The question's box has clear
  cells around it, setup's last page breaks the settings path only between folders, and the history names its columns
  and keeps its times whole. Setup's size page marks the disks it counts and its protect page shows Docker Desktop's
  disk as in use, as the screen does. `c` is "clean inside" in English, so `h history` fits 80 columns.

## [0.5.0] - 2026-11-10

The numbers 0.3 and 0.4 were used before the version numbers started over at 0.1.0, and stay retracted: this release
takes the next minor number free.

### Added

- The screen's header shows wsl-shrinker's mark and the version, and help and the loading screen open with the logo,
  the version and what the tool does.
- From a WSL terminal, tmux and zellij included, setup draws in that terminal too, and saves what changed through
  `wsl-shrinker.exe setup` with the choices as flags. `wsl-shrinker.exe`, which may see only pipes there, is told which
  of its ends a person is at: `shrink` asks its question, the lines take their colors and marks, and CLI mode prints its
  table rather than the bare list. Before, a first start there could stop with "setup needs a terminal".
- `list --json` says for each disk whether it is `protected`, and in `blocked` what stands in the way of a shrink now.
  The CLI table says `protected`, `unreadable`, `encrypted` or `fs errors` in its state column, as the screen does.
- A mistyped command names the one meant (`did you mean: wsl-shrinker list`); other mistakes in a command line point at
  the command's `--help`.

### Fixed

- Docker Desktop's own distributions are not shrunk while they run: Docker Desktop holds them as it holds its data disk.
- A distribution's root read as writable when WSL printed a warning line before the answer.
- `recover` no longer keeps a run's lock forever when its distribution was unregistered, and waits while WSL still
  compacts the disk.
- A VHDX whose block table points past the end of the file is refused instead of read.
- `shrink --json` from inside the distribution it would stop refuses before the question, not after it.
- A run that failed before the disk was measured again shows no expected size under it, which read as if the disk had
  been shrunk.
- `inside --json` names the distribution as WSL does, whatever case it was typed in.
- Choosing what to remove after a look inside failed says it failed, not that there is nothing to remove. The keys
  under the disks leave out `s` while a run is cut off or going on, the question to shrink opens without an older note
  above it, `wsl -d NAME` is never split across lines, and the history shows no total before the first run.
- `doctor`'s failed checks read as failures ("WSL does not answer wsl --version"), and a protected disk's note says how
  to shrink it. CLI mode's protected disks say the command to use rather than "protected" twice, and its shrink example
  names every disk worth shrinking. A description that does not fit is left out rather than cut.
- `--lang` and `--theme` say they take `auto` too. `config init` on an existing file says what to do instead.
- The installer says why a download failed, what a checksum mismatch means, and that nothing was installed when an
  uninstall finds nothing.
- Korean wording: shorter notes for a disk in use or with filesystem errors, "줄이기 전" and "줄인 뒤" for the bars, and
  clearer words for a running disk's reading, the compaction, `recover`, setup's last page and the help's colors.

## [0.2.2] - 2026-11-09

### Changed

- The README and every guide say the same in fewer words: shorter sentences and table cells, nothing left out.
- The `v0.4.1` tag is gone from the repository. It only carried `go.mod`'s retraction of v0.3.0 and v0.4.0 to the Go
  module proxy, which keeps it: `go install` still takes the newest release, and the three stay retracted.

## [0.2.1] - 2026-11-08

### Fixed

- The note under a run that started containers again comes only when the run started the distribution itself again: a
  stop step that failed never stopped it, and its terminals kept it running. `recover` gives the same note on a line of
  its own, and the JSON says `distro_started`. The note is shorter too: with no terminal open in the distribution, WSL
  stops it in about 15 seconds, and its containers with it.
- In a window too short for everything, a finished run keeps the sentence that sums it up and drops the before and
  after bars first. The header counts what a run gave back as soon as it ends.
- On an East Asian code page, where the marks are ASCII, a box's lines and corners were left as they were and drawn two
  cells wide; they are now `-`, `|` and `+`. A step done is `+` (it was `v`, which read as the arrow down), a step
  waiting is `.`, apart from `-` for one skipped, and an arrow in a sentence is `->`.
- A protected disk says so in its state column, on the screen and in the CLI table, not by its color alone.
- Help wraps what a key does instead of cutting it. Setup's last page shows where the settings go only when the whole
  path fits. The demo opens setup as a first start, with no disk protected yet, as a first start has.
- Wording: the disk first in the recent runs ("Debian freed 20.0 GB"), "item 1 of 11" for a single item, the help's
  keys for choosing what to remove, and in Korean the compaction's description, the protected disk's message, the end
  of a run of several disks and setup's last step.

### Changed

- CI checks that the setting keeping its throwaway distribution running holds, and a weekly canary runs the real shrink
  on WSL's newest pre-release.

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

[Unreleased]: https://github.com/kanggu12/wsl-shrinker/compare/v0.5.1...HEAD
[0.5.1]: https://github.com/kanggu12/wsl-shrinker/releases/tag/v0.5.1
[0.5.0]: https://github.com/kanggu12/wsl-shrinker/releases/tag/v0.5.0
[0.2.2]: https://github.com/kanggu12/wsl-shrinker/releases/tag/v0.2.2
[0.2.1]: https://github.com/kanggu12/wsl-shrinker/releases/tag/v0.2.1
[0.2.0]: https://github.com/kanggu12/wsl-shrinker/releases/tag/v0.2.0
[0.1.0]: https://github.com/kanggu12/wsl-shrinker/releases/tag/v0.1.0
