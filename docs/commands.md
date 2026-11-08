# Commands

Every command runs on Windows, or from inside WSL through the launcher installed next to `wsl-shrinker.exe`. From
inside WSL the screen runs in the Linux terminal itself and asks `wsl-shrinker.exe` for what it shows, so keys, resizing
and colors are the terminal's own; `WSL_SHRINKER_SCREEN=windows` keeps the screen in `wsl-shrinker.exe` instead. The
commands run in `wsl-shrinker.exe`.

| Command | What it does |
|:---|:---|
| `wsl-shrinker` | in a terminal, the screen in TUI mode, or the disks and the commands to use in CLI mode; the first start opens `setup` first. Without a terminal it prints `list` |
| `wsl-shrinker tui [--theme dark\|light]` | the screen, in either mode |
| `wsl-shrinker setup [flags]` | choose the language, colors, mode, size worth shrinking and protected disks: a screen, or the choices as flags |
| `wsl-shrinker list [--json]` | every disk: its state, file size, size after a shrink, and what that gives back |
| `wsl-shrinker inside DISTRO [--json]` | what can be removed inside a running distribution, with the id of each item |
| `wsl-shrinker shrink DISK... [--clean DISK=ID,ID]... [--yes] [--json]` | shrink the named disks one after another |
| `wsl-shrinker recover` | finish a run that was cut off: start its distribution and containers again |
| `wsl-shrinker history [--json]` | past runs, each with its steps |
| `wsl-shrinker doctor` | what a shrink needs on this PC, and what to do about what is missing; exits with 1 when something is missing |
| `wsl-shrinker config path` | where the settings file is |
| `wsl-shrinker config init` | write a settings file with every key at its default |
| `wsl-shrinker version` | the version, commit and date of this build (`dev` for a build of a commit that is not a release) |

