<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/logo-dark.svg">
    <img alt="wsl-shrinker" src="docs/assets/logo-light.svg" width="440">
  </picture>
</p>

<p align="center">
  <b>Give Windows back the space your WSL disks no longer use, and know how much before you start.</b><br>
  One terminal screen for every WSL 2 disk on your PC: what fills it, what a shrink frees, and a shrink that stops your
  containers properly and checks the result.
</p>

<p align="center">
  <a href="https://github.com/kanggu12/wsl-shrinker/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/kanggu12/wsl-shrinker/actions/workflows/ci.yml/badge.svg"></a>
  <a href="https://github.com/kanggu12/wsl-shrinker/releases"><img alt="Release v0.1.0" src="docs/assets/badges/release.svg"></a>
  <a href="LICENSE"><img alt="License: MIT" src="docs/assets/badges/license.svg"></a>
  <a href="https://github.com/kanggu12/wsl-shrinker/pkgs/container/wsl-shrinker-demo"><img alt="Demo image" src="docs/assets/badges/demo.svg"></a>
  <img alt="Runs on Windows 10 and 11, x64 and ARM64, with WSL 2" src="docs/assets/badges/runs-on.svg">
</p>

<p align="center">
  <a href="#quick-start">Quick start</a> |
  <a href="#why-wsl-shrinker">Why</a> |
  <a href="#a-shrink-step-by-step">A shrink</a> |
  <a href="#the-screen">The screen</a> |
  <a href="#commands">Commands</a> |
  <a href="docs/README.md">Documentation</a> |
  <a href="#open-source">Open source</a>
</p>

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/demo-dark.gif">
  <img alt="The wsl-shrinker screen on a PC with five WSL disks. Ubuntu-24.04 is selected: it gives back about 64 GB, and inside it the Docker build cache, Go modules and other caches and downloads are listed. Docker Desktop's disk shows as in use. In Ubuntu-24.04 two items are chosen, the shrink is confirmed, and the run goes through check, clean, stop, compact, verify and start again." src="docs/assets/demo-light.gif">
</picture>

A WSL 2 distribution keeps its files in a virtual disk, `ext4.vhdx`, that grows as Linux writes and never shrinks on its
own: deleting files inside Linux does not give Windows its space back. WSL 3.0.1 and later can compact a disk
(`wsl --manage <distro> --compact`), but it does not say how much that would give back, what fills the disk, or whether
the containers it stops were done writing. wsl-shrinker does those parts, and leaves the compaction to WSL.

## Quick start

