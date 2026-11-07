# Security

## Reporting a vulnerability

Please report a security problem privately: on GitHub, open the repository's **Security** tab and choose **Report a
vulnerability**. Do not open a public issue for it.

Say what an attacker can do, how to reproduce it, and which version you ran (a release tag or a commit). The
maintainer will reply, agree on a fix with you, and publish an advisory with the fixed release, crediting you unless
you ask not to be named.

The latest release and `main` get security fixes.

## What matters most

wsl-shrinker runs as you, reads WSL's disk files, runs commands as root inside your distributions, and has Windows
compact the files. These are the problems to report:

- a way to make it remove anything inside a distribution other than the item listed, or a Docker volume or container;
- a file name, image name or command output inside a distribution that makes it run a command it did not build;
- a disk file that makes its readers crash, hang or read outside the file;
- a shrink that reports success while the data on the disk changed;
- `scripts/install.ps1` installing something other than the release it checked.

It makes no network connection and needs no administrator rights.
