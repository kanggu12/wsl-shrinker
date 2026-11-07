# Changelog

All notable changes are listed here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and
versions follow [semantic versioning](https://semver.org/): a release that adds something raises the minor number, one
that only fixes raises the patch number. Each release's section here is its release notes.

## [Unreleased]

## [0.1.0] - 2026-11-08

### Added

- A terminal screen listing every WSL 2 disk and Docker Desktop's data disk, with the file's size now, its size after a
  shrink, and what that gives back to Windows. The numbers come from reading the disk itself: the VHDX block table,
  with its log replayed in memory while WSL holds the file, and ext4's block bitmaps. Nothing is started or written to
  read them.
- What fills a running distribution: package and language caches, the system journal, Docker's build cache, unnamed
  images and images no container uses, each with the one command that removes it. Choose any of them to remove before
  the disk is compacted. Docker volumes and containers are never touched; the code that would do it does not exist.
- A shrink in steps the screen follows: check, clean what was chosen, stop the containers with their own timeouts and
  run `/etc/wsl-shrinker/pre-stop.d`, compact with `wsl --manage --compact` (or Windows' virtual disk API for files WSL
  does not register), read the disk again to check the filesystem holds the same data, and start the distribution
  again, writable, with its containers.
- Several disks in one run, a history of past runs, protected disks in the settings, English and Korean.
- Commands for scripts: `list`, `inside`, `shrink`, `history`, `doctor` and `config`, with `--json` where it applies.
- Started from inside WSL, it continues in a new window when it shrinks the distribution its terminal runs in.

[Unreleased]: https://github.com/kanggu12/wsl-shrinker/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/kanggu12/wsl-shrinker/releases/tag/v0.1.0
