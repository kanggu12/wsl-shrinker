# wsl-shrinker internals

The code's layout, what runs where, what it reads and changes, why, and how it is tested. For a shrink seen from the
outside, see [how it works](how-it-works.md).

## Layout

The packages sit in layers, and imports go down, never up. `internal/layers_test.go` fails on an upward import and on a
folder in no layer. It reads the source with `go/parser`, so it also runs as a Windows test binary. `internal/version`
sits with core, where every layer may reach it; `internal/testutil` serves tests only.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/layers-dark.svg">
  <img alt="The packages in layers, highest first. cmd holds the program, which runs where it is started or hands over to wsl-shrinker.exe. front holds cli, tui, ui and i18n. app puts the rest together, and remote is the screen's link to wsl-shrinker.exe from inside WSL. engine holds disks, estimate, survey, shrink, journal and history. settings holds config. machine holds wsl, winapi, vhdx and ext4. core holds units and version, and does no I/O. Imports go down the layers, never up." src="assets/layers-light.svg">
</picture>

| Layer | Package | What it holds |
|:---|:---|:---|
| cmd | `cmd/wsl-shrinker` | `main`; inside WSL, the launcher that draws the screen there or hands the command line to `wsl-shrinker.exe` |
| front | `cli` | the commands, CLI mode's overview, the look of a mistake |
| | `tui` | the screen, the setup wizard, the made-up PC of `--demo` |
| | `ui` | the colors, the block, fitting text to the cell |
| | `i18n` | every word, in English and Korean |
| app | `app` | puts the rest together for this PC, once: settings, disks, history, the journal |
| | `app/remote` | the screen's backend in WSL: `wsl-shrinker.exe`, asked through WSL's pipes |
| engine | `disks` | finding every disk and reading its estimate |
| | `estimate` | what a compaction leaves, from the block table and the bitmaps |
| | `survey` | what fills a running distribution, and the one command that removes each item |
| | `shrink` | a run's steps, `Preflight` (whether a disk can be shrunk now) and how a run left its distribution |
| | `journal`, `history` | the run going on (`run.json`), and past runs |
| settings | `config` | `config.toml`, read whole and written key by key |
| machine | `wsl` | `wsl.exe` and the registry's list of distributions |
| | `winapi` | file attributes, Windows' virtual disk API, the console, a new window |
| | `vhdx`, `ext4` | the disk file's block table with its log replayed, and the filesystem's bitmaps; both only read |
| core | `units`, `version` | sizes as Windows shows them; the build's version |

## What runs where

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/pieces-dark.svg">
  <img alt="What runs where. wsl-shrinker.exe, the Windows program, reads the disks, runs each shrink and draws the screen or prints the commands. In a Windows terminal it takes the keys and shows the screen itself. In a WSL terminal, wsl-shrinker is the Linux build: it draws the screen there and asks wsl-shrinker.exe for every answer as JSON lines. wsl-shrinker.exe asks wsl.exe to list, stop, start and compact distributions and to run scripts inside them, compacts Docker Desktop's disk and other files through Windows' virtual disk API, and reads every disk file directly, without mounting it." src="assets/pieces-light.svg">
</picture>

`wsl-shrinker.exe` does the work: the disks are Windows' to read and compact. In WSL, `wsl-shrinker` is the Linux build
of the same code. It first decides where to run:

1. Outside WSL, or with `--demo` or `--snapshot`, it runs where it is: the demo needs nothing of Windows.
2. It finds `wsl-shrinker.exe` beside itself or on the PATH and reads its header. A build for another processor stops
   here, in the terminal, not in a Windows dialog on the desktop.
3. A screen (TUI mode, or `wsl-shrinker tui`) runs in the Linux terminal if `hello` finds `wsl-shrinker.exe` is the
   same build. `app/remote` stands in for the engine: it runs `wsl-shrinker.exe list --json`, `inside --json`,
   `history --json` and the hidden `backend` command (`hello`, `info`, `settings`, `pending`, `shrink`, `reopen`,
   `crashes`). `backend shrink` streams the run's events as JSON lines.
