// Package config reads the settings file, %APPDATA%\wsl-shrinker\config.toml.
// Every setting has a default, so the file is optional.
package config

import (
	"errors"
	"fmt"
	"io/fs"
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
	// MinGainGB is the least a disk must give back, in GB, to be marked as
	// worth shrinking on the dashboard.
	MinGainGB float64 `toml:"min_gain_gb"`
	// ExtraDisks lists more VHDX files to show (disks of other tools).
	ExtraDisks []string `toml:"extra_disks"`
	// Protect lists disks, by name, that the dashboard never shrinks.
	Protect []string `toml:"protect"`
}

// Default is what applies without a file.
func Default() Config {
	return Config{Language: "auto", Theme: "auto", MinGainGB: 1}
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
	md, err := toml.Decode(string(b), &c)
	if err != nil {
		return Default(), fmt.Errorf("%s: %w", path, err)
	}
	// A key this version does not know is a typo more often than not.
	if und := md.Undecoded(); len(und) > 0 {
		return Default(), fmt.Errorf("%s: unknown setting %q", path, und[0].String())
	}
	return c, c.check(path)
}

// check rejects values the program cannot use.
func (c Config) check(path string) error {
	switch {
	case !oneOf(c.Language, "auto", "en", "ko"):
		return fmt.Errorf("%s: language must be auto, en or ko", path)
	case !oneOf(c.Theme, "auto", "dark", "light"):
		return fmt.Errorf("%s: theme must be auto, dark or light", path)
	case c.MinGainGB < 0:
		return fmt.Errorf("%s: min_gain_gb cannot be negative", path)
	}
	for _, p := range c.ExtraDisks {
		if !strings.EqualFold(filepath.Ext(p), ".vhdx") {
			return fmt.Errorf("%s: extra_disks takes .vhdx files, not %q", path, p)
		}
	}
	return nil
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

# Language of the screen: "en", "ko", or "auto": WSL_SHRINKER_LANG when it is
# set, then Windows' display language.
language = "auto"

# Colors: "auto" asks the terminal, or "dark", "light".
theme = "auto"

# A disk that gives back at least this many GB is marked worth shrinking.
min_gain_gb = 1

# More VHDX files to show, for disks of other tools.
extra_disks = []

# Disks, by name, the dashboard never shrinks.
protect = []
`
