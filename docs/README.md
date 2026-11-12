<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/logo-dark.svg">
    <img alt="wsl-shrinker" src="assets/logo-light.svg" width="300">
  </picture>
</p>

# Documentation

| Guide | What it covers |
|:---|:---|
| [Setup](setup.md) | requirements, a sandbox to try it, every way to install, checking a download, upgrading and removing; Windows, WSL, PowerShell and terminal versions |
| [How it works](how-it-works.md) | what it adds to WSL's compaction and how it compares, finding the disks, the estimate and the log replay, each step of a shrink and its checks |
| [Commands](commands.md) | the screen, TUI and CLI, every command, flag, key and exit code |
| [Settings](settings.md) | setup, `config.toml` with the mode, the pre-stop scripts, the files wsl-shrinker writes, the environment |
| [Troubleshooting](troubleshooting.md) | a disk that gives back less than expected, a step that fails, an install that stops |
| [Questions](questions.md) | deleting files, closing the window during a shrink, removing it, a terminal that closed, WSL 1, what it touches |
| [Internals](internals.md) | the layers, what runs where, what it reads and changes, the decisions, the tests and CI |
| [Third-party notices](THIRD_PARTY_NOTICES.md) | the licenses of what the program is built from |

For working on the code, see [CONTRIBUTING](../.github/CONTRIBUTING.md).
