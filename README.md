<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/logo-dark.svg">
    <img alt="wsl-shrinker" src="docs/assets/logo-light.svg" width="440">
  </picture>
</p>

<p align="center">
  <i>Give Windows back the space your WSL disks no longer use, and know how much before you start.</i><br>
  A terminal screen and a CLI for every WSL 2 disk on your PC.
</p>

<p align="center">
  <a href="https://github.com/kanggu12/wsl-shrinker/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/kanggu12/wsl-shrinker/actions/workflows/ci.yml/badge.svg"></a>
  <a href="https://github.com/kanggu12/wsl-shrinker/releases"><img alt="Release v0.8.1" src="docs/assets/badges/release.svg"></a>
  <a href="LICENSE"><img alt="License: MIT" src="docs/assets/badges/license.svg"></a>
  <a href="https://github.com/kanggu12/wsl-shrinker/pkgs/container/wsl-shrinker-demo"><img alt="Demo image" src="docs/assets/badges/demo.svg"></a>
  <img alt="Runs on Windows 10 and 11, x64 and ARM64, with WSL 2" src="docs/assets/badges/runs-on.svg">
</p>

<p align="center">
  <a href="#install">Install</a> ·
  <a href="#what-it-does">What it does</a> ·
  <a href="#how-it-works">How it works</a> ·
  <a href="#documentation">Documentation</a>
</p>

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/demo-dark.gif">
  <img alt="The wsl-shrinker screen on a made-up PC with five WSL disks. Ubuntu-24.04 gives back about 64 GB, and Docker Desktop's disk is in use. In Ubuntu-24.04 the Docker build cache and the pip cache are chosen, the shrink is confirmed, and the run goes through its six steps and gives back 81.5 GB." src="docs/assets/demo-light.gif">
</picture>

A WSL 2 distribution keeps its files in a virtual disk, `ext4.vhdx`, that grows but never shrinks on its own. WSL 3.0.1
and later can compact it (`wsl --manage <distro> --compact`). wsl-shrinker adds what that leaves out: how much comes
back, what fills the disk, containers stopped properly first, and a check of the result.

## Install

In PowerShell, on Windows 10 or 11 with WSL 2 ([requirements](docs/setup.md#requirements)):

```powershell
# Install for this user; it opens setup to choose how it works.
irm https://github.com/kanggu12/wsl-shrinker/releases/latest/download/install.ps1 | iex

# In a new terminal. It only reads until you confirm a shrink.
wsl-shrinker
```

The same `wsl-shrinker` works in a WSL terminal opened after the install. [Setup](docs/setup.md) has Scoop and every
other way to install it, and a [sandbox](docs/setup.md#try-it-in-a-sandbox) to try the screen in Docker first.

## What it does

- **The size after, before anything runs.** It reads the disk's block table and ext4's bitmaps, without mounting it.
- **Only what you choose goes.** Caches, downloads and unused Docker images are listed with their sizes; Docker
  volumes and containers never go.
- **Containers stop properly.** Each gets its own timeout and your pre-stop scripts run; with
  [wsl-spawner](https://github.com/kanggu12/wsl-spawner) installed, the stop goes through it.
- **It checks the result.** The disk is read again after the compaction; one that does not read as before stays
  stopped.
- **A closed window is not a lost run.** The next start finishes a run that was cut off.
- **Light.** One program: no service, no administrator rights, no network.

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/steps-dark.svg">
    <img alt="Six steps: check, clean, stop, compact, verify, start again. Check and verify only read, clean removes only what you chose, stop stops the containers, compact is WSL's own. Under them the disk, 312 GB now and 248 GB after: its mint blocks, 64 GB, go back to Windows at the compact step." src="docs/assets/steps-light.svg">
  </picture>
  <br>
  <i>A shrink, step by step: check and verify only read, and clean removes only what you chose.</i>
</p>

## How it works

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/pieces-dark.svg">
  <img alt="What runs where. wsl-shrinker.exe, the Windows program, reads the disks and runs every shrink. A Windows terminal sends it keys and shows its screen; in a WSL terminal, the Linux build draws the screen and asks the .exe as JSON lines. It asks wsl.exe, compacts other disks through Windows' virtual disk API, and reads the disk files without mounting them." src="docs/assets/pieces-light.svg">
</picture>

`wsl-shrinker.exe` does the work: the disks are Windows' to read and to compact. Inside WSL, `wsl-shrinker` is the
Linux build of the same program, which draws the screen in your terminal and asks the `.exe` for every answer.
[How it works](docs/how-it-works.md) has the estimate and every check of a shrink; [Internals](docs/internals.md) maps
the code.

## Documentation

| Using it | Understanding it | Changing it |
|:---|:---|:---|
| [Setup](docs/setup.md): try, install, upgrade, remove | [How it works](docs/how-it-works.md): the estimate, each step | [Internals](docs/internals.md): layers, decisions, tests |
| [Commands](docs/commands.md): flags, keys, JSON | [Troubleshooting](docs/troubleshooting.md): problems and what to do | [Contributing](.github/CONTRIBUTING.md): build, test, change |
| [Settings](docs/settings.md): `config.toml`, pre-stop.d | [Questions](docs/questions.md): your files, a closed window, WSL 1 | [Changelog](CHANGELOG.md) · [Security](.github/SECURITY.md) |

## License

MIT ([LICENSE](LICENSE)). The libraries wsl-shrinker is built from, and the font of its logo and figures, are listed
with their licenses in [THIRD_PARTY_NOTICES.md](docs/THIRD_PARTY_NOTICES.md).
