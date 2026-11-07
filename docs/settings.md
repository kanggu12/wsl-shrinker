# Settings

The settings file is `%APPDATA%\wsl-shrinker\config.toml`. Every key is optional; `wsl-shrinker config init` writes
one with every key at its default. A file that does not load stops wsl-shrinker with the reason, rather than running
with settings you did not choose.

```toml
# Language of the screen: "en", "ko", or "auto": WSL_SHRINKER_LANG when it is
# set, then Windows' display language.
language = "auto"

# Colors: "auto" asks the terminal, or "dark", "light".
theme = "auto"

# A disk that gives back at least this many GB is marked worth shrinking.
min_gain_gb = 1

# More VHDX files to show, for disks of other tools.
extra_disks = ['D:\VMs\data.vhdx']

# Disks, by name, the dashboard never shrinks.
protect = ["Work"]
```

A protected disk is listed in purple with its estimate, but cannot be marked or shrunk, from the screen or from
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

A script that fails is reported in the run's stop step; the run goes on.

## Files wsl-shrinker writes

| File | |
|---|---|
| `%APPDATA%\wsl-shrinker\config.toml` | the settings, only when you run `config init` |
| `%LOCALAPPDATA%\wsl-shrinker\history.jsonl` | one line per run, the newest 500 |
