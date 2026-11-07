<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/logo-dark.svg">
    <img alt="wsl-shrinker" src="docs/assets/logo-light.svg" width="460">
  </picture>
</p>

<p align="center">
  <b>See where your WSL disk space went, clean it, shrink it, and check it worked.</b><br>
  One terminal screen for every WSL 2 disk on your PC, and a shrink that stops your containers properly first.
</p>

<p align="center">
  <a href="https://github.com/kanggu12/wsl-shrinker/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/kanggu12/wsl-shrinker/actions/workflows/ci.yml/badge.svg"></a>
  <a href="https://github.com/kanggu12/wsl-shrinker/releases"><img alt="Release v0.2.0" src="docs/assets/badges/release.svg"></a>
  <a href="LICENSE"><img alt="License: MIT" src="docs/assets/badges/license.svg"></a>
  <img alt="Windows 10 and 11, WSL 2" src="docs/assets/badges/runs-on.svg">
</p>

<p align="center">
  <a href="#quick-start">Quick start</a> |
  <a href="#what-a-shrink-does">What a shrink does</a> |
  <a href="#where-the-numbers-come-from">The numbers</a> |
  <a href="#compared-with">Compared with</a> |
  <a href="docs/README.md">Documentation</a> |
  <a href="#open-source">Open source</a>
</p>

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/demo-dark.gif">
  <img alt="The wsl-shrinker screen on a PC with five WSL disks. Ubuntu-24.04 is selected: it gives back about 64 GB, and inside it the Docker build cache, Go modules and other caches are listed. Two of them are chosen, the shrink is confirmed, and the run goes through check, clean, stop, compact, verify and start again." src="docs/assets/demo-light.gif">
</picture>

A WSL 2 distribution keeps its files in a virtual disk, `ext4.vhdx`, that grows as Linux writes and never shrinks on its
own: deleting files inside Linux does not give Windows its space back. WSL 3.0.3 can compact a disk
(`wsl --manage <distro> --compact`), but it does not say how much that would give back, what fills the disk, or whether
the containers it kills were done writing. wsl-shrinker does those parts, and leaves the compaction to WSL.

## Quick start

In PowerShell:

```powershell
# 1. Install for this user, into %LOCALAPPDATA%\Programs\wsl-shrinker.
irm https://github.com/kanggu12/wsl-shrinker/releases/latest/download/install.ps1 | iex

# 2. In a new terminal: the screen. It reads; nothing changes until you confirm a shrink.
wsl-shrinker
```

The same name works inside WSL, once Windows' folders are on its PATH (they are by default):

```sh
wsl-shrinker
```

To look around first, `wsl-shrinker --demo` opens the screen on a made-up PC: nothing real is read or changed.

Requirements: Windows 10 or 11 with WSL 2. Shrinking a distribution needs WSL 3.0.3 or later (`wsl --update`);
`wsl-shrinker doctor` checks everything a shrink needs.

## What you see

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/screenshot-dark.png">
  <img alt="The disks with their file sizes, sizes after a shrink and what each gives back, the selected disk's space as a bar, its checks, and the largest things inside it." src="docs/assets/screenshot-light.png">
</picture>

- **Every disk**: each WSL 2 distribution, Docker Desktop's data disk, and any VHDX file you list in the settings, with
  the file's size now, its size after a shrink, and what that gives back. A stopped distribution is not started to
  find out.
- **What fills a running distribution**: package and language caches (pip, npm, Yarn, Go, Cargo), model and browser
  downloads, the system journal, Docker's build cache, unnamed images and images no container uses. `c` lists them to
  choose from; chosen items are removed just before the disk is compacted.
- **Several disks at once**: `space` marks disks, `s` shrinks them one after another. `h` shows past runs.

## What a shrink does

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/steps-dark.svg">
  <img alt="Six steps: check, clean, stop, compact, verify, start again. Check and verify only read; clean removes only what you chose; stop stops containers and runs pre-stop scripts; compact is WSL's own; start again brings the distribution and its containers back." src="docs/assets/steps-light.svg">
