What `wsl-spawner stop NAME --yes --json --events` printed (`.stdout`, `.stderr`) and its exit code (`.code`), on the
made-up PC of wsl-spawner 0.1.2's own command-line tests, for each way a stop ends: `clean`, `hookfailed` (a hook
failed), `limit` (a step ran out of time), `busy` (another stop of it runs), `error` (the stop inside did not run to
its end), `notrunning`, `skipped` (a distribution it leaves to WSL), `unknown` (a name WSL does not have), `detached`
(the terminal ran in the distribution it stopped). The tests play them back as a fake wsl-spawner.
