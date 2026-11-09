# Commands

Commands run in `wsl-shrinker.exe`, on Windows or from WSL through the launcher installed next to it. Inside WSL the
screen runs in the Linux terminal and asks `wsl-shrinker.exe` what to show, so keys, resizing and colors are the
terminal's own; `WSL_SHRINKER_SCREEN=windows` keeps it in `wsl-shrinker.exe`.

| Command | What it does |
|:---|:---|
| `wsl-shrinker` | in a terminal, the screen (TUI mode) or the disks and commands to use (CLI mode), after `setup` on the first start. Without a terminal it prints `list` |
| `wsl-shrinker tui [--theme auto\|dark\|light]` | the screen, in either mode |
| `wsl-shrinker setup [flags]` | choose the language, colors, mode, size worth shrinking and protected disks, on a screen or with flags |
| `wsl-shrinker list [--json]` | every disk: its state, file size, size after a shrink, and what that gives back |
| `wsl-shrinker inside DISTRO [--json]` | what can be removed inside a running distribution, each item with its id |
| `wsl-shrinker shrink DISK... [--clean DISK=ID,ID]... [--yes] [--json]` | shrink the named disks one after another |
| `wsl-shrinker recover` | finish a cut-off run: start its distribution and containers again |
| `wsl-shrinker history [--json]` | past runs, each with its steps |
| `wsl-shrinker doctor` | what a shrink needs on this PC and how to fix what is missing; exits with 1 if something is missing |
| `wsl-shrinker config path` | where the settings file is |
| `wsl-shrinker config init` | write a settings file with every key at its default |
| `wsl-shrinker version` | this build's version, commit and date (`dev` for a build that is not a release) |

`--json` prints one object: `"schema": 1`, then `disks`, `runs`, `results`, or the look inside (the root filesystem's
numbers, Docker, and the `items` with their ids). Each disk in `list --json` says whether it is `protected`, and in
`blocked` the first thing that stands in the way of a shrink now (`protected`, `held`, ...), `""` for none. Sizes are in bytes and times in seconds, as keys like `after_bytes`
and `took_seconds` say. A change to this shape raises the schema number.

