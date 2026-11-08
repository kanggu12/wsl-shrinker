# Security

## Reporting a vulnerability

Report a security problem privately, not in a public issue: on GitHub, open the repository's **Security** tab and
choose **Report a vulnerability**.

Say what an attacker can do, how to reproduce it, and which version you ran (a release tag or a commit). The
maintainer replies, agrees on a fix with you, and publishes an advisory with the fixed release. It credits you unless
you ask otherwise.

The latest release and `main` get security fixes.

## What matters most

wsl-shrinker runs as you, reads WSL's disk files, runs commands as root inside your distributions, and has Windows
compact the files. Report any of these:

- a way to make it remove anything in a distribution but the listed item, or a Docker volume or container;
- a file name, image name or command output in a distribution that makes it run a command it did not build;
- a disk file that makes its readers crash, hang or read outside the file;
- a shrink that reports success while the data on the disk changed;
- `scripts/install.ps1` installing anything but the release it checked, or removing anything but wsl-shrinker's own
  files and PATH entry.

It makes no network connection and needs no administrator rights.

## Checking a download

`checksums.txt` lists the SHA-256 of each release zip, and `install.ps1` checks it before installing. GitHub signs the
zips' build provenance: `gh attestation verify <zip> -R kanggu12/wsl-shrinker` shows this repository's release workflow
built them from the tagged commit.
