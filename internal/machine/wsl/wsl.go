// Package wsl talks to WSL: the distributions registered for this user, which
// of them run, and the wsl.exe commands this tool needs (run a script inside
// a distribution, stop one, compact its disk). The real implementation is
// Windows-only; a fake one serves the tests and the demo.
package wsl

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"
)

// ErrNotWindows is returned by every call on a system that is not Windows.
var ErrNotWindows = errors.New("wsl-shrinker runs on Windows, where WSL is")

// ErrNoDistro wraps the error for a distribution name WSL does not have, so
// callers can tell a typo from a failure (errors.Is).
var ErrNoDistro = errors.New("no such distribution")

// ErrNotRunning is a script for a distribution that is stopped: Run never
// starts one as a side effect.
var ErrNotRunning = errors.New("not running")

// find returns the registered distribution named name, ignoring case as
// wsl.exe does, or an error wrapping ErrNoDistro.
func find(ds []Distro, name string) (Distro, error) {
	for _, d := range ds {
		if strings.EqualFold(d.Name, name) {
			return d, nil
		}
	}
	return Distro{}, fmt.Errorf("%w: %s", ErrNoDistro, name)
}

// describe is how an error names a wsl.exe call: its arguments, with a
// script given to sh -c shown as "<script>". The script is tens of lines
// of this program's own, which say nothing about what went wrong.
func describe(args []string) string {
	shown := make([]string, len(args))
	for i, a := range args {
		shown[i] = a
		if i > 0 && args[i-1] == "-c" {
			shown[i] = "<script>"
		}
	}
	return strings.Join(shown, " ")
}

// runningNames reads which distributions run from `wsl --list --running
// --quiet`. That call also fails when none runs, so a failure is checked
// against `wsl --list --quiet`: when listing all of them works, the failure
// meant "none running"; when that fails too, WSL did not answer, and that
// is an error, never taken as "all stopped" (a run would then stop a
// distribution without its containers' own stop, and leave it stopped).
func runningNames(running string, runErr error, all string, allErr error) (map[string]bool, error) {
	names := map[string]bool{}
	if runErr == nil {
		for _, l := range lines(running) {
			names[strings.ToLower(l)] = true
		}
		return names, nil
	}
	// A call that ran out of time, or was stopped, did not answer: it says
	// nothing about what runs.
	if allErr != nil || errors.Is(runErr, context.DeadlineExceeded) || errors.Is(runErr, context.Canceled) {
		return nil, fmt.Errorf("asking WSL which distributions run: %w", runErr)
	}
	// Some distribution is listed, yet none runs: an empty set.
	if len(lines(all)) == 0 {
		return nil, fmt.Errorf("asking WSL which distributions run: %w", runErr)
	}
	return names, nil
}

// Distro is one registered distribution.
type Distro struct {
	// Name is the name wsl.exe knows it by.
	Name string
	// ID is its registry key, a GUID.
	ID string
	// VHD is the full path of its disk file; empty for WSL 1.
	VHD string
	// Version is 1 or 2.
	Version int
	// Default is set for the default distribution.
	Default bool
	// Running is set while it runs.
	Running bool
}

// Info is what wsl.exe says about itself.
type Info struct {
	// Version is the WSL package version, like "3.0.3.0".
	Version string
	// CanCompact is set when `wsl --manage` offers --compact (WSL 3.0.3 and
	// later), which this tool uses to compact a distribution's disk.
	CanCompact bool
}

// System is what the rest of the tool asks of WSL.
type System interface {
	// Distros lists the registered distributions, with which ones run.
	Distros(ctx context.Context) ([]Distro, error)
	// Info returns the WSL version and what it can do.
	Info(ctx context.Context) (Info, error)
	// Run runs a shell script inside a running distribution as user and
	// returns its output. It never starts a stopped distribution: a script
	// for one that is not running fails.
	Run(ctx context.Context, distro, user, script string) (string, error)
	// Start starts a distribution (it stays up while something runs in it).
	Start(ctx context.Context, distro string) error
	// Terminate stops a distribution at once.
	Terminate(ctx context.Context, distro string) error
	// Compact trims and compacts a distribution's disk with
	// `wsl --manage <distro> --compact`; WSL stops the distribution first.
	Compact(ctx context.Context, distro string) error
}

// decode turns wsl.exe's output into text: its own messages are UTF-16LE,
// while output from inside a distribution is UTF-8.
func decode(b []byte) string {
	// UTF-16 text starts with a byte order mark, or has a zero high byte in
	// most of its characters (line breaks and Latin names do), even when the
	// first name is not Latin.
	if len(b) >= 2 && len(b)%2 == 0 && (b[0] == 0xff && b[1] == 0xfe || zeroHighBytes(b)*4 >= len(b)/2) {
		u := make([]uint16, len(b)/2)
		for i := range u {
			u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
		}
		return strings.TrimPrefix(string(utf16.Decode(u)), "\ufeff")
	}
	return string(b)
}

// zeroHighBytes counts the odd bytes that are zero: the high bytes of
// UTF-16LE characters below U+0100.
func zeroHighBytes(b []byte) int {
	n := 0
	for i := 1; i < len(b); i += 2 {
		if b[i] == 0 {
			n++
		}
	}
	return n
}

// lines splits output into trimmed, non-empty lines.
func lines(s string) []string {
	var out []string
	for l := range strings.SplitSeq(strings.ReplaceAll(s, "\r", ""), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// parseVersion finds the WSL version in `wsl --version` output, whose first
// line reads "WSL version: 3.0.3.0" (translated on other display languages,
// so only the number is relied on).
func parseVersion(out string) string {
	for _, l := range lines(out) {
		// The first line holding a dotted number is the package version.
		for f := range strings.FieldsSeq(l) {
			if strings.Count(f, ".") >= 2 && f[0] >= '0' && f[0] <= '9' {
				return f
			}
		}
	}
	return ""
}

// hasCompact reports whether `wsl --help` lists the compact option.
func hasCompact(help string) bool { return strings.Contains(help, "--compact") }
