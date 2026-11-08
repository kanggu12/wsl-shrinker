# Settings

The settings file is `%APPDATA%\wsl-shrinker\config.toml`. Every key is optional. `wsl-shrinker setup` chooses the
language, colors, mode, size worth shrinking and protected disks, in a wizard or as flags (see
[Commands](commands.md#setup)), and writes only the keys that changed, leaving the rest of the file, comments
included, as it was; with no file yet it writes a whole commented file like the one below (here with example values).
The first start and the installer open setup. `extra_disks` has no question in setup: add it by hand.
`wsl-shrinker config init` writes a file with every key at its default. A file that does not load stops wsl-shrinker
with the reason and what to do, rather than running with settings you did not choose.

```toml
# Language of the screen, setup and CLI mode's disks: "en", "ko", or "auto":
# WSL_SHRINKER_LANG when it is set, then Windows' display language. The other
# commands print English, for scripts.
language = "auto"

# Colors: "auto" asks the terminal, or "dark", "light".
theme = "auto"

# What wsl-shrinker opens in a terminal: "tui", the screen, or "cli", the
# disks as a table with the commands to use. Every command works in both.
mode = "tui"

# A disk that gives back at least this many GB is marked worth shrinking.
min_gain_gb = 1

# More VHDX files to show, for disks of other tools, as full Windows paths.
extra_disks = ['D:\VMs\data.vhdx']

# Disks, by name, that are never shrunk.
protect = ["Work"]
```

`min_gain_gb` takes a number from 0 to 1,000,000. `extra_disks` takes full Windows paths, from a drive letter
(`D:\VMs\data.vhdx`) or a network share (`\\server\share\data.vhdx`), each a `.vhdx`; a file listed twice is shown once.
A file with the same name as another disk (other tools call theirs `ext4.vhdx` too) is listed with its folder in
front: `data/ext4.vhdx`, which `protect` takes as it is shown.

A protected disk is listed in pink with its estimate, but cannot be marked or shrunk, from the screen or from
`wsl-shrinker shrink`.

## Before a distribution stops: pre-stop.d

Before compacting a running distribution, wsl-shrinker stops its Docker containers, each with its own stop timeout
(compose's `stop_grace_period`, Docker's 10 seconds otherwise), then runs every executable file in
`/etc/wsl-shrinker/pre-stop.d/` inside the distribution, as root, in name order, up to two minutes each. Put there what
a service needs to save before WSL stops it:

```sh
sudo mkdir -p /etc/wsl-shrinker/pre-stop.d
sudo tee /etc/wsl-shrinker/pre-stop.d/10-postgres >/dev/null <<'EOF'
#!/bin/sh
systemctl stop postgresql
EOF
sudo chmod +x /etc/wsl-shrinker/pre-stop.d/10-postgres
```

A script that fails is reported in the run's stop step; the run goes on. The whole stop step, containers and scripts,
has 15 minutes; past that, the run ends there and starts the containers it stopped again.

## Files wsl-shrinker writes

| File | |
|:---|:---|
| `%APPDATA%\wsl-shrinker\config.toml` | the settings, when setup saves them (the first start, the installer, `wsl-shrinker setup`) or you run `config init` |
| `%LOCALAPPDATA%\wsl-shrinker\history.jsonl` | one line per run, the newest 500 |
| `%LOCALAPPDATA%\wsl-shrinker\run.json` | the run going on, while it goes; one left behind is a run cut off, which `R` on the screen or `wsl-shrinker recover` finishes |
| `%LOCALAPPDATA%\wsl-shrinker\handoff-*` | an empty receipt a new window leaves for the terminal a run moved from, removed once seen, or at the next move |
| `%LOCALAPPDATA%\wsl-shrinker\crash-*.txt` | each run's, empty while it runs and removed when it ends. After an error Go cannot recover from, it holds what Go printed, until a later run in a terminal says where it is, as that run ends, and renames it `...-shown.txt`. Ten of each wait at most. Inside WSL, the launcher keeps one the same way in `~/.cache/wsl-shrinker` while it draws the screen |

## Environment

| Variable | |
|:---|:---|
| `WSL_SHRINKER_LANG` | `en` or `ko`: the language of the screen, setup and CLI mode's disks, before Windows' display language and after `--lang` and the settings |
| `WSL_SHRINKER_SYMBOLS` | `plain`, `ascii` or `unicode`: the marks a terminal shows ([Setup](setup.md#terminals)); by default plain ones in the Windows console's own window, all ASCII there on an East Asian code page |
| `WSL_SHRINKER_SCREEN` | `windows`: from inside WSL, the screen runs in `wsl-shrinker.exe` instead of the Linux terminal |
| `NO_COLOR` | set: the commands print without colors, as they do piped |

The install script takes its own ([Setup](setup.md#powershell-one-line)). Installing, upgrading and removing
wsl-shrinker are in [Setup](setup.md#upgrade-and-remove).