With `--json`, each command prints one object that starts with `"schema": 1`, then what it reads: `disks`, `runs`,
`results`, or the look inside (the root filesystem's numbers, Docker, and the `items` with their ids). Sizes are in
bytes and their keys say so (`after_bytes`); times are in seconds (`took_seconds`). A later change to this shape
raises the schema's number.

Flags for every command: `--lang en|ko`, the language of the screen, setup and CLI mode's disks (default: the settings,
then `WSL_SHRINKER_LANG`, then Windows' display language); the other commands print English. For the screen:
`--theme dark|light` (default: the settings, then ask the terminal), and `wsl-shrinker --demo`, a made-up PC where
nothing real is read or changed. The environment variables that change how it looks are in
[Settings](settings.md#environment).

In a terminal, the commands mark how things went (`✓`, `✗`, `!`) in the terminal's own colors, with the meanings they
have on the screen, and a mistake reads as what
went wrong with what to do under it:

```text
✗ no disk named "ubunt"
  → see wsl-shrinker list
```

Piped, or with `NO_COLOR` set, they print the same words as plain text a script can read: each step of a run is a
line of its name, how it ended (`done`, `skipped`, `warned`, `failed`) and what it did; each check of `doctor` starts
with `ok`, `FAIL` or `note`; and a mistake is one line, `wsl-shrinker: no disk named "ubunt" (see wsl-shrinker list)`.

A bug reads the same way, `✗ stopped on a bug`, with where to report it under the lines a report needs, on the screen's
way out too. An error Go cannot recover from, such as a deadlock, ends with Go's own words; they are also kept in a
file, and a later run in a terminal says which as it ends, the screen inside WSL for `wsl-shrinker.exe` too (see
[Settings](settings.md#files-wsl-shrinker-writes)).

## TUI and CLI

Started bare in a terminal, wsl-shrinker works in the mode the settings say. TUI, the default, opens the screen. CLI
prints the disks in the terminal's own colors, with the screen's notes (what stands in a disk's way, what to do about
it), what is worth
shrinking, and the commands to go on with, with the disk that gives back most in them. Every command works in both;
`wsl-shrinker tui` opens the screen in CLI mode too.

## setup

```powershell
wsl-shrinker setup
wsl-shrinker setup --mode cli --min-gain 5 --protect Work
wsl-shrinker setup --language ko --theme light --unprotect Work
```

Without flags it opens a wizard, a page for each choice and a last one to check them: `↑` `↓` choose, and the page
shows what that does at once, the language and colors on the wizard itself; `⏎` or `→` goes on and `←` goes back. On
the disks' page `↑` `↓` select a disk and `space` protects it or lifts its protection. `⏎` on the last page writes the
choices, `esc` leaves without them, and `Ctrl+C` leaves writing nothing. With flags it opens no screen and works
anywhere, a script too:

| Flag | |
|:---|:---|
| `--language auto\|en\|ko` | the language |
| `--theme auto\|dark\|light` | the colors |
| `--mode tui\|cli` | what a bare start opens |
| `--min-gain GB` | what a disk must give back to be worth shrinking |
| `--protect NAME`, `--unprotect NAME` | a disk to protect, or to protect no more; once per disk |

What the flags leave out stays as it is; a value no setting takes stops with code 2.

Either way it writes only the keys that changed and leaves the rest of the file, comments included, as it was; with no
file yet it writes the whole commented file with the choices in it. A value the program would refuse is not written.

With no settings file, setup comes first: the first start in a terminal opens it before anything else, and the
installer opens it in a console on a first install. There, `esc` keeps the defaults, written down, so it does not
come back, and the start goes on; `Ctrl+C` writes nothing and ends the start, and setup opens again next time. An
upgrade keeps the settings it has. The installer skips setup with `WSL_SHRINKER_NO_SETUP=1`, and when nobody can
answer: its console redirected, or PowerShell started with `-NonInteractive`.

## shrink

```powershell
wsl-shrinker shrink Ubuntu-24.04
wsl-shrinker shrink Ubuntu-24.04 Debian --yes
wsl-shrinker inside Ubuntu-24.04
wsl-shrinker shrink Ubuntu-24.04 --clean "Ubuntu-24.04=docker-build-cache,pip:/home/ann/.cache/pip" --yes
```

Disks are named as `list` shows them, in any case: a distribution's name, `Docker Desktop data`, or a file's name.
Without `--yes` it asks once for all the disks named; with no terminal to ask in, it stops with code 2 and asks for
`--yes`. The summary and the question go to stderr, so stdout holds only the steps, or the JSON. Ctrl+C at the question
cancels. A name that is not a disk, a disk that cannot be shrunk now (protected, encrypted, in use, with filesystem
errors), a `--clean` id not found inside the distribution, or a run of another wsl-shrinker not finished stops it
before anything runs. `--clean` can be given more than once; an id named twice is removed once. Each step prints one
line: its name (`check`, `clean`, `stop`, `compact`, `verify`, `restart`, the same names as in the JSON), how it ended,
and what it found or did. A failure stops the disks after it, and a line says how it left the disk: compacted or
not, and whether its distribution runs; the exit code is then 1, or 3 when a distribution was left not running. A run
that started containers again ends with a note: WSL stops the distribution again once nothing from Windows runs in it,
and them with it (see [Troubleshooting](troubleshooting.md#containers-stopped-a-little-after-a-shrink)). In the JSON,
`containers_started` and `containers_not_started` count them.

Run from a terminal inside a distribution it shrinks, it cannot stay: stopping the distribution closes the terminal.
The run then continues on the screen in a new window. The command waits for that window to start, then ends with code
0, and the result is in that window and in `wsl-shrinker history`; if the window does not start, the command says so
and exits with 1. `--json` is refused there, since nothing would be printed. Once the stop step has
started, the run goes to its end: Ctrl+C does not leave a distribution or its containers stopped.

## Keys on the screen

| Key | |
|:---:|:---|
| `↑` `↓` | select a disk (`Home` `End` go to the first and the last) |
| `space` | mark a disk, to shrink several in one run |
| `s` or `⏎` | shrink the marked disks, or the selected one |
| `c` | choose what to remove inside the selected running distribution; also in the confirm box, when there is something to choose |
| `r` | read the disks again |
| `R` | finish a run that was cut off, when the screen says one was |
| `h` | past runs |
| `?` | help |
| `q` | quit |

In the confirm box, `⏎` or `y` shrinks, `esc` or `n` cancels. In the clean list, grouped by what removing costs:
`space` chooses an item, `a` every cache (nothing that needs a download or holds history), `n` none, `PgUp` `PgDn`
`Home` `End` move by pages, `⏎` continues to the shrink, `esc` goes back. A long disk list scrolls with the selection.
The help and the history scroll with `↑` `↓` `PgUp` `PgDn` `Home` `End`; `q` quits, and any other key goes back.

## Exit codes

| Code | |
|---:|:---|
| 0 | done |
| 1 | not done: a step failed, a disk cannot be shrunk now, another run is not finished, the answer to the question was no, or setup was left with `Ctrl+C`; the message says why |
| 2 | wrong usage: an unknown flag, command, disk, distribution or id, a `setup` flag value no setting takes, or a question or screen with no terminal for it |
| 3 | a run failed and left a distribution not running: it did not start again, or it stays stopped because its disk does not read as before |
