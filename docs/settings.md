# Settings

Settings live in `%APPDATA%\wsl-shrinker\config.toml`, and every key is optional. `wsl-shrinker setup` sets all but
`extra_disks`, in a wizard or with flags ([Commands](commands.md#setup)); add that one by hand. Setup writes only the
keys that changed and keeps the rest, comments included. With no file yet, it writes a whole commented file like the one
below, here with example values. The first start and the installer open setup. `wsl-shrinker config init` writes every
key at its default. A file that does not load stops wsl-shrinker with the reason and what to do, rather than running
with settings you did not choose; a wrong `language`, `theme`, `mode` or `min_gain_gb` is put right with the setup flag
the message names, like `wsl-shrinker setup --language auto`, which edits the file as it is.

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

`min_gain_gb` takes 0 to 1,000,000. An `extra_disks` path starts with a drive letter (`D:\VMs\data.vhdx`) or a network
share (`\\server\share\data.vhdx`) and ends in `.vhdx`; a file listed twice shows once. A file with another disk's name
(other tools use `ext4.vhdx` too) shows its folder in front, as `data/ext4.vhdx`; `protect` takes that name. A protected
disk shows in pink with its estimate, but neither the screen nor `wsl-shrinker shrink` can mark or shrink it.

## Before a distribution stops: pre-stop.d

Before compacting a running distribution, wsl-shrinker stops its Docker containers, each with its own stop timeout
(compose's `stop_grace_period`, Docker's 10 seconds otherwise). Then, inside the distribution, it runs each executable
file in `/etc/wsl-shrinker/pre-stop.d/` as root, in name order, up to two minutes each. Put there what a service needs
to save before WSL stops it:

```sh
sudo mkdir -p /etc/wsl-shrinker/pre-stop.d
sudo tee /etc/wsl-shrinker/pre-stop.d/10-postgres >/dev/null <<'EOF'
#!/bin/sh
systemctl stop postgresql
EOF
sudo chmod +x /etc/wsl-shrinker/pre-stop.d/10-postgres
```

A failed script is reported in the stop step; the run goes on. The whole stop step, containers and scripts, gets 15
minutes; past that, the run ends and restarts the containers it stopped.

## Files wsl-shrinker writes

| File | |
|:---|:---|
| `%APPDATA%\wsl-shrinker\config.toml` | the settings, from setup or `config init` |
| `%LOCALAPPDATA%\wsl-shrinker\history.jsonl` | one line per run, the newest 500 |
| `%LOCALAPPDATA%\wsl-shrinker\run.json` | the run in progress; one left behind is a run cut off, which `R` on the screen or `wsl-shrinker recover` finishes |
| `%LOCALAPPDATA%\wsl-shrinker\handoff-*` | a new window's empty receipt for the terminal a run moved from; removed once seen, or at the next move |
| `%LOCALAPPDATA%\wsl-shrinker\crash-*.txt` | each run's, empty while it runs and removed when it ends. After an error Go cannot recover from, it keeps what Go printed until a later run in a terminal says where it is, as that run ends, and renames it `...-shown.txt`. At most ten of each stay. The WSL launcher keeps one the same way in `~/.cache/wsl-shrinker` while it draws the screen |

## Environment

| Variable | |
|:---|:---|
| `WSL_SHRINKER_LANG` | `en` or `ko`: the language, after `--lang` and the settings, before Windows' display language |
| `WSL_SHRINKER_SYMBOLS` | `plain`, `ascii` or `unicode`: the marks to draw ([Setup](setup.md#terminals)). By default plain in the Windows console's own window, all ASCII there on an East Asian code page |
| `WSL_SHRINKER_SCREEN` | `windows`: inside WSL, the screen runs in `wsl-shrinker.exe`, not the Linux terminal |
| `NO_COLOR` | set: the commands print without colors, as when piped |

The install script takes its own ([Setup](setup.md#powershell-one-line)). [Setup](setup.md#upgrade-and-remove) covers
installing, upgrading and removing wsl-shrinker.
