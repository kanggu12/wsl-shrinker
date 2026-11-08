# Contributing to wsl-shrinker

wsl-shrinker changes disk files people cannot lose, so a change comes with a test showing it still refuses what it
should. Almost all of it can be worked on without touching a real disk: the readers are tested on VHDX files and ext4
filesystems the tests build, the steps of a shrink on a fake WSL, and the screen on a made-up PC (`--demo`).

## How the pieces fit

The code under `internal/` sits in layers; an import goes down them, never up, and `internal/layers_test.go` fails
when one goes up.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="../docs/assets/layers-dark.svg">
  <img alt="The packages in layers, highest first. cmd holds the program, which runs where it is started or hands over to wsl-shrinker.exe. front holds cli, tui, ui and i18n. app puts the rest together, and remote is the screen's link to wsl-shrinker.exe from inside WSL. engine holds disks, estimate, survey, shrink, journal and history. settings holds config. machine holds wsl, winapi, vhdx and ext4. core holds units and version, and does no I/O. Imports go down the layers, never up." src="../docs/assets/layers-light.svg">
</picture>

| Layer | Packages | |
|:---|:---|:---|
| front | `cli`, `tui`, `ui`, `i18n` | the command line, the screen, colors and words |
| app | `app`, `app/remote` | puts the rest together for this PC; the screen's backend from WSL, over `wsl-shrinker.exe` |
| engine | `disks`, `estimate`, `survey`, `shrink`, `history`, `journal` | finding disks, what a shrink gives back, looking inside, the steps of a shrink, past runs, the run in progress |
| settings | `config` | `config.toml` |
| machine | `vhdx`, `ext4`, `wsl`, `winapi` | reading disk files, talking to WSL and Windows |
| core | `units`, `version` | sizes as Windows shows them, with no I/O; the build's version |

`machine/vhdx` and `machine/ext4` only read, and refuse what they do not fully understand rather than guess. Every
command run inside a distribution is built in `engine/survey` (cleaning) or `engine/shrink` (stopping and starting);
nothing else builds one. Whether a disk can be shrunk now is decided once, by `shrink.Preflight`: the run's check, the
screen, `doctor` and `shrink` all ask it. How a run left its distribution is decided once too, at the end of
`shrink.Run`, and the result says it (`Left`); the fronts read that rather than the steps.
[Internals](../docs/internals.md) has what runs where and the reasons behind each part, and
[how it works](../docs/how-it-works.md) what a shrink does.

## Build and try it

You need Go (the version in `go.mod`; `go` fetches it if yours is older), shellcheck, e2fsprogs (`mkfs.ext4`,
`debugfs`, `dumpe2fs`) for the ext4 tests, Docker for the demo image, and a Windows PC with WSL to run it for real.
Development works from inside WSL:

```sh
git clone https://github.com/kanggu12/wsl-shrinker && cd wsl-shrinker
make build
dist/wsl-shrinker.exe list        # reads only
go run ./cmd/wsl-shrinker --demo  # the screen on a made-up PC, on any system
```

`list`, `inside`, `doctor` and the screen change nothing until you confirm a shrink. To try a shrink, make a
throwaway distribution and shrink only that one:

```sh
docker export "$(docker create alpine:3 true)" -o /tmp/alpine.tar
wsl.exe --import wss-test 'C:\wss-test' "$(wslpath -w /tmp/alpine.tar)" --version 2
dist/wsl-shrinker.exe shrink wss-test --yes
wsl.exe --unregister wss-test
```

## Run the tests

```sh
make check          # gofmt, go vet and staticcheck for Linux and Windows, shellcheck, go test -race ./...
make test-windows   # from WSL: every package's tests again as Windows programs
make fuzz           # the VHDX and ext4 readers, 30s each; a failing input lands in testdata/fuzz
make vuln           # govulncheck, for Linux and Windows
make demo           # the demo image from the work tree, opened in this terminal
```

The screen has a test that renders every view at widths from 40 to 200 columns, in English and Korean, and fails if
any line is not exactly the screen's width. `--snapshot 120x30 --keys "c space enter"` prints one frame of the demo
for a look, `setup --snapshot 100x30` the setup screen, and `--snapshot 100x24 --cli` CLI mode's disks.

A few tests hold the parts that are hardest to see go wrong:

- `TestRecoveryMatrix` (engine/shrink): for every point a run can fail at, on every kind of disk, whether the
  distribution and its containers run afterwards. A change to how failures are handled must keep its table.
