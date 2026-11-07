// Package version reports which build this is. Release builds set the values
// with -ldflags; a build from source reads the module's own build info.
package version

import "runtime/debug"

// Set at release build time: -X github.com/kanggu12/wsl-shrinker/internal/version.Version=v0.1.0
var (
	Version = ""
	Commit  = ""
	Date    = ""
)

// String returns "v0.1.0 (abc1234, 2026-11-04)" or the best a source build knows.
func String() string {
	v, c, d := Version, Commit, Date
	if info, ok := debug.ReadBuildInfo(); ok {
		if v == "" && info.Main.Version != "" && info.Main.Version != "(devel)" {
			v = info.Main.Version
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
