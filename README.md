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
  <a href="https://github.com/kanggu12/wsl-shrinker/releases"><img alt="Release v0.3.0" src="docs/assets/badges/release.svg"></a>
  <a href="LICENSE"><img alt="License: MIT" src="docs/assets/badges/license.svg"></a>
  <img alt="Windows 10 and 11, WSL 2" src="docs/assets/badges/runs-on.svg">
</p>

<p align="center">
  <a href="#quick-start">Quick start</a> ·
  <a href="#features">Features</a> ·
  <a href="#compared-with">Compared with</a> ·
  <a href="#commands">Commands</a> ·
  <a href="docs/README.md">Documentation</a>
</p>

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/demo-dark.gif">
  <img alt="The wsl-shrinker screen on a PC with five WSL disks. Ubuntu-24.04 is selected: it gives back about 64 GB, and inside it the Docker build cache, Go modules and other caches and downloads are listed. Docker Desktop's disk shows as in use. In Ubuntu-24.04 two items are chosen, the shrink is confirmed, and the run goes through check, clean, stop, compact, verify and start again." src="docs/assets/demo-light.gif">
</picture>

A WSL 2 distribution keeps its files in a virtual disk, `ext4.vhdx`, that grows as Linux writes and never shrinks on its
own: deleting files inside Linux does not give Windows its space back. WSL 3.0.3 can compact a disk
(`wsl --manage <distro> --compact`), but it does not say how much that would give back, what fills the disk, or whether
the containers it kills were done writing. wsl-shrinker does those parts, and leaves the compaction to WSL.

## Quick start

In PowerShell:

```powershell
# 1. Install for this user, into %LOCALAPPDATA%\Programs\wsl-shrinker, and choose how it works.
irm https://github.com/kanggu12/wsl-shrinker/releases/latest/download/install.ps1 | iex

# 2. In a new terminal: the screen, or in CLI mode the disks and the commands to use.
#    It reads; nothing changes until you confirm a shrink.
wsl-shrinker
```

The same name works inside WSL, once Windows' folders are on its PATH (they are by default):

```sh
wsl-shrinker
```

To look around first, `wsl-shrinker --demo` opens the screen on a made-up PC: nothing real is read or changed.

Requirements: Windows 10 or 11 with WSL 2. Shrinking a distribution needs WSL 3.0.3 or later (`wsl --update`);
`wsl-shrinker doctor` checks everything a shrink needs.

## Features

### The exact size after, before anything runs

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/estimate-dark.svg">
  <img alt="The disk file as blocks. Now: 312 GB, blocks of data that stay, a block freed inside but still stored, and space no block holds. After: 248 GB, only the data, packed; 64 GB goes back to Windows. The data that stays comes from ext4's block bitmaps; the stored blocks from the VHDX block table, its log replayed in memory." src="docs/assets/estimate-light.svg">
</picture>

wsl-shrinker reads the disk file itself: the VHDX block table says which 1 MiB blocks the file stores, and ext4's
block bitmaps say which of them hold anything. A compaction keeps exactly the stored blocks that do, so the size after
is known before it runs. On a disk WSL made, the estimate matched the compacted file to the byte. While WSL holds the
file, its log is replayed in memory, as Windows would replay it; nothing is mounted or written. A disk read while its
distribution runs can be off by its last few seconds of writes. Docker Desktop's disk, and any file from the
settings, is compacted without a trim: only its blocks of zeros go, and its estimate counts it that way.

### What fills it, and only what you choose goes

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/screenshot-dark.png">
  <img alt="The disks with their file sizes, sizes after a shrink and what each gives back, the selected disk's space as a bar, its checks, and the largest things inside it." src="docs/assets/screenshot-light.png">
</picture>

Inside a running distribution, wsl-shrinker lists package and language caches (APT, pip, npm, Yarn, Go, Cargo), model
and browser downloads, the trash, the system journal, Docker's build cache, unnamed images and images no container uses.
`c` lists them to choose from; chosen items are removed just before the disk is compacted. Docker volumes and
containers are never removed: the code that would do it does not exist.

### A shrink that stops things properly and checks the result

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/steps-dark.svg">
  <img alt="Six steps: check, clean, stop, compact, verify, start again. Check and verify only read; clean removes only what you chose; stop stops containers and runs pre-stop scripts; compact is WSL's own; start again brings the distribution and its containers back." src="docs/assets/steps-light.svg">
</picture>