4. Anything else, every other command or a `wsl-shrinker.exe` of another build, goes to `wsl-shrinker.exe` by `exec`,
   with the distribution's name in `WSLENV`.

A shrink stops its distribution and the terminal in it. Started from inside that distribution, the run continues in a
new window (`start`, the default terminal), already confirmed. The old terminal waits for the new window's receipt
(`handoff-*`), so a window that never started is reported, not guessed.

## What it reads and what it changes

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/touches-dark.svg">
  <img alt="What wsl-shrinker reads and what it changes. It reads WSL's own answers (the distributions Windows lists, which run, WSL's version), the disk files (the VHDX block table and its log, ext4's block bitmaps) and, inside a running distribution, the sizes of caches and images. It runs as you, with no administrator rights and no network. It changes a disk only through Windows, with wsl --manage --compact or the virtual disk API; inside a distribution only the items you chose, and the containers it stops and starts again; and its own files: the settings, the history and the run's journal. Nothing changes before you confirm a shrink, and a protected disk is never shrunk." src="assets/touches-light.svg">
</picture>

A few rules keep that list short, each held in one place:

- **The readers only read.** `machine/vhdx` and `machine/ext4` open files read-only. They refuse what they do not fully
  understand rather than guess: a failed checksum, an unknown region marked required, a differencing disk.
- **Two places build commands.** Every command run inside a distribution comes from a fixed list in `engine/survey`
  (cleaning) or `engine/shrink` (stopping and starting). An id from the command line is matched against a fresh look
  inside, so only commands built there run.
- **One rule for "can it be shrunk".** `shrink.Preflight` decides; the run's check, the screen, `doctor` and `shrink`
  all ask it. The run itself refuses a protected disk, whatever asked for it.
- **One answer for "how was it left".** The end of `shrink.Run` decides it (`running`, `stopped`, `file`, `suspect`,
  `down`), even after a panic in the run's own code. The screen, the command line and the history report that answer.
- **One run at a time.** `run.json` is created whole, with a hard link, before anything changes. A second run finds it
  and stops. A run cut off leaves it behind, for `recover` to read.

## Decisions

### Read the disk rather than ask

