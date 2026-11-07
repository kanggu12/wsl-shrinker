# Contributing to wsl-shrinker

wsl-shrinker changes disk files people cannot lose, so a change comes with a test showing it still refuses what it
should. Almost all of it can be worked on without touching a real disk: the readers are tested on VHDX files and ext4
filesystems the tests build, the steps of a shrink on a fake WSL, and the screen on a made-up PC (`--demo`).

## How the pieces fit

The code under `internal/` sits in layers; an import goes down them, never up.

| Layer | Packages | |
|---|---|---|
| front | `cli`, `tui`, `ui`, `i18n` | the command line, the screen, colors and words |
| app | `app` | puts the rest together for this PC |
| engine | `disks`, `survey`, `shrink`, `history` | finding disks, looking inside, the steps of a shrink, past runs |
| settings | `config` | `config.toml` |
| machine | `vhdx`, `ext4`, `wsl`, `winapi` | reading disk files, talking to WSL and Windows |
| core | `estimate`, `units` | the arithmetic, with no I/O |

`machine/vhdx` and `machine/ext4` only read, and refuse what they do not fully understand rather than guess. Every
command run inside a distribution is built in `engine/survey` (cleaning) or `engine/shrink` (stopping and starting);
nothing else builds one. [How it works](../docs/how-it-works.md) has the reasons behind each part.

## Build and try it

You need Go (the version in `go.mod`), shellcheck, e2fsprogs (`mkfs.ext4`, `debugfs`, `dumpe2fs`) for the ext4 tests,
and a Windows PC with WSL to run it for real. Development works from inside WSL:

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
```

The screen has a test that renders every view at widths from 40 to 200 columns, in English and Korean, and fails if
any line is not exactly the screen's width. `--snapshot 120x30 --keys "c space enter"` prints one frame of the demo
for a look.

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

Versions follow semantic versioning: a release that adds something raises the minor number, one that only fixes raises
the patch number. To release: move `[Unreleased]` to a `## [x.y.z] - date` section with its link, run `make badges`,
commit, and push a `vx.y.z` tag. The release workflow runs CI, builds the zips with `scripts/dist.sh`, and publishes them
with that section as the notes.

## Report a problem

[Open an issue](https://github.com/kanggu12/wsl-shrinker/issues) with `wsl-shrinker doctor` and `wsl-shrinker list
--json`. Report a security problem privately: see [SECURITY](SECURITY.md).