| Step | What happens |
|:---|:---|
| **Check** | Windows can compact the disk: WSL's version, an encrypted file, Docker Desktop holding its disk, errors the filesystem recorded. Whether the distribution runs is read again. |
| **Clean** | Only what you chose. |
| **Stop** | Containers with their own stop timeouts, then your scripts in `/etc/wsl-shrinker/pre-stop.d/`, so a database flushes before its distribution stops. |
| **Compact** | WSL's own `wsl --manage --compact`, which trims the filesystem first. Docker Desktop's disk and files from the settings, which WSL does not register, go through Windows' virtual disk API, which drops only blocks of zeros. |
| **Verify** | The disk is read again: the filesystem must read without errors and hold what it held, less what you removed. |
| **Start again** | If it was running: writable (WSL has been seen to leave the root read-only after compacting; a second start clears it), with the containers that were running, and only those. A failed step brings it back too, unless the disk does not read as before. |

Started from a terminal inside the distribution being shrunk, the run continues in a new window, since stopping the
distribution closes the terminal. A run cut off on the way, by a closed window or a crash, can be finished the next time
wsl-shrinker starts: `R` on the screen, or `wsl-shrinker recover`, starts the distribution and its containers again.

### Every disk on the PC

Each WSL 2 distribution, Docker Desktop's data disk, and any VHDX file you list in the settings, with the file's size
now, its size after a shrink, and what that gives back. A stopped distribution is not started to find out. `space`
marks several disks and `s` shrinks them one after another; a protected disk is never shrunk. `h` shows past runs.

### TUI or CLI, chosen once

The installer asks in a console, as does the first start: the language, the colors, the mode, the size worth
shrinking, and the disks never to shrink. `wsl-shrinker setup` asks again, and takes the same choices as flags.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/setup-dark.png">
  <img alt="The setup screen on its first start: Language auto, following Windows; Colors auto; Mode TUI; Worth shrinking 1 GB. Below, the disks with what each gives back, the one called Work checked as protected. Enter writes the settings file, Esc keeps the defaults." src="docs/assets/setup-light.png">
</picture>

TUI mode, the default, opens the screen. CLI mode prints the disks, each in the terminal's own shade of its color
on the screen, what is worth shrinking, and the commands to go on with, with the disk that gives back most in them:

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/cli-dark.png">
  <img alt="CLI mode in a terminal: a headline saying WSL can compact, the disks as a table with their state, file size, size after and what each frees, a line saying two disks are worth shrinking for about 87 GB, and the commands wsl-shrinker shrink Ubuntu-24.04, inside Ubuntu-24.04, tui, setup and --help." src="docs/assets/cli-light.png">
</picture>

Every command works in both modes, and `wsl-shrinker tui` opens the screen in CLI mode too. In a terminal a mistake
reads as what went wrong, then what to do; piped, every command prints plain text a script can read.

## Compared with

| | Says what it gives back | Shows what fills it | Stops services first | Checks the result | Screen |
|:---|:---:|:---:|:---:|:---:|:---:|
| **wsl-shrinker** | exact, before | yes | yes | yes | terminal |
| `wsl --manage --compact` (WSL 3.0.3) | no | no | no, kills | no | none |
| `Optimize-VHD` / `diskpart` scripts | no | no | no | no | none |
| Export-and-import tools | estimate | no | no | no | none |
| WSL managers with a compact button | after | no | no | no | window |

wsl-shrinker uses `wsl --manage --compact` for the compaction itself: the parts around it are what it adds.

## Commands

| Command | What it does |
|:---|:---|
| `wsl-shrinker` | the screen in TUI mode, the disks and the commands to use in CLI mode |
| `wsl-shrinker tui` | the screen, in either mode |
| `wsl-shrinker setup` | choose the language, colors, mode, size worth shrinking and protected disks |
| `wsl-shrinker list` | every disk and what a shrink gives back |
| `wsl-shrinker inside DISTRO` | what can be removed inside a running distribution, with ids |
| `wsl-shrinker shrink DISK... [--clean DISK=ID,ID] [--yes]` | shrink by name, printing each step |
| `wsl-shrinker recover` | finish a run that was cut off |
| `wsl-shrinker history` | past runs |
| `wsl-shrinker doctor` | what a shrink needs, and what is missing |
| `wsl-shrinker config init` | write a settings file with every key |

`list`, `inside`, `shrink` and `history` take `--json`.

## Documentation

| Guide | |
|:---|:---|
| [How it works](docs/how-it-works.md) | the estimate, the log replay, each step and its checks |
| [Commands](docs/commands.md) | every command, flag and exit code |
| [Settings](docs/settings.md) | setup and `config.toml`: the mode, protected disks, extra VHDX files, language, colors; the pre-stop scripts |
| [Troubleshooting](docs/troubleshooting.md) | when a disk gives back less than expected, or a step fails |

## Open source

wsl-shrinker is MIT-licensed. Bugs and ideas go to [issues](https://github.com/kanggu12/wsl-shrinker/issues);
[CONTRIBUTING](.github/CONTRIBUTING.md) explains how to build and test it, [CHANGELOG](CHANGELOG.md) lists every
release, and [SECURITY](.github/SECURITY.md) says how to report a vulnerability.