WSL does not say what a compaction gives back, and Windows' tools say it only after. Two tables decide the size after,
and wsl-shrinker reads both without changing anything: the VHDX block table (which blocks the file stores) and
ext4's block bitmaps (which hold data). A disk WSL holds is read with its log replayed in memory, as Windows does on
open. [How it works](how-it-works.md#the-estimate) has the arithmetic.

### WSL compacts; wsl-shrinker does what is around it

The compaction is `wsl --manage <distro> --compact`: it trims the filesystem first and keeps WSL's state right.
wsl-shrinker adds the size before, what fills the disk, stopping containers with their own timeouts and the person's
pre-stop scripts, and reading the disk again after. Only files WSL does not register (Docker Desktop's disk, files from
the settings) go through Windows' virtual disk API.

### The screen in the terminal it was opened in

A Windows program started from WSL gets its own console, with its own keys and colors. Drawing the screen in the Linux
build keeps the person's terminal, tmux and zellij included, while `wsl-shrinker.exe` reads the disks. The two are one
build, checked by `hello`, so they never disagree on a format.

### No service, no administrator, no network

It runs when started and ends when done. WSL lets the user compact a distribution, and nothing wsl-shrinker reads needs
more. It sends nothing anywhere: no update check, no telemetry.

### Two modes, one engine

Every command works in both TUI and CLI mode. Both read `Preflight` and the run's outcome rather than deciding again.

## Tests

A change comes with a test that fails without it. Almost nothing needs a real disk.

| Level | Where | What it shows |
|:---|:---|:---|
| Layers and changelog | `internal/layers_test.go`, `internal/changelog_test.go` | no import goes up a layer; the changelog in shape, newest first, the badge naming the newest release |
| Readers | `machine/vhdx`, `machine/ext4` | files the tests build (`mkfs.ext4` for the filesystems), the log replayed, every refusal; both fuzzed |
| WSL's words | `machine/wsl` | `wsl.exe`'s own output, recorded in UTF-16 and UTF-8, through the parsing |
| A shrink | `engine/shrink` | every step on a fake WSL; `TestRecoveryMatrix`: for every point a run can fail at, on every kind of disk, how the distribution and its containers are left |
| Cleaning | `engine/survey` | the commands built for each item; `TestNoCommandTouchesWhatMustStay`: none could remove a volume or a container |
| The screen | `front/tui` | every view of the made-up PC at widths from 40 to 200 columns in English and Korean, each line exactly the screen's width; the setup wizard's pages |
| The command line | `front/cli` | both modes, the look of a mistake in a terminal and piped, setup's flags |
| The launcher | `cmd/wsl-shrinker` | where it runs, the processor and Windows build checks, with fake programs and no `cmd.exe` |
| Windows | `scripts/test-windows.sh`, CI | every package again as a Windows program; the screen and setup in a pseudoconsole; on Windows Server 2022 and 2025 and Windows 11 on Arm |
| A real WSL | CI | a throwaway Alpine distribution on Windows Server 2025's WSL 2 with Docker, two containers running and one stopped, and half a GiB freed inside: estimated, shrunk while it runs and read again, what came back held against the estimate, the containers against what ran; copies of its disk where Docker Desktop keeps its disk, where Docker's settings move it and from `extra_disks`, compacted through Windows' virtual disk API and held against their estimates; a WSL 1 one on Server 2022's built-in WSL, which must not be listed |
| Installers and packages | CI, release | `install.ps1` in Windows PowerShell 5.1 and PowerShell 7 and on ARM64, installed, run and removed; the Scoop manifest; the demo image |

No test opens a window: `winapi.Reopen` refuses inside a test binary, and the launcher's tests replace what it reaches
Windows through. [Contributing](../.github/CONTRIBUTING.md#run-the-tests) has the commands.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/release-dark.svg">
  <img alt="From a change to a release. make check runs gofmt, go vet and staticcheck for Linux and Windows, shellcheck, the unit tests with the race detector, and the layers and changelog tests. make test-windows runs every package again as a Windows program, from WSL. CI runs both, fuzzing and govulncheck, builds the release zips and the demo image, tests on Windows x64 and ARM64, shrinks a throwaway distribution on a real WSL 2, and runs install.ps1 in PowerShell 5.1 and 7, on every push and pull request and once a week. A vX.Y.Z tag runs all of CI, then publishes the zips with checksums, build provenance and a Scoop manifest, and the demo image; then it installs the release to check it." src="assets/release-light.svg">
</picture>

**CI** (`.github/workflows/ci.yml`) runs on every push to `main`, every pull request and every Monday, so a new Go
patch, runner image or WSL release shows up before a release does. It runs `make check`, a short fuzzing run and
`govulncheck` for Linux and Windows. It builds the release zips and the demo image from the commit and runs the image.
On each Windows runner it runs the tests, and the program against that runner's WSL. It shrinks a throwaway
distribution on a real WSL 2 (`.github/workflows/wsl.yml`), and installs, runs and removes the zip it built with
`install.ps1`. Windows 10 has no hosted runner; it runs the same code as Server 2022.

**The canary** (`.github/workflows/canary.yml`) runs that real shrink every Monday on WSL's newest pre-release
(`wsl --update --pre-release`), what the next WSL release will be. A change in WSL's commands, registry or disks shows
up there before people meet it. A failure there blocks nothing.

**A release** is a `vX.Y.Z` tag on a commit whose `CHANGELOG.md` has a `## [X.Y.Z]` section, with the README's badge
naming it. `.github/workflows/release.yml` runs all of CI on that commit. Then `scripts/dist.sh` builds the zips,
`checksums.txt` and the Scoop manifest, the zips get build provenance (`gh attestation verify`), and the release goes
out with the changelog's section as its notes. The demo image goes to `ghcr.io/kanggu12/wsl-shrinker-demo` for amd64
and arm64. Last, the installers run against the published release, as people do.
