package config

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestSetKeysStartsFromTheTemplate(t *testing.T) {
	// No file yet: the template with every key, the chosen ones in place.
	p := filepath.Join(t.TempDir(), "wsl-shrinker", "config.toml")
	if err := SetKeys(p, map[string]string{"mode": Quote("cli"), "protect": QuoteList([]string{"Work", "a\\b"})}); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil || c.Mode != "cli" || !slices.Equal(c.Protect, []string{"Work", "a\\b"}) || c.Language != "auto" {
		t.Fatalf("%+v %v", c, err)
	}
	b, _ := os.ReadFile(p)
	// The notes of the template are there, each key once.
	if !strings.Contains(string(b), "# What wsl-shrinker opens in a terminal") || strings.Count(string(b), "mode =") != 1 {
		t.Fatalf("\n%s", b)
	}
}

func TestSetKeysLeavesTheRestAsWritten(t *testing.T) {
	// A file written by hand on Windows: its notes, its other keys and its
	// line endings stay; a key only commented out is set in its place.
	p := filepath.Join(t.TempDir(), "config.toml")
	hand := "# mine\r\nlanguage = \"ko\" # kept\r\n# theme = \"dark\"\r\nextra_disks = ['D:\\vm\\a.vhdx']\r\n"
	if err := os.WriteFile(p, []byte(hand), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetKeys(p, map[string]string{"theme": Quote("light"), "min_gain_gb": "5", "language": Quote("en")}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	// The note after a value stays with it, and a new key ends its line as
	// the file's other lines do.
	want := "# mine\r\nlanguage = \"en\" # kept\r\ntheme = \"light\"\r\nextra_disks = ['D:\\vm\\a.vhdx']\r\nmin_gain_gb = 5\r\n"
	if string(b) != want {
		t.Fatalf("got  %q\nwant %q", b, want)
	}
	if c, err := Load(p); err != nil || c.Theme != "light" || c.MinGainGB != 5 || c.Language != "en" {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestSetKeysReplacesAValueOverSeveralLines(t *testing.T) {
	// An array written over several lines, notes inside, is replaced whole:
	// leaving its other lines would make a file that does not load.
	p := filepath.Join(t.TempDir(), "config.toml")
	hand := "protect = [\n  \"Work\", # mine\n  'Old',\n  \"a]b\",\n] # the ones to keep\nmode = \"tui\"\n"
	if err := os.WriteFile(p, []byte(hand), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetKeys(p, map[string]string{"protect": QuoteList([]string{"New"})}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "protect = [\"New\"] # the ones to keep\nmode = \"tui\"\n" {
		t.Fatalf("%q", b)
	}
}

func TestSetKeysKnowsAKeyInQuotes(t *testing.T) {
	// TOML lets a key stand in quotes: it is the same key, set in its place,
	// not a second one the program would refuse.
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte("\"mode\" = \"tui\"\n'theme' = \"dark\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetKeys(p, map[string]string{"mode": Quote("cli"), "theme": Quote("light")}); err != nil {
		t.Fatal(err)
	}
	if c, err := Load(p); err != nil || c.Mode != "cli" || c.Theme != "light" {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestSetKeysWritesNothingTheProgramWouldRefuse(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte("mode = \"tui\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := SetKeys(p, map[string]string{"mode": Quote("both")})
	// A value refused is told apart from a file that could not be written.
	var invalid *InvalidError
	if !errors.As(err, &invalid) {
		t.Fatalf("not an InvalidError: %v", err)
	}
	// The error names the settings file, not a file of the program's own.
	if !strings.HasPrefix(err.Error(), p+": ") || strings.Contains(err.Error(), ".new") {
		t.Fatalf("%v", err)
	}
	if b, _ := os.ReadFile(p); string(b) != "mode = \"tui\"\n" {
		t.Fatalf("the file changed: %q", b)
	}
	// Nothing is left beside it.
	if _, err := os.Stat(p + ".new"); !os.IsNotExist(err) {
		t.Fatalf("a file was left beside the settings: %v", err)
	}
}
