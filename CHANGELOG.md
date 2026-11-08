# Changelog

All notable changes are listed here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and
versions follow [semantic versioning](https://semver.org/): a release that adds something raises the minor number, one
that only fixes raises the patch number. Each release's section here is its release notes.

## [Unreleased]

### Changed

- Setup is a wizard: a page for each choice, with the steps on a rail beside it and a bar of how far along, and a last
  page to check them before they are written. Each page shows what its choice does as it is made: the language and
  colors on the wizard itself, the mode as a small picture of what it opens, and the size worth shrinking as the
  person's own disks colored by it. The first page says what was found and what can be given back now. On a small
  terminal the pictures give way before the choices do.

## [0.3.0] - 2026-11-08

The first public release. 0.1.0 and 0.2.0 below were published for a short time and withdrawn; what changed since
them follows.

### Added

- Two modes, TUI and CLI. Started bare in a terminal, TUI opens the screen, and CLI prints the disks, in the terminal's
  own colors with the screen's meanings, what is worth shrinking, and the commands to go on with. Every command works
  in both, and `wsl-shrinker tui` opens the screen in either.
- `wsl-shrinker setup` chooses the language, colors, mode, size worth shrinking and protected disks, on a screen or as
  flags, and writes only the keys that changed, leaving the rest of the settings file as written. The first start in
  a terminal opens it, and the installer opens it in a console on a first install.
- In a terminal the commands mark how things went in the terminal's own colors, with the screen's meanings, and a
  mistake says what went wrong with what to do under it. Piped, they print plain text, as before.
- A crash says `stopped on a bug`, with where to report it and the lines a report needs, in place of a bare Go panic,
  wherever it happens: in a command, on the screen, or during a run. An error Go cannot recover from, such as a
  deadlock, is also kept in a file, which a later run in a terminal points at once.
- The launcher in WSL reads `wsl-shrinker.exe`'s header before starting it: a build for another processor is refused
  in the terminal, with the command that installs the right one, where Windows would have shown a dialog on the
  desktop. On an ARM64 PC the launcher asks Windows for its build: an x64 build is refused on Windows 10 on ARM, which
  cannot emulate x64.
- A run keeps a journal of how far it got. One cut off on the way, by a closed window or a crash, can be finished the
  next time: the screen says so and `R` starts the distribution and its containers again, as does the new
  `wsl-shrinker recover`, and `doctor` reports it. The journal also keeps two runs, in two windows, from going at once.
- The history keeps each run's steps, the build that ran it, and how it left the distribution, for a bug report.
- A run moved to a new window waits for that window to say it started; the terminal it came from says so when it did
  not.
- Inside WSL, the screen runs in the Linux terminal itself and asks `wsl-shrinker.exe` for what it shows, so keys,
  resizing and colors are the terminal's own, in tmux or zellij too. `WSL_SHRINKER_SCREEN=windows` keeps the screen
  in `wsl-shrinker.exe`, as does a `wsl-shrinker.exe` of another build.
- `shrink` exits with 3 when a run failed and left a distribution not running, apart from a plain failure's 1.

### Changed

- A mistake's hint, piped, follows it in brackets: `wsl-shrinker: no disk named "x" (see wsl-shrinker list)`.
- A look of its own: the disk as blocks. The logo is a block map whose free blocks lift away, and the screen draws dots
  and bars with the same small square, which keeps one cell in every terminal, East Asian ones included. Teal is what
  goes back to Windows, slate the data that stays, indigo what runs, pink what is protected, amber a warning.
- A warning in a run shows in amber, apart from a failure in red.
- The header leaves out its last pieces one at a time in a narrow window, rather than all of them, and keeps the total
  longest.
- The estimate for Docker Desktop's disk and other files WSL does not register counts only blocks of zeros as going
  back: Windows compacts such a file without a trim, and keeps the blocks that still hold deleted data. It used to count
  what a trim would free as well.
- A disk whose filesystem records errors is not shrunk until e2fsck has checked it; the screen and `doctor` say so.
- After a failed stop, or a verify that could not open the file, the distribution and its containers are started again.
  A disk that does not read as before after compacting stays stopped, and the run says so.
- `shrink` with no terminal to ask in needs `--yes`, and stops with code 2 without it. The summary and the question go
  to stderr, so stdout holds only the steps or the JSON, and Ctrl+C at the question cancels.
- `--json`: every command prints one object that starts with `"schema": 1`; every key is in snake_case, a size's key
  says it counts bytes (`after_bytes`, `freed_bytes`), and durations are `took_seconds` everywhere. `inside --json`
  gives the whole look inside, the root filesystem's numbers and Docker with the items. A history written before keeps
  its numbers.
- What can be shrunk is one rule, which the run itself applies: protection holds whatever asked for the run. `shrink`
  refuses a disk that cannot be shrunk now (encrypted, in use, with filesystem errors) before its question, not only a
  protected one, and after a failure says how it left the disk.
- A distribution name that WSL does not have stops `inside` and `shrink` with code 2 before anything runs inside a
  distribution; names are matched without regard to case, as WSL does. `config` with an unknown command, or a command
  given words it does not take, exits with code 2 too.
- The help, the history and a long disk list scroll; the help's last steps and its colors are no longer cut off.
- In a narrow window the disk table leaves out its bar, then AFTER, then FILE, and keeps what a shrink frees.
- `extra_disks` takes full Windows paths, and `min_gain_gb` a number up to a million; a file with the same name as
  another disk is listed with its folder in front.
- `list` lines up names and states on the left and sizes on the right, as `inside` and `history` do. `version` says
  `dev` for a build of a commit that is not a release.

### Fixed

- A run cut off while its containers stop has them started again by `recover`: they are written down before they
  stop. `recover` reads the disk of a run cut off once its compaction began, and leaves a disk that does not read
  cleanly stopped.
- A run's check reads the disk afresh: a file a program took since the disks were listed, or errors recorded since,
  stop it there.
- The Hugging Face item empties the downloaded models only; the login token and accelerate's settings stay. An image
  known only by its digest is removed, and a cache folder with something mounted inside it is left whole.
- The look inside a distribution and the cleaning have time limits, and a call to WSL that ran out of time is no
  longer taken for "none running".
- The header counts the disks worth shrinking, as the status line does; a run's size expected and its check count the
  items chosen inside, as its question did.
- The installer reads the machine's architecture from Windows, so Windows PowerShell on ARM64 picks the ARM64 build
  too. The third-party notices include the modules the WSL launcher is built from.
- A failure in the program's own code during a run (a panic) no longer leaves the distribution stopped: it is settled
  like a failed step, and the distribution and its containers start again.
- With no distribution registered, reading the disks no longer fails. On a WSL too old to say its version, the screen
  no longer offers a shrink that the run's check then refuses.
- A good compaction of Docker Desktop's disk no longer fails its verify step: Windows drops the blocks of zeros the
  filesystem uses too, which the step took for data gone.
- A file deleted between reading the disks and the shrink is no longer taken for data the compaction lost: what verify
  compares with is read right before the compaction.
- A distribution started after the disks were read is stopped the way its containers expect and started again, rather
  than stopped by the compaction and left so. When WSL does not say which distributions run, the run stops at its
  check.
- Containers are started again one by one: one that does not start no longer counts the others as failed.
- Items chosen in a distribution that stopped in the meantime say why nothing was removed.
- A disk WSL cannot compact (an old WSL, an encrypted file) is no longer shown as worth shrinking or counted in the
  header's total. A file from the settings that another program holds no longer says to quit Docker Desktop.
- A long disk list scrolls to the selected disk, and the table's header lines up with its columns.
- A run's bars draw the data that stays in slate and what goes back in teal. A run of several names the disk its steps
  belong to, and lists the disks a failure left out.
- The confirm box keeps its keys on a small screen, offers `c` only when there is something to choose, and names the
  distribution it stops when several disks are marked.
- A failed step's error and a failed look inside show whole, and no amount is split from its unit at a line's end.
- A blocked disk's message shows in the color of its reason. The clean list keeps `space` in its footer, shows whole
  its line on what is never touched, and counts its position in items.
- With WSL turned off, the screen said to restart Windows and press `r`; it now says to start wsl-shrinker again.
- `inside` with a name in another case no longer starts a stopped distribution, and an error no longer prints the
  whole script it ran.
- An id with a comma in it is found whole, an empty id is refused, and an id named twice is removed once; a run moved
  to a new window gets every item.
- Sizes from 1000 to 1023 of a unit show in the next one ("1.0 GB", not "1021 MB"), and just under 100 as "100", not
  "100.0".
- A damaged file can no longer hold the reader or mislead it: a log whose entries overlap is refused in bounded time,
  and a log or region outside the file, or a superblock with impossible inode counts, is refused.
- The history skips a damaged line, however long, instead of the lines after it; a history that cannot be read is not
  overwritten, and each write goes to a file of its own, synced to the drive before it replaces the old one.
- `doctor` says when the settings are the defaults, names `wsl --update` for an old WSL, and counts "1 problem".
- Without `wsl-shrinker.exe`, the launcher in WSL still answers `--help` and `version`.
- The installer picks the ARM64 build on ARM64 Windows from an x64 PowerShell, and a running wsl-shrinker no longer
  leaves a half-done install. The zip holds a short README.txt in place of the repository's page, whose pictures it
  does not hold, and its notices link to the license next to them.
- The run's sentence for a compaction, and the help's steps, say what Windows does with Docker Desktop's disk and
  other files: no trim, only blocks of zeros go. A disk whose filesystem cannot be read says so, rather than that it
  holds no ext4.
- The demo's run says how long its steps took, rather than "in 0s".
- A run refused at its check says why in the screen's language, not in the engine's English.
- Cargo's crate cache is called a cache, as the group it is listed in says.
- Korean: one word for what goes back, one for downloads, and smaller wording fixes.

## 0.2.0 - 2026-11-08 (withdrawn)

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

## 0.1.0 - 2026-11-08 (withdrawn)

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

[Unreleased]: https://github.com/kanggu12/wsl-shrinker/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/kanggu12/wsl-shrinker/releases/tag/v0.3.0
