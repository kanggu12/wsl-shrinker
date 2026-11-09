# Setup

Every way to install, upgrade and remove wsl-shrinker, what it needs, and what differs between Windows, WSL and
PowerShell versions.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/ways-dark.svg">
  <img alt="The ways to install wsl-shrinker, and where it runs. The one-line PowerShell install checks the release's checksum, puts the folder on your PATH and opens setup. Scoop installs the manifest from the release, and scoop update keeps it current. By hand, the zip for your processor goes in any folder on your PATH; from source, make build. Each makes one folder holding wsl-shrinker.exe, the Windows program, and wsl-shrinker, the launcher for WSL. In PowerShell or Windows Terminal, wsl-shrinker opens the screen or prints the disks; inside WSL, the same name is found on the PATH Windows lends to WSL. Before installing, the demo image opens the screen on a made-up PC." src="assets/ways-light.svg">
</picture>

## Requirements

| | Needs | Best with |
|:---|:---|:---|
| Windows | Windows 10 version 2004 or later, Windows 11, or Windows Server 2022 or 2025, on x64 or ARM64 ([what differs](#windows)) | Windows 11 |
| WSL | WSL 2 distributions. Shrinking one needs WSL 3.0.1 or later, from `wsl --update` ([what differs](#wsl)) | the newest WSL |
| Install script | Windows PowerShell 5.1, which every Windows has, or PowerShell 7 ([what differs](#powershell)) | |
| Screen | a VT terminal of 40 by 12 cells: Windows Terminal, the Windows console, VS Code's terminal, or any Linux terminal inside WSL ([what differs](#terminals)) | Windows Terminal, 120 by 30 cells |
| Demo sandbox | Docker | |

The program is 6.5 MB and its WSL launcher 6.2 MB; neither needs another library. It runs as you, only while you use it:
no administrator rights, no service, no network.

## Install

### PowerShell, one line

```powershell
irm https://github.com/kanggu12/wsl-shrinker/releases/latest/download/install.ps1 | iex
```

The script picks the zip for the machine's processor, checks it against the release's `checksums.txt`, and puts
`wsl-shrinker.exe` and the launcher in `%LOCALAPPDATA%\Programs\wsl-shrinker`. It adds that folder to your user PATH
once, so new terminals find `wsl-shrinker`, in Windows and inside WSL. A first install opens setup in the console. A
running wsl-shrinker stops the install before anything is replaced.

Environment variables set before it runs change what it does:

| Variable | |
|:---|:---|
| `WSL_SHRINKER_VERSION` | the release to install, like `v0.5.0` (default: the latest) |
| `WSL_SHRINKER_BIN_DIR` | where the program goes (default: `%LOCALAPPDATA%\Programs\wsl-shrinker`) |
| `WSL_SHRINKER_ZIP` | a downloaded zip to install as it is ([without internet access](#without-internet-access)) |
| `WSL_SHRINKER_BASE_URL` | where releases download from, for a mirror |
| `WSL_SHRINKER_NO_SETUP` | `1`: no setup after a first install. It is also skipped when nobody can answer: a redirected console, or PowerShell started with `-NonInteractive` |

### Scoop

```powershell
scoop install https://github.com/kanggu12/wsl-shrinker/releases/latest/download/wsl-shrinker.json
```

Each release carries the manifest. Scoop keeps the address it installed from, so `scoop update wsl-shrinker` (and
`scoop update *`) fetches the manifest again and installs any newer release. As with the one-line install, the app's
folder goes on your PATH, not a shim: inside WSL, `wsl-shrinker` is a Linux program, which a shim cannot stand in for.

### By hand

Download `wsl-shrinker_<version>_windows_amd64.zip`, or `_arm64.zip` for an ARM64 PC, from the
[releases](https://github.com/kanggu12/wsl-shrinker/releases), [check it](#check-a-download), and unpack both programs
into one folder on your PATH. A zip from a browser carries Windows' mark of a download, and SmartScreen asks before the
first run: once the zip checks out, clear the mark with `Unblock-File` before unpacking.

### From source

With Go 1.26, inside WSL or anywhere with a shell:

```sh
git clone https://github.com/kanggu12/wsl-shrinker && cd wsl-shrinker
make build   # dist/wsl-shrinker.exe and dist/wsl-shrinker
```

Put both files from `dist/` in one folder on your PATH. A build from a commit says `dev` in `wsl-shrinker version`.

## Check a download

The release's `checksums.txt` lists every zip's SHA-256:

```powershell
(Get-FileHash .\wsl-shrinker_0.5.0_windows_amd64.zip -Algorithm SHA256).Hash
```

GitHub signs the zips' build provenance. With the [GitHub CLI](https://cli.github.com),

```sh
gh attestation verify wsl-shrinker_0.5.0_windows_amd64.zip -R kanggu12/wsl-shrinker
```

shows that this repository's release workflow built the zip from the tagged commit.

## Without internet access

On a machine with access, download the zip for the PC's processor and `checksums.txt`. Carry them over with
`install.ps1`, check the zip, then install from it:

```powershell
$env:WSL_SHRINKER_ZIP = 'D:\wsl-shrinker_0.5.0_windows_amd64.zip'
& ([scriptblock]::Create((Get-Content D:\install.ps1 -Raw)))
Remove-Item Env:WSL_SHRINKER_ZIP
```

wsl-shrinker itself needs no network: it reads disks and asks WSL.

## Upgrade and remove

To upgrade, run the one-line install again, which replaces the two files, or `scoop update wsl-shrinker`. The settings
and history stay.

The same script removes it:

```powershell
$env:WSL_SHRINKER_UNINSTALL = '1'; irm https://github.com/kanggu12/wsl-shrinker/releases/latest/download/install.ps1 | iex
```

It removes the two programs, and their folder and PATH entry if nothing else is in it. A folder with other files, such
as one already on your PATH, keeps them and its entry. Settings (`%APPDATA%\wsl-shrinker`) and history
(`%LOCALAPPDATA%\wsl-shrinker`) stay unless `$env:WSL_SHRINKER_PURGE = '1'` is also set; a cut-off run keeps its journal
either way, for `wsl-shrinker recover`. Both variables are cleared at the end, so a later install in the same window
installs. With Scoop, run `scoop uninstall wsl-shrinker`, then delete those two folders if you want them gone.

## Versions

CI tests every change on Windows Server 2025, Windows Server 2022 and Windows 11 on Arm. On 2025 it imports a throwaway
distribution with Docker and running containers in it, and shrinks it on WSL 2 for real. It then compacts copies of the
distribution's disk where Docker Desktop keeps its disk, where Docker's settings move it, and as a file from
`extra_disks`; Docker Desktop itself runs on none of these runners. On 2022 it uses the WSL built into Windows, WSL 1
only, and on Arm the arm64 build. Windows 10 has no runner but runs the same code as Server 2022. A weekly run repeats
the real shrink on WSL's newest pre-release, so a change in the next WSL shows up before it ships.

### Windows

| | |
|:---|:---|
| Windows 11 | everything, on x64 and ARM64; Windows Terminal is its default terminal from version 22H2 |
| Windows 10, version 2004 and later | everything, on x64, and on ARM64 with the arm64 build: Windows 10 on ARM runs no x64 programs, and the WSL launcher says so instead of a Windows dialog. Its console's fonts lack a few marks ([terminals](#terminals)) |
| Windows 10, versions 1903 and 1909 | WSL 2 came as an update (builds 18362.1049 and 18363.1049), but the WSL package, which shrinks distributions, needs 2004: wsl-shrinker lists and estimates the disks and compacts Docker Desktop's, but shrinks no distribution |
| Windows Server 2025 and 2022 | everything, as on Windows 11 and Windows 10 |
| Windows Server 2019 | WSL 1 only: no disk to shrink |

### WSL

wsl-shrinker asks WSL what it can do (`wsl --version`, and `wsl --help` for `--compact`) instead of going by a version
number. It tells WSL 1 from WSL 2 as WSL does, by the distribution's registry flags:

| WSL | Listing, estimates, cleaning | Shrinking a distribution | Docker Desktop's disk, other files |
|:---|:---:|:---:|:---:|
| 3.0.1 or later (2.9.8 as a pre-release) | yes | yes | yes |
| the Store or MSI package, before 3.0.1 | yes | no: `doctor` says to run `wsl --update` | yes |
| built into Windows, with no `--version` | yes | no, as above | yes |
| WSL 1 distributions | not listed: they have no disk file | | |

`wsl --update` installs the newest WSL package on Windows 10 version 2004 and later, Windows 11 and Windows Server 2022
and 2025.

Docker Desktop's disk and the files from the settings are compacted with Windows' virtual disk API, which every Windows
10 and 11 has.

### PowerShell

The one-line install runs the same in Windows PowerShell 5.1 and PowerShell 7; CI runs it in both on every change.

- **Execution policy** does not apply to `irm ... | iex`, which runs no script file. Where scripts are restricted, a
  saved `install.ps1` run as a file needs `powershell -ExecutionPolicy Bypass -File install.ps1`.
- **ConstrainedLanguage mode**, which AppLocker and Windows Defender Application Control turn on, refuses the script's
  .NET calls from its first line: install [by hand](#by-hand) there.
- **A 32-bit or emulated PowerShell** still installs the build for the machine's processor: the script reads it from the
  system, not from PowerShell.
- **AppLocker's default rules** run programs only from Windows' own folders and Program Files, so one under
  `%LOCALAPPDATA%` does not start; an administrator can set `WSL_SHRINKER_BIN_DIR` to an allowed folder.

### Terminals

Every terminal in the [requirements](#requirements) reads the screen's VT sequences, and inside WSL so do tmux and
zellij. In a window under 40 by 12 cells, the screen says it needs more.

Its marks are `▪`, `✓`, `✗`, `⏎`, `↻`, a Braille-dot spinner, and a few arrows, dots and lines. Windows Terminal finds a
font for each. The Windows console's own window, Windows 10's default, uses one font, and its fonts (Consolas, Lucida
Console and the Korean console fonts) lack most marks, so wsl-shrinker draws plain ones there: `+`, `x`, `~` and a
turning line, each as wide as the mark it replaces, and `Enter` for `⏎`. On a Japanese, Chinese or Korean code page,
that console also draws arrows, dots and lines two cells wide, which would push the screen off its lines, so there every
mark is ASCII.

`WSL_SHRINKER_SYMBOLS` picks `plain`, `ascii` or `unicode` (the marks themselves) for any terminal. Inside WSL, a
terminal that does not say what it is (no `WT_SESSION`, `TERM_PROGRAM`, `TMUX`, `ZELLIJ` or SSH) counts as the Windows
console's window and gets plain marks. Linux cannot see that console's code page: on a Korean, Japanese or Chinese
Windows, set `ascii` or open WSL in Windows Terminal, free for Windows 10 from the Microsoft Store.

### Docker Desktop

wsl-shrinker finds Docker Desktop's data disk at `%LOCALAPPDATA%\Docker\wsl\disk\docker_data.vhdx` (current releases),
at `%LOCALAPPDATA%\Docker\wsl\data\ext4.vhdx` (older ones), or where Docker Desktop's settings move it (Resources,
Advanced, "Disk image location"). A second one, such as a disk an older release left behind, is listed under its own
name. Docker Desktop holds its disk while it runs: quit it to shrink the disk.

## Inside WSL

WSL adds Windows' PATH to each distribution (`appendWindowsPath`, on by default), so `wsl-shrinker` is found there too:
the launcher, a Linux build that draws the screen in your Linux terminal and asks `wsl-shrinker.exe` for every answer.
That needs WSL's interop, also on by default. For a distribution without either, see
[Troubleshooting](troubleshooting.md#wsl-shrinker-is-not-found-inside-wsl).

## Not offered

There are no Linux packages, Helm chart or server image: wsl-shrinker must run on the Windows host, out of reach of a
Linux package manager, container or cluster. The only image is the demo, the screen on a made-up PC:
`docker run --rm -it ghcr.io/kanggu12/wsl-shrinker-demo`.
