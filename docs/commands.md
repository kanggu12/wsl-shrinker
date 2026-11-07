# Commands

Every command runs on Windows, or from inside WSL through the launcher installed next to `wsl-shrinker.exe`.

| Command | What it does |
|---|---|
| `wsl-shrinker` | the screen; without a terminal it prints `list` |
| `wsl-shrinker list [--json]` | every disk: its state, file size, size after a shrink, and what that gives back |
| `wsl-shrinker inside DISTRO [--json]` | what can be removed inside a running distribution, with the id of each item |
| `wsl-shrinker shrink DISK... [--clean DISK=ID,ID]... [--yes] [--json]` | shrink the named disks one after another |
| `wsl-shrinker history [--json]` | past runs |
| `wsl-shrinker doctor` | what a shrink needs on this PC, and what to do about what is missing |
| `wsl-shrinker config path` | where the settings file is |
| `wsl-shrinker config init` | write a settings file with every key at its default |
| `wsl-shrinker version` | the version, commit and date of this build |

Flags for every command: `--lang en|ko` (default: the settings, then `WSL_SHRINKER_LANG`, then Windows' display
language). For the screen: `--theme dark|light` (default: ask the terminal) and `--demo`, a made-up PC where nothing
real is read or changed.

## shrink

```powershell
wsl-shrinker shrink Ubuntu-24.04
wsl-shrinker shrink Ubuntu-24.04 Debian --yes
wsl-shrinker inside Ubuntu-24.04
wsl-shrinker shrink Ubuntu-24.04 --clean "Ubuntu-24.04=docker-build-cache,pip:/home/ann/.cache/pip" --yes
```

Without `--yes` it asks once for all the disks named. Names are matched without regard to case. A name that is not a
disk, a protected disk, or a `--clean` id not found inside the distribution now stops it before anything runs. A
failure stops the disks after it; the exit code is then 1.

Run from a terminal inside a distribution it shrinks, it cannot stay: stopping the distribution closes the terminal.
The run then continues on the screen in a new window, and the command ends at once with code 0; the result is in that
window and in `wsl-shrinker history`. `--json` is refused there, since nothing would be printed. Once WSL has stopped
the distribution, the run goes to its end: Ctrl+C does not leave a distribution or its containers stopped.

## Keys on the screen

| Key | |
|---|---|
| `↑` `↓` | select a disk |
| `space` | mark a disk, to shrink several in one run |
| `s` or `⏎` | shrink the marked disks, or the selected one |
| `c` | choose what to remove inside the selected running distribution |
| `r` | read the disks again |
| `h` | past runs |
| `?` | help |
| `q` | quit |

In the clean list: `space` chooses an item, `a` every cache (nothing that needs a download or holds history), `n` none,
`⏎` continues to the shrink.

## Exit codes

| Code | |
|---|---|
| 0 | done |
| 1 | failed; the message says why |
| 2 | wrong usage: an unknown flag, disk or id |
