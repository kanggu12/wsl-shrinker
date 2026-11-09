// Package version reports which build this is. Release builds set the values
// with -ldflags; a build from source reads the module's own build info.
package version

import (
	"regexp"
	"runtime/debug"
	"strings"
)

// pseudo matches the end of a Go pseudo-version, "-20261107192537-1d1924b3cf34"
// or ".20261107192537-1d1924b3cf34", with "+dirty" for uncommitted changes.
var pseudo = regexp.MustCompile(`[-.]\d{14}-[0-9a-f]{12}(\+dirty)?$`)

// moduleVersion is the version go build stamped, or "dev" for a commit with no
// tag: its pseudo-version only repeats the hash and date printed beside it.
func moduleVersion(v string) string {
	if v == "" || v == "(devel)" || pseudo.MatchString(v) {
		return "dev"
	}
	return v
}

// Set at release build time: -X github.com/kanggu12/wsl-shrinker/internal/version.Version=v0.1.0
var (
	Version = ""
	Commit  = ""
	Date    = ""
)

// Short returns the version alone, "v0.1.0", or "dev" for a source build:
// what the screen shows next to the name.
func Short() string {
	v, _, _ := strings.Cut(String(), " ")
	return v
}

// String returns "v0.1.0 (abc1234, 2026-11-04)" or the best a source build knows.
func String() string {
	v, c, d := Version, Commit, Date
	if info, ok := debug.ReadBuildInfo(); ok {
		// A tagged build names its tag; any other source build is "dev".
		if v == "" {
			v = moduleVersion(info.Main.Version)
		}
		for _, s := range info.Settings {
			switch {
			case s.Key == "vcs.revision" && c == "":
				c = s.Value
			case s.Key == "vcs.time" && d == "":
				d = s.Value
			}
		}
	}
	if v == "" {
		v = "dev"
	}
	if len(c) > 7 {
		c = c[:7]
	}
	if len(d) > 10 {
		d = d[:10]
	}
	switch {
	case c != "" && d != "":
		return v + " (" + c + ", " + d + ")"
	case c != "":
		return v + " (" + c + ")"
	}
	return v
}