</picture>

1. **Check** that Windows can compact the disk (WSL's version, an encrypted file, Docker Desktop holding its disk)
   before anything changes.
2. **Clean** only what you chose. Docker volumes and containers are never touched: the code that would do it does not
   exist.
3. **Stop** the containers with their own stop timeouts, then run your scripts in `/etc/wsl-shrinker/pre-stop.d/`,
   so a database flushes before its distribution stops.
4. **Compact** with WSL's own `wsl --manage --compact`, which trims the filesystem first. For Docker Desktop's disk,
   which WSL does not register, Windows' virtual disk API does the same.
5. **Verify**: read the disk again. The filesystem must be clean and hold what it held, less what you cleaned.
6. **Start again** if it was running: writable (WSL has been seen to leave the root read-only after compacting; a
   second start clears it), with the containers that were running, and only those.

Started from a terminal inside the distribution being shrunk, the run continues in a new window, since stopping the
distribution closes the terminal.

## Where the numbers come from

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/estimate-dark.svg">
  <img alt="The disk file now: regions, blocks in use, blocks freed but still stored, and space no block holds. After: the regions and the blocks in use." src="docs/assets/estimate-light.svg">
</picture>

wsl-shrinker reads the disk file itself: the VHDX block table says which 1 MiB blocks the file stores, and ext4's
block bitmaps say which of them hold anything. A compaction keeps exactly the stored blocks that do, so the size after
is known before it runs. On a disk WSL made, the estimate matched the compacted file to the byte. While WSL holds the
file, its log is replayed in memory, as Windows would replay it; nothing is mounted or written. A disk read while its
distribution runs can be off by its last few seconds of writes. [How it works](docs/how-it-works.md) has the details.

## Compared with

| | Says what it gives back | Shows what fills it | Stops services first | Checks the result | Screen |
|---|:---:|:---:|:---:|:---:|:---:|
| **wsl-shrinker** | exact, before | yes | yes | yes | terminal |
| `wsl --manage --compact` (WSL 3.0.3) | no | no | no, kills | no | none |
| `Optimize-VHD` / `diskpart` scripts | no | no | no | no | none |
| Export-and-import tools | estimate | no | no | no | none |
| WSL managers with a compact button | after | no | no | no | window |

wsl-shrinker uses `wsl --manage --compact` for the compaction itself: the parts around it are what it adds.

## Commands

| Command | What it does |
|---|---|
| `wsl-shrinker` | the screen |
| `wsl-shrinker list` | every disk and what a shrink gives back |
| `wsl-shrinker inside DISTRO` | what can be removed inside a running distribution, with ids |
| `wsl-shrinker shrink DISK... [--clean DISK=ID,ID] [--yes]` | shrink by name, printing each step |
| `wsl-shrinker history` | past runs |
| `wsl-shrinker doctor` | what a shrink needs, and what is missing |
| `wsl-shrinker config init` | write a settings file with every key |

`list`, `inside`, `shrink` and `history` take `--json`. [Commands](docs/commands.md) has every flag, and
[settings](docs/settings.md) every key: protected disks, extra VHDX files, language, colors.

## Documentation

| Guide | |
|---|---|
| [How it works](docs/how-it-works.md) | the estimate, the log replay, each step and its checks |
| [Commands](docs/commands.md) | every command, flag and exit code |
| [Settings](docs/settings.md) | `config.toml`, and the pre-stop scripts |
| [Troubleshooting](docs/troubleshooting.md) | when a disk gives back less than expected, or a step fails |

## Open source

wsl-shrinker is MIT-licensed. Bugs and ideas go to [issues](https://github.com/kanggu12/wsl-shrinker/issues);
[CONTRIBUTING](.github/CONTRIBUTING.md) explains how to build and test it, [CHANGELOG](CHANGELOG.md) lists every
release, and [SECURITY](.github/SECURITY.md) says how to report a vulnerability.
