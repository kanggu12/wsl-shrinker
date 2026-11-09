// Package config reads the settings file, %APPDATA%\wsl-shrinker\config.toml.
// Every setting has a default, so the file is optional.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config is every setting.
type Config struct {
	// Language is "auto" (follow Windows), "en" or "ko".
	Language string `toml:"language"`
	// Theme is "auto" (ask the terminal), "dark" or "light".
	Theme string `toml:"theme"`
	// Mode is what wsl-shrinker opens in a terminal when started bare: "tui",
	// the screen, or "cli", the disks as a table with the commands to use.
	// Every command works in both.
	Mode string `toml:"mode"`
	// MinGainGB is the least a disk must give back, in GB, to be marked as
	// worth shrinking on the screen.
	MinGainGB float64 `toml:"min_gain_gb"`
	// ExtraDisks lists more VHDX files to show (disks of other tools).
	ExtraDisks []string `toml:"extra_disks"`
	// Protect lists disks, by name, that are never shrunk, from the screen
	// or the command line.
	Protect []string `toml:"protect"`
}

// Default is what applies without a file.
func Default() Config {
	return Config{Language: "auto", Theme: "auto", Mode: "tui", MinGainGB: 1}
}

// Path is where the file is, under dir (%APPDATA%).
func Path(dir string) string { return filepath.Join(dir, "wsl-shrinker", "config.toml") }

// Load reads the file at path; a missing file gives the defaults.
func Load(path string) (Config, error) {
	c := Default()
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	return parse(path, b)
}

// parse reads a settings file's text; path names it in the errors.
func parse(path string, b []byte) (Config, error) {
	c := Default()
	md, err := toml.Decode(string(b), &c)
	if err != nil {
		return Default(), fmt.Errorf("%s: %w", path, err)
	}
	// A key this version does not know is a typo more often than not.
	if und := md.Undecoded(); len(und) > 0 {
		return Default(), &KeyError{path, und[0].String(), fmt.Sprintf("unknown setting %q", und[0].String())}
	}
	return c, c.check(path)
}

// KeyError is a setting in the file at Path the program cannot use: Key
// names it, for the fronts to say how to put it right.
type KeyError struct{ Path, Key, Msg string }

// Error says it with the file's path in front, the file the person knows.
func (e *KeyError) Error() string { return e.Path + ": " + e.Msg }

// check rejects values the program cannot use.
func (c Config) check(path string) error {
	bad := func(key, format string, a ...any) error { return &KeyError{path, key, fmt.Sprintf(format, a...)} }
	switch {
	case !oneOf(c.Language, "auto", "en", "ko"):
		return bad("language", "language must be auto, en or ko")
	case !oneOf(c.Theme, "auto", "dark", "light"):
		return bad("theme", "theme must be auto, dark or light")
	case !oneOf(c.Mode, "tui", "cli"):
		return bad("mode", "mode must be tui or cli")
	case c.MinGainGB < 0:
		return bad("min_gain_gb", "min_gain_gb cannot be negative")
	case math.IsNaN(c.MinGainGB) || c.MinGainGB > maxGainGB:
		// Not a number, or more bytes than a count holds: every disk would
		// read as worth shrinking.
		return bad("min_gain_gb", "min_gain_gb must be a number from 0 to %d", maxGainGB)
	}
	for _, p := range c.ExtraDisks {
		if !strings.EqualFold(filepath.Ext(p), ".vhdx") {
			return bad("extra_disks", "extra_disks takes .vhdx files, not %q", p)
		}
		if !windowsAbs(p) {
			return bad("extra_disks", "extra_disks takes full Windows paths (C:\\...\\disk.vhdx), not %q", p)
		}
	}
	return nil
}

// maxGainGB is the largest min_gain_gb: a million GB, far past any disk and
// well inside what a byte count holds.
const maxGainGB = 1_000_000

// windowsAbs reports whether p is a full Windows path: a drive letter, a
// colon and a slash, or a network path. The check is the same on every
// system, as the file is read on Windows: a relative path would depend on
// the folder the program starts in, and a WSL path (/mnt/c/...) is not one
// Windows opens.
func windowsAbs(p string) bool {
	if len(p) >= 3 && (p[0]|0x20 >= 'a' && p[0]|0x20 <= 'z') && p[1] == ':' && (p[2] == '\\' || p[2] == '/') {
		return true
	}
	return strings.HasPrefix(p, `\\`) || strings.HasPrefix(p, "//")
}

// Protected reports whether the disk named name is protected.
func (c Config) Protected(name string) bool {
	for _, p := range c.Protect {
		if strings.EqualFold(p, name) {
			return true
		}
	}
	return false
}

// oneOf reports whether v is one of the choices.
func oneOf(v string, choices ...string) bool {
	for _, c := range choices {
		if v == c {
			return true
		}
	}
	return false
}

// Example is a commented settings file with every key at its default.
const Example = `# wsl-shrinker settings. Every key is optional.

# Language of the screen, setup and CLI mode's disks: "en", "ko", or "auto":
# WSL_SHRINKER_LANG when it is set, then Windows' display language. The other
# commands print English, for scripts.
language = "auto"

# Colors: "auto" asks the terminal, or "dark", "light".
theme = "auto"

# What wsl-shrinker opens in a terminal: "tui", the screen, or "cli", the
# disks as a table with the commands to use. Every command works in both.
mode = "tui"

# A disk that gives back at least this many GB is marked worth shrinking.
min_gain_gb = 1

# More VHDX files to show, for disks of other tools, as full Windows paths.
extra_disks = []

# Disks, by name, that are never shrunk.
protect = []
`