Windows 10 or 11 with WSL 2, in PowerShell ([requirements](docs/setup.md#requirements)):

```powershell
# 1. Install for this user, into %LOCALAPPDATA%\Programs\wsl-shrinker. It opens setup to choose how it works.
irm https://github.com/kanggu12/wsl-shrinker/releases/latest/download/install.ps1 | iex

# 2. In a new terminal: the screen, or in CLI mode the disks and the commands to use.
#    It reads; nothing changes until you confirm a shrink.
wsl-shrinker
```

The same name works inside WSL, in a terminal opened after the install, since WSL takes Windows' folders onto its PATH
by default:

```sh
wsl-shrinker
```

With Scoop: `scoop install https://github.com/kanggu12/wsl-shrinker/releases/latest/download/wsl-shrinker.json`.
[Setup](docs/setup.md#install) has every way in, and how to remove it again. No install yet?
[Try it in a sandbox](#try-it-in-a-sandbox).

The installer and the first start open a short wizard, a page per choice, each showing what it does as you choose:

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/setup-dark.png">
  <img alt="The setup wizard at step 3 of 6. A rail on the left lists the steps, Language and Colors checked, Mode the step now. The page asks what wsl-shrinker should open, TUI chosen over CLI, with a small preview of the screen it opens: two disks and what each gives back. Below, a note that every command works either way. The keys: esc keeps the defaults, left goes back, up and down choose, enter goes on." src="docs/assets/setup-light.png">
</picture>

`wsl-shrinker setup` opens it again, and takes the same choices as flags.

## Why wsl-shrinker

- **The size after, before anything runs.** It reads the disk file itself, the VHDX block table and ext4's block
  bitmaps, without mounting it. On disks WSL made, the estimate matched the compacted file to the byte.
- **Only what you choose goes.** Package and language caches, model and browser downloads, the trash, the system
  journal, Docker's build cache and unused images are listed with what each frees, and you pick. Docker volumes and
  containers are never removed: the code that would do it does not exist.
- **Containers stop properly.** Each with its own stop timeout, then your scripts in `/etc/wsl-shrinker/pre-stop.d/`,
  so a database flushes before its distribution stops; afterwards, the ones that were running start again.
- **It checks the result.** The disk is read again after the compaction. One that does not read as before stays
  stopped, and the run says why.
- **A closed window is not a lost run.** Each run keeps a journal, and the next start finishes one that was cut off.
- **Light.** One program, run when you start it: no service, no administrator rights, no network.

## A shrink, step by step

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/estimate-dark.svg">
  <img alt="The disk file as blocks. Now: 312 GB, blocks of data that stay, a block freed inside but still stored, and space no block holds. After: 248 GB, only the data, packed; 64 GB goes back to Windows. The data that stays comes from ext4's block bitmaps; the stored blocks from the VHDX block table, its log replayed in memory." src="docs/assets/estimate-light.svg">
</picture>

A compaction keeps exactly the stored blocks that hold data, so the size after is known before it runs. While WSL holds
the file, its log is replayed in memory, as Windows would replay it; nothing is mounted or written. A disk read while
its distribution runs can be off by its last few seconds of writes. Docker Desktop's disk, and any file from the
settings, is compacted without a trim: only its blocks of zeros go, and its estimate counts it that way.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/steps-dark.svg">
  <img alt="Six steps: check, clean, stop, compact, verify, start again. Check and verify only read; clean removes only what you chose; stop stops containers and runs pre-stop scripts; compact is WSL's own; start again brings the distribution and its containers back." src="docs/assets/steps-light.svg">
</picture>

| Step | What happens |
|:---|:---|
| **Check** | Whether Windows can compact the disk: WSL's version, an encrypted file, Docker Desktop holding its disk, errors the filesystem recorded. Whether the distribution runs is read again. |
| **Clean** | Only what you chose. |
| **Stop** | Containers with their own stop timeouts, then your scripts in `/etc/wsl-shrinker/pre-stop.d/`. |
| **Compact** | WSL's own `wsl --manage --compact`, which trims the filesystem first. Docker Desktop's disk and files from the settings, which WSL does not register, go through Windows' virtual disk API, which drops only blocks of zeros. |
| **Verify** | The disk is read again: the filesystem must read without errors and hold what it held, less what you removed. |
| **Start again** | If it was running: writable (WSL has been seen to leave the root read-only after compacting; a second start clears it), with the containers that were running, and only those. A failed step brings it back too, unless the disk does not read as before. |

Started from a terminal inside the distribution being shrunk, the run continues in a new window, since stopping the
distribution closes the terminal. [How it works](docs/how-it-works.md) has every check.

## The screen

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/screenshot-dark.png">
  <img alt="The disks with their file sizes, sizes after a shrink and what each gives back, the selected disk's space as a bar, its checks, and the largest things inside it." src="docs/assets/screenshot-light.png">
</picture>

Each WSL 2 distribution, Docker Desktop's data disk, and any VHDX file you list in the settings, with the file's size
now, its size after a shrink, and what that gives back. A stopped distribution is not started to find out, and a
protected disk is never shrunk. Teal is what goes back to Windows, slate the data that stays, indigo what runs, pink
what is protected.

| Key | Does | Key | Does |
|:---:|:---|:---:|:---|
| <kbd>↑</kbd> <kbd>↓</kbd> | select a disk | <kbd>c</kbd> | choose what to remove inside it |
| <kbd>space</kbd> | mark it, to shrink several in one run | <kbd>r</kbd> | read the disks again |
| <kbd>s</kbd> or <kbd>⏎</kbd> | shrink the marked disks, or the selected one | <kbd>h</kbd> | past runs |
| <kbd>R</kbd> | finish a run that was cut off | <kbd>?</kbd> | every key and what each color means |

It speaks English and Korean (`--lang en` or `ko`; by default Windows' display language). In CLI mode, chosen in
setup, a bare `wsl-shrinker` prints the disks in the terminal's own colors, what is worth shrinking, and the commands to
go on with; `wsl-shrinker tui` opens the screen in either mode:

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/cli-dark.png">
  <img alt="CLI mode in a terminal: a headline saying WSL can compact, the disks as a table with their state, file size, size after and what each frees, a line saying two disks are worth shrinking for about 87 GB, and the commands wsl-shrinker shrink Ubuntu-24.04, inside Ubuntu-24.04, tui, setup and --help." src="docs/assets/cli-light.png">
</picture>

## Try it in a sandbox

```sh
docker run --rm -it ghcr.io/kanggu12/wsl-shrinker-demo
```

The screen on a made-up PC with five disks, in any terminal with Docker: mark disks, choose what to clean and run
shrinks freely; nothing real is read or changed. Put `-e WSL_SHRINKER_LANG=ko` before the image name for Korean. Once
installed, `wsl-shrinker --demo` opens the same PC.

## Commands

| Command | Does |
|:---|:---|
| `wsl-shrinker` | the screen in TUI mode, the disks and the commands to use in CLI mode |
| `tui` | the screen, in either mode |
| `setup` | choose the language, colors, mode, size worth shrinking and protected disks; with flags (`--mode cli`, `--protect NAME`, ...) without the wizard |
| `list [--json]` | every disk and what a shrink gives back |
| `inside DISTRO [--json]` | what can be removed inside a running distribution, with ids |
| `shrink DISK... [--clean DISK=ID,ID] [--yes]` | shrink by name, printing each step |
| `recover` | finish a run that was cut off |
| `history [--json]` | past runs, each with its steps |
| `doctor` | what a shrink needs on this PC, and what to do about what is missing |
| `config [path\|init]` | where the settings are, or a settings file with every key |

[Commands](docs/commands.md) has every flag, the `--json` shape and the exit codes.

## Settings

They live in `%APPDATA%\wsl-shrinker\config.toml`. `wsl-shrinker setup` writes the first three; `extra_disks` you add by
hand:

```toml
mode = "cli"                          # what a bare wsl-shrinker opens: "tui" or "cli"
min_gain_gb = 5                       # mark a disk worth shrinking from 5 GB back
protect = ["Work"]                    # disks, by name, that are never shrunk
extra_disks = ['D:\VMs\data.vhdx']    # more VHDX files to show and shrink
```

[Settings](docs/settings.md) lists every key, and the scripts a distribution runs before it stops.

## How it compares

| | Says what it gives back | Shows what fills it | Stops services first | Checks the result | Screen |
|:---|:---:|:---:|:---:|:---:|:---:|
| **wsl-shrinker** | exact, before | yes | yes | yes | terminal |
| `wsl --manage --compact` (WSL 3.0.1) | no | no | no, stops them as they are | no | none |
| `Optimize-VHD` / `diskpart` scripts | no | no | no | no | none |
| Export-and-import tools | estimate | no | no | no | none |
| WSL managers with a compact button | after | no | no | no | window |

wsl-shrinker uses `wsl --manage --compact` for the compaction itself: the parts around it are what it adds.

## Questions

**Will it delete my files?** No. Inside a distribution it removes only the items you choose, with the one command each
was listed with, right before the compaction. Docker volumes and containers are never removed.

**Is it safe to close the window during a shrink?** A compaction WSL started finishes on its own. What wsl-shrinker
would have done after it waits in its journal: the next start says so, and `R` or `wsl-shrinker recover` starts the
distribution and its containers again.

**Why did my terminal close?** Shrinking the distribution your terminal runs in stops it, so a run started from inside
it moves to a new window first.

**Does it work with WSL 1?** WSL 1 distributions keep their files on the Windows drive itself, with no disk file to
shrink, so they are not listed.

**What does it touch?**

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/touches-dark.svg">
  <img alt="What wsl-shrinker reads and what it changes. It reads WSL's own answers (the distributions Windows lists, which run, WSL's version), the disk files (the VHDX block table and its log, ext4's block bitmaps) and, inside a running distribution, the sizes of caches and images. It runs as you, with no administrator rights and no network. It changes a disk only through Windows, with wsl --manage --compact or the virtual disk API; inside a distribution only the items you chose, and the containers it stops and starts again; and its own files: the settings, the history and the run's journal. Nothing changes before you confirm a shrink, and a protected disk is never shrunk." src="docs/assets/touches-light.svg">
</picture>

## Documentation

| Guide | What it covers |
|:---|:---|
| [Setup](docs/setup.md) | requirements, every way to install, upgrade and remove; Windows, WSL and PowerShell versions |
| [How it works](docs/how-it-works.md) | finding the disks, the estimate, the log replay, each step of a shrink and its checks |
| [Commands](docs/commands.md) | every command and flag, the screen's keys, the `--json` shape and the exit codes |
| [Settings](docs/settings.md) | `config.toml`, the scripts a distribution runs before it stops, the files wsl-shrinker writes |
| [Troubleshooting](docs/troubleshooting.md) | a disk that gives back less than expected, a step that fails, an install that does not |
| [Internals](docs/internals.md) | the layers, what runs where, what it reads and changes, the decisions, the tests |

## Open source

wsl-shrinker is MIT licensed and built in the open. Its tests build the VHDX files and ext4 filesystems they read and
shrink on a fake WSL, so no change needs your own disks.

- **Report a disk it got wrong** in [Issues](https://github.com/kanggu12/wsl-shrinker/issues/new/choose), with
  `wsl-shrinker doctor`, `wsl-shrinker list --json` and, for a run, `wsl-shrinker history --json`.
- **Change the code.** [CONTRIBUTING.md](.github/CONTRIBUTING.md) has a map of the code and what a change needs.
- **See what changed** in [CHANGELOG.md](CHANGELOG.md), and report security problems through
  [SECURITY.md](.github/SECURITY.md).

```sh
git clone https://github.com/kanggu12/wsl-shrinker && cd wsl-shrinker
make check          # gofmt, go vet, staticcheck, shellcheck, the unit tests
make test-windows   # from WSL: every package again as a Windows program
make demo           # the screen on a made-up PC, in a container
```

## License

MIT ([LICENSE](LICENSE)). The libraries wsl-shrinker is built from and the font its logo and figures are drawn from
are listed with their licenses in [THIRD_PARTY_NOTICES.md](docs/THIRD_PARTY_NOTICES.md).