- `TestScreenInAConsole` (cmd/wsl-shrinker, Windows): the program in a pseudoconsole, the headless console a terminal
  tab is made of, so no window opens: the screen draws, redraws for a new size, and `q` ends it. Beside it, setup takes
  keys there and writes its file, and a mistake reads in color with what to do.
- `recorded_test.go` (machine/wsl): wsl.exe's own output, recorded once with made-up distribution names, through the
  parsing. Record a new wsl.exe's output there when its format changes.

No test opens a window: `winapi.Reopen` refuses inside a test binary, and a test that runs a whole command sets the
App's `TerminalIn`, since a test binary started from WSL looks started from inside a distribution. A command line test
that needs a terminal replaces `stdinIsTerminal` and `stdoutIsTerminal` in the cli package, and the screens it would
open, `openScreen` and `openSetup`. The launcher's tests replace what it reaches Windows through (`locate`, `execve`,
`openRemote`, `runChild`, `exit`), the PC's processor and its Windows build (`hostMachine`, `windowsBuild`, so no test
starts `cmd.exe`), and give it only fake programs without the execute bit: a real one built for another processor
would put Windows' dialog on the desktop.

## Make a change

- **A new cache or download to clean**: add it to `caches` in `internal/engine/survey/survey.go`, with its kind
  (rebuilt on its own, or downloaded again), a word for it in both catalogs (`what.<name>`), and a line in the test's
  sample output. `TestNoCommandTouchesWhatMustStay` must keep passing.
- **Words on the screen**: every word goes through `i18n.T`, in `internal/front/i18n/catalog.go`, in English and
  Korean. `TestCatalogsComplete` fails for a key missing from either.
- **The disk readers**: add a case to the tests that builds the layout, and run `make fuzz` for a while.

## Style

Comments say why a line is there, nearly line by line; docs and comments are in English. A commit says what changed
for the person running wsl-shrinker. `CHANGELOG.md` gets a line under `[Unreleased]` for anything they would notice.

## Releases

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="../docs/assets/release-dark.svg">
  <img alt="From a change to a release. make check runs gofmt, go vet and staticcheck for Linux and Windows, shellcheck, the unit tests with the race detector, and the layers and changelog tests. make test-windows runs every package again as a Windows program, from WSL. CI runs both, fuzzing and govulncheck, builds the release zips and the demo image, tests on Windows x64 and ARM64, shrinks a throwaway distribution on a real WSL 2, and runs install.ps1 in PowerShell 5.1 and 7, on every push and pull request and once a week. A vX.Y.Z tag runs all of CI, then publishes the zips with checksums, build provenance and a Scoop manifest, and the demo image; then it installs the release to check it." src="../docs/assets/release-light.svg">
</picture>

A release is a `## [X.Y.Z] - date` section in `CHANGELOG.md` with its link at the bottom, `make badges`, a commit, and
an annotated `vX.Y.Z` tag on it once CI is green. Versions follow [semantic versioning](https://semver.org/): a release
that adds something raises the minor number (0.2.0), one that only fixes raises the patch number (0.1.1). Before 1.0 a
minor release may also change how something works, and its notes say what. The section is the release's notes:
`.github/workflows/release.yml` refuses a tag without one, and `internal/changelog_test.go` checks the file's shape and
that the README's badge names the newest release. A tag with a hyphen (`v0.2.0-rc.1`) goes out as a pre-release, with
a section of its own.

The workflow runs all of CI, then `scripts/dist.sh` builds the zips, `checksums.txt` and the Scoop manifest, the zips
get build provenance, and the demo image goes to `ghcr.io`; last, `.github/workflows/installers.yml` installs the
published release as people do. `scripts/dist.sh` builds only from a work tree that matches its commit, since the zips
carry that commit's name.

v0.3.0, v0.4.0 and v0.4.1 can never be released. v0.3.0 and v0.4.0 were published before the version numbers started
over at v0.1.0, and the Go module proxy keeps them; `go.mod` retracts them, and v0.4.1 is tagged only to carry that list
to the proxy, which reads retractions from the highest version it has. The release workflow ignores the three tags.

## Report a problem

[Open an issue](https://github.com/kanggu12/wsl-shrinker/issues). Its bug form asks for the output of
`wsl-shrinker doctor`, `wsl-shrinker list --json`, `wsl-shrinker history --json` for a run that went wrong, and the
versions. Report a security problem privately: see [SECURITY](SECURITY.md).