Every command takes `--lang auto|en|ko`, the language of the screen, setup and CLI mode's disks (default: the settings,
then `WSL_SHRINKER_LANG`, then Windows' display language); other commands print English. The screen takes
`--theme auto|dark|light` (default: the settings, then ask the terminal), and `wsl-shrinker --demo` shows a made-up PC
where nothing real is read or changed. [Settings](settings.md#environment) lists the environment variables for the look.

In a terminal, the commands mark how things went with `✓`, `✗` and `!`, in the terminal's own colors and with the
screen's meanings. A mistake says what went wrong and, under it, what to do:

```text
✗ no disk named "ubunt"
  → see wsl-shrinker list
```

Piped, or with `NO_COLOR` set, they print the same words as plain text for scripts: each step of a run is a line of its
name, how it ended (`done`, `skipped`, `warned`, `failed`) and what it did; each `doctor` check starts with `ok`, `FAIL`
or `note`; and a mistake is one line, `wsl-shrinker: no disk named "ubunt" (see wsl-shrinker list)`.

A bug reads the same way, `✗ stopped on a bug`, with where to report it under the lines a report needs, also as the
screen exits. An error Go cannot recover from, such as a deadlock, ends with Go's own words. A file keeps them, and a
later run in a terminal names that file as it ends; inside WSL the screen does this for `wsl-shrinker.exe` too
([Settings](settings.md#files-wsl-shrinker-writes)).

## TUI and CLI

A bare start in a terminal opens the mode the settings name. TUI, the default, is the screen. CLI prints, in the
terminal's own colors, the disks with the screen's notes (what stands in a disk's way, what to do about it), what is
worth shrinking, and the commands to go on with, with the disk that gives back most in them. Every command works in both
modes.

## setup

```powershell
wsl-shrinker setup
wsl-shrinker setup --mode cli --min-gain 5 --protect Work
wsl-shrinker setup --language ko --theme light --unprotect Work
```

Without flags it opens a wizard: a page per choice, then one to check them. `↑` `↓` choose, and the page shows the
result at once, the language and colors on the wizard itself. `⏎` or `→` goes on, `←` back. On the disks' page, `↑` `↓`
select a disk and `space` toggles its protection. `⏎` on the last page writes the choices, `esc` leaves without them,
and `Ctrl+C` leaves writing nothing. With flags there is no screen, so it works anywhere, scripts included:

| Flag | |
|:---|:---|
| `--language auto\|en\|ko` | the language |
| `--theme auto\|dark\|light` | the colors |
| `--mode tui\|cli` | what a bare start opens |
| `--min-gain GB` | what a disk must give back to be worth shrinking |
| `--protect NAME`, `--unprotect NAME` | a disk to protect, or to protect no more; once per disk |

Settings the flags leave out stay; a value no setting takes stops with code 2.

Either way, setup writes only the keys that changed and keeps the rest of the file, comments included; with no file yet,
it writes the whole commented file with the choices in it. It never writes a value the program would refuse.

With no settings file, setup comes first: the first start in a terminal opens it, and the installer opens it in a
console on a first install. There, `esc` writes the defaults, so setup does not come back, and the start goes on;
`Ctrl+C` writes nothing and ends the start, and setup opens again next time. An upgrade keeps the settings. The
installer skips setup with `WSL_SHRINKER_NO_SETUP=1`, or when nobody can answer: a redirected console, or PowerShell
started with `-NonInteractive`.

## shrink

```powershell
wsl-shrinker shrink Ubuntu-24.04
wsl-shrinker shrink Ubuntu-24.04 Debian --yes
wsl-shrinker inside Ubuntu-24.04
wsl-shrinker shrink Ubuntu-24.04 --clean "Ubuntu-24.04=docker-build-cache,pip:/home/ann/.cache/pip" --yes
```

Name disks as `list` shows them, in any letter case: a distribution's name, `Docker Desktop data`, or a file's name.
Without `--yes` it asks once for all of them; with no terminal to ask in, it stops and asks for `--yes`. The summary and
the question go to stderr, so stdout holds only the steps or the JSON. Ctrl+C at the question cancels. `--clean` can
repeat; an id named twice is removed once.

Nothing runs if a name is not a disk, a disk cannot be shrunk now (protected, encrypted, in use, with filesystem
errors), a `--clean` id is not in the distribution, or another wsl-shrinker's run is not finished.

Each step prints a line: its name (`check`, `clean`, `stop`, `compact`, `verify`, `restart`, as in the JSON), how it
ended, and what it found or did. A failure skips the disks after it, with a line on whether the disk was compacted and
whether its distribution runs.

When a run starts a distribution and its containers again, a note under that disk's result says WSL stops them once no
terminal is open in the distribution ([Troubleshooting](troubleshooting.md#containers-stopped-a-little-after-a-shrink)).
A failed stop step leaves the distribution running, with no note. `recover` adds the same note. In the JSON,
`distro_started` marks a distribution started again, `containers_started` and `containers_not_started` count containers,
and false or 0 values are left out.

Run from a terminal inside a distribution it shrinks, the run moves to the screen in a new window, since stopping the
distribution closes that terminal. The command exits with 0 once that window starts, or says it did not and exits with
1; the result shows in the window and in `wsl-shrinker history`. `--json` is refused there, since nothing would be
printed. Once the stop step starts, the run finishes: Ctrl+C does not leave a distribution or its containers stopped.

## Keys on the screen

| Key | |
|:---:|:---|
| `↑` `↓` | select a disk (`Home` `End`: the first, the last) |
| `space` | mark a disk, to shrink several in one run |
| `s` or `⏎` | shrink the marked disks, or the selected one |
| `c` | choose what to remove inside the selected running distribution; also in the confirm box, when there is a choice |
| `r` | read the disks again |
| `R` | finish a cut-off run, when the screen says there is one |
| `h` | past runs |
| `?` | help |
| `q` | quit |

In the confirm box, `⏎` or `y` shrinks and `esc` or `n` cancels. The clean list groups items by what removing costs:
`space` chooses one, `a` every cache (nothing that needs a download or holds history), `n` none; `PgUp` `PgDn` move by
pages and `Home` `End` to the first and the last, `⏎` goes on to the shrink, `esc` back. A long disk list scrolls with
the selection. Help and history scroll with `↑` `↓` `PgUp` `PgDn` `Home` `End`; `q` quits, any other key goes back.

## Exit codes

| Code | |
|---:|:---|
| 0 | done |
| 1 | not done: a step failed, a disk cannot be shrunk now, another run is not finished, the answer to the question was no, or setup was left with `Ctrl+C`; the message says why |
| 2 | wrong usage: an unknown flag, command, disk, distribution or id, a `setup` flag value no setting takes, or a question or screen with no terminal for it |
| 3 | a run failed and left a distribution not running: it did not start again, or stays stopped because its disk does not read as before |
