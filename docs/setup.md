# Setup

Every way to install wsl-shrinker, what it needs, how to keep it current and remove it, and what differs between
Windows, WSL and PowerShell versions. Each way ends the same: one folder on your PATH holding the Windows program and
its launcher for WSL.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/ways-dark.svg">
  <img alt="The ways to install wsl-shrinker, and where it runs. The one-line PowerShell install checks the release's checksum, puts the folder on your PATH and opens setup. Scoop installs the manifest from the release, and scoop update keeps it current. By hand, the zip for your processor goes in any folder on your PATH; from source, make build. Each makes one folder holding wsl-shrinker.exe, the Windows program, and wsl-shrinker, the launcher for WSL. In PowerShell or Windows Terminal, wsl-shrinker opens the screen or prints the disks; inside WSL, the same name is found on the PATH Windows lends to WSL. Before installing, the demo image opens the screen on a made-up PC." src="assets/ways-light.svg">
</picture>

## Requirements

| | Needs | Best with |
|:---|:---|:---|
| Windows | Windows 10 version 2004 or later, Windows 11, or Windows Server 2022 or 2025, on x64 or ARM64 ([what differs](#windows)) | Windows 11 |
| WSL | WSL 2 distributions. Shrinking a distribution needs WSL 3.0.1 or later, which `wsl --update` installs ([what differs](#wsl)) | the newest WSL |
| Install script | Windows PowerShell 5.1, which every Windows has, or PowerShell 7 ([what differs](#powershell)) | |
| Screen | a terminal of 40 by 12 cells that reads VT sequences: Windows Terminal, the Windows console, VS Code's terminal, or any Linux terminal inside WSL ([what differs](#terminals)) | Windows Terminal, 120 by 30 cells |
| Demo sandbox | Docker | |

The program is 6.5 MB, and its launcher for WSL 6.2 MB; neither needs another library. It runs as you, with no
administrator rights, no service and no network, and only while you use it.

## Install

### PowerShell, one line

```powershell
irm https://github.com/kanggu12/wsl-shrinker/releases/latest/download/install.ps1 | iex
```

The script picks the zip for the PC's processor (the machine's, also when PowerShell itself runs emulated), checks it
against the release's `checksums.txt`, and puts `wsl-shrinker.exe` and the launcher in
`%LOCALAPPDATA%\Programs\wsl-shrinker`. It adds that folder to your user PATH once, so terminals opened from then on
find `wsl-shrinker`, in Windows and inside WSL, and on a first install opens setup in the console. An upgrade
replaces the two files and keeps the settings. A wsl-shrinker still running stops the install before anything is
replaced.

These environment variables, set before it runs, change what it does:

| Variable | |
|:---|:---|
| `WSL_SHRINKER_VERSION` | the release to install, like `v0.2.0` (default: the latest) |
| `WSL_SHRINKER_BIN_DIR` | where the program goes (default: `%LOCALAPPDATA%\Programs\wsl-shrinker`) |
| `WSL_SHRINKER_ZIP` | a zip already downloaded, installed as it is: see [without internet access](#without-internet-access) |
| `WSL_SHRINKER_BASE_URL` | where releases are downloaded from, for a mirror |
| `WSL_SHRINKER_NO_SETUP` | `1`: no setup after a first install. It is skipped anyway when nobody can answer: the console redirected, or PowerShell started with `-NonInteractive` |

### Scoop

```powershell
scoop install https://github.com/kanggu12/wsl-shrinker/releases/latest/download/wsl-shrinker.json
```

Each release carries the manifest. Scoop keeps the address it installed from, so `scoop update wsl-shrinker` (and
`scoop update *`) fetches it again and installs a newer release when there is one. The manifest puts the app's folder
on your PATH, as the one-line install does, rather than a shim: inside WSL, `wsl-shrinker` is a Linux program, and a
shim could not stand in for it.

### By hand

Download `wsl-shrinker_<version>_windows_amd64.zip`, or `_arm64.zip` for an ARM64 PC, from the
[releases](https://github.com/kanggu12/wsl-shrinker/releases), [check it](#check-a-download), and unpack both programs
into one folder on your PATH. A zip downloaded with a browser carries Windows' mark of a download, and SmartScreen asks
before the program's first run: clear it with `Unblock-File` on the zip before unpacking, once you have checked it.

### From source

With Go 1.26, inside WSL or anywhere with a shell:

```sh
git clone https://github.com/kanggu12/wsl-shrinker && cd wsl-shrinker
make build   # dist/wsl-shrinker.exe and dist/wsl-shrinker
```

Put both files from `dist/` in one folder on your PATH. A build from a commit says `dev` in `wsl-shrinker version`.

## Check a download

Every zip is listed in the release's `checksums.txt` with its SHA-256; the one-line install checks it for you:

```powershell
(Get-FileHash .\wsl-shrinker_0.2.0_windows_amd64.zip -Algorithm SHA256).Hash
```

The zips also carry build provenance, which GitHub signs: with the [GitHub CLI](https://cli.github.com),

```sh
gh attestation verify wsl-shrinker_0.2.0_windows_amd64.zip -R kanggu12/wsl-shrinker
```

shows that the zip was built by this repository's release workflow from the tagged commit.

## Without internet access

Download the zip for the PC's processor and `checksums.txt` on a machine that has access, carry them over with
`install.ps1`, check the zip, then install from it:

```powershell
$env:WSL_SHRINKER_ZIP = 'D:\wsl-shrinker_0.2.0_windows_amd64.zip'
& ([scriptblock]::Create((Get-Content D:\install.ps1 -Raw)))
Remove-Item Env:WSL_SHRINKER_ZIP
```

wsl-shrinker itself needs no network: it reads disks and asks WSL.

## Upgrade and remove

Run the one-line install again for the newest release; with Scoop, `scoop update wsl-shrinker`. The settings and the
history stay.

To remove it, the same script, told to:

```powershell
$env:WSL_SHRINKER_UNINSTALL = '1'; irm https://github.com/kanggu12/wsl-shrinker/releases/latest/download/install.ps1 | iex
```

It removes the two programs, and their folder with its PATH entry when nothing else is in it; a folder holding other
files, as one that was on your PATH before, keeps them and its entry. It clears the two variables again when it is
done, so a later install in the same window installs. Your settings
(`%APPDATA%\wsl-shrinker`) and history (`%LOCALAPPDATA%\wsl-shrinker`) stay, unless `$env:WSL_SHRINKER_PURGE = '1'`
is set too; a run that was cut off keeps its journal either way, for `wsl-shrinker recover`. With Scoop,
`scoop uninstall wsl-shrinker`, then delete those two folders if you want them gone.

## Versions

CI runs wsl-shrinker on every change on Windows Server 2025, where it imports a throwaway distribution with Docker and
running containers in it and shrinks it with WSL 2 for real, then compacts copies of its disk where Docker Desktop
keeps its disk, where Docker's settings move it, and as a file from `extra_disks`; on Windows Server 2022, with the WSL
built into Windows; and on Windows 11 on Arm, with the arm64 build. Windows 10 has no runner of its own; it runs the
same code as Server 2022. Docker Desktop itself does not run on these runners: its disk is a copy put where Docker
Desktop keeps it.

### Windows

| | |
|:---|:---|
| Windows 11 | everything, on x64 and ARM64; Windows Terminal is its default terminal |
| Windows 10, version 2004 and later | everything, on x64, and on ARM64 with the arm64 build: Windows 10 on ARM runs no x64 programs, and the WSL launcher says so rather than let Windows show a dialog. Its console's fonts lack a few marks ([terminals](#terminals)) |
| Windows 10, versions 1903 and 1909 | WSL 2 came to them as an update (build 18362.1049), but the WSL package, which shrinks distributions, needs 2004: there wsl-shrinker lists and estimates the disks and compacts Docker Desktop's, and shrinks no distribution |
| Windows Server 2025 and 2022 | everything, as on Windows 11 and Windows 10. CI shrinks a distribution on 2025's WSL 2 for real, and runs 2022 with the WSL built into Windows, WSL 1 only |
| Windows Server 2019 | WSL 1 only: no disk to shrink |

### WSL

wsl-shrinker asks WSL what it can do (`wsl --version`, and `wsl --help` for `--compact`) rather than assume it from a
version number, and tells WSL 1 from WSL 2 the way WSL does, by the distribution's registry flags:

| WSL | Listing, estimates, cleaning | Shrinking a distribution | Docker Desktop's disk, other files |
|:---|:---:|:---:|:---:|
| 3.0.1 or later (2.9.8 as a pre-release) | yes | yes | yes |
| the Store or MSI package, before 3.0.1 | yes | no: `doctor` says to run `wsl --update` | yes |
| built into Windows, with no `--version` | yes | no, as above | yes |
| WSL 1 distributions | not listed: they have no disk file | | |

`wsl --update` installs the newest WSL package on Windows 10 version 2004 and later, Windows 11 and Windows Server
2022 and 2025.

Docker Desktop's disk and the files from the settings are compacted through Windows' virtual disk API, which every
version of Windows 10 and 11 has.

### PowerShell

The one-line install runs the same in Windows PowerShell 5.1 and PowerShell 7, and CI runs it in both on every change.

- **Execution policy** does not apply to `irm ... | iex`, which runs no script file. A saved `install.ps1` run as a
  file needs `powershell -ExecutionPolicy Bypass -File install.ps1` on a PC that restricts scripts.
- **ConstrainedLanguage mode**, which AppLocker and Windows Defender Application Control turn on, refuses the .NET
  calls the script makes, from its first line on. Install [by hand](#by-hand) there.
- **A 32-bit or emulated PowerShell** installs the build for the machine's own processor all the same: the script
  reads it from the system, not from PowerShell.
- **AppLocker's default rules** run programs only from Windows' own folders and Program Files, so a program under
  `%LOCALAPPDATA%` does not start. An administrator can install it into an allowed folder instead, with
  `WSL_SHRINKER_BIN_DIR` set to it.

### Terminals

The screen draws with VT sequences: Windows Terminal, the Windows console, and VS Code's terminal read them, and inside
WSL so does any Linux terminal, tmux and zellij included. It needs 40 by 12 cells, and says so in a smaller window.

Its marks are `▪`, `✓`, `✗`, `⏎`, `↻`, a spinner of Braille dots, and a few arrows, dots and lines. Windows Terminal,
the default on Windows 11, finds a font for each. The Windows console's own window, which Windows 10 opens by default,
draws with one font only, and its fonts (Consolas, Lucida Console, and the Korean console fonts) lack most of them.
There wsl-shrinker draws plain ones by itself: `v`, `x`, `~` and a turning line, each as wide as the mark it stands
for, and the word `Enter` for `⏎`. On a Japanese, Chinese or Korean code page that console also draws arrows, dots
and lines two cells wide, which would push the screen off its lines, so there every one of them is plain ASCII.

`WSL_SHRINKER_SYMBOLS` chooses for any terminal: `plain`, `ascii`, or `unicode` for the marks themselves. Inside WSL a
terminal that does not say what it is (no `WT_SESSION`, `TERM_PROGRAM`, `TMUX`, `ZELLIJ` or SSH) is taken for the
Windows console's window and gets plain marks; the Linux side cannot see its code page, so set `ascii` there on a
Korean, Japanese or Chinese Windows, or open WSL in Windows Terminal, free for Windows 10 from the Microsoft Store.

### Docker Desktop

Its data disk is `%LOCALAPPDATA%\Docker\wsl\disk\docker_data.vhdx` in current releases and
`%LOCALAPPDATA%\Docker\wsl\data\ext4.vhdx` in older ones. A disk moved in Docker Desktop's settings (Resources,
Advanced, "Disk image location") is found where its settings say. wsl-shrinker finds each; a second one, as a disk an
older release left behind, is listed under a name of its own. Docker Desktop holds its disk while it runs: quit it to
shrink the disk.

## Inside WSL

WSL takes Windows' PATH into each distribution (`appendWindowsPath`, on by default), so the install folder is found
there too, and `wsl-shrinker` there is the launcher: the Linux build, which draws the screen in your Linux terminal and
asks `wsl-shrinker.exe` for every answer. That needs WSL's interop, also on by default. A distribution set up without
either: see [Troubleshooting](troubleshooting.md#wsl-shrinker-is-not-found-inside-wsl).

## Not offered

There are no Linux packages and no Helm chart or server image: wsl-shrinker has to run on the Windows host, which a
Linux package manager, a container or a cluster does not reach. The one image there is, the demo, shows the screen on
a made-up PC: `docker run --rm -it ghcr.io/kanggu12/wsl-shrinker-demo`.
