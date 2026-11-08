package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// keyLine matches a top-level setting's line, set or commented out:
// "mode = ...", "# mode = ...", and the key in quotes, as TOML allows. The
// key is the third group; the quotes around it, if any, must match.
var keyLine = regexp.MustCompile(`^(#\s*)?(["']?)([a-z_]+)(["']?)\s*=`)

// keys is the order SetKeys adds keys in, the order of Example.
var keys = []string{"language", "theme", "mode", "min_gain_gb", "extra_disks", "protect"}

// Quote is a string as a TOML value; a list of them is QuoteList.
func Quote(s string) string { return strconv.Quote(s) }

// QuoteList is strings as a TOML array, one line.
func QuoteList(ss []string) string {
	q := make([]string, len(ss))
	for i, s := range ss {
		q[i] = Quote(s)
	}
	return "[" + strings.Join(q, ", ") + "]"
}

// InvalidError is a value SetKeys would have written that the program
// refuses to load: a mistake in what was asked, not in writing the file.
type InvalidError struct{ Err error }

func (e *InvalidError) Error() string { return e.Err.Error() }

// Unwrap gives the reason Load gives.
func (e *InvalidError) Unwrap() error { return e.Err }

// SetKeys writes values (TOML values, keyed by setting) into the settings
// file at path, and leaves every other line as it is: notes and settings
// written by hand stay. A key set in the file has its value replaced, over
// as many lines as it took (an array may span several), and keeps the note
// after it on its line; a key only commented out has that line replaced, in
// its place among the notes; any other is added at the end. Without a file,
// Example is written with the values in. The result must load: a value the
// program would refuse is not written, and comes back as an InvalidError.
func SetKeys(path string, values map[string]string) error {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		b, err = []byte(Example), nil
	}
	if err != nil {
		return err
	}
	// A file written on Windows keeps its line endings, new lines included.
	eol := ""
	if strings.Contains(string(b), "\r\n") {
		eol = "\r"
	}
	lines := strings.Split(string(b), "\n")
	// The last line of each key, set or commented out: the set one is the
	// one in effect.
	set, commented := map[string]int{}, map[string]int{}
	for i, l := range lines {
		m := keyLine.FindStringSubmatch(strings.TrimSpace(l))
		// Quotes on one side only make no key.
		if m == nil || m[2] != m[4] {
			continue
		}
		if m[1] == "" {
			set[m[3]] = i
		} else {
			commented[m[3]] = i
		}
	}
	// The new lines, by the line each starts at, and the lines they take
	// the place of; the keys the file has nowhere go at the end.
	replace := map[int]string{}
	drop := map[int]bool{}
	var added []string
	// In a fixed order, so the file comes out the same every time.
	for _, k := range keys {
		v, ok := values[k]
		if !ok {
			continue
		}
		line := k + " = " + v
		if i, ok := set[k]; ok {
			// The whole value goes, and the note after it stays.
			end, note := valueEnd(lines, i)
			if note != "" {
				line += " " + note
			}
			replace[i] = line + eol
			for j := i + 1; j <= end; j++ {
				drop[j] = true
			}
			continue
		}
		if i, ok := commented[k]; ok {
			replace[i] = line + eol
			continue
		}
		added = append(added, line+eol)
	}
	var out []string
	for i, l := range lines {
		switch {
		case drop[i]:
			// The rest of a value that spanned lines.
		case replace[i] != "":
			out = append(out, replace[i])
		default:
			out = append(out, l)
		}
	}
	// New keys at the end, after the last line that holds anything.
	end := len(out)
	for end > 0 && strings.TrimSpace(out[end-1]) == "" {
		end--
	}
	out = append(out[:end], added...)
	text := strings.Join(out, "\n") + "\n"
	// What is written must load, as the program reads it next time; the
	// error names the settings file, the one the person knows.
	if _, err := parse(path, []byte(text)); err != nil {
		return &InvalidError{err}
	}
	tmp := path + ".new"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(tmp, []byte(text), 0o644); err != nil {
		return err
	}
	// Moved over the old one whole, so nothing reads half a file.
	return os.Rename(tmp, path)
}

// valueEnd follows the value of the key set on lines[i], from its "=", to
// the line it ends on, and returns that line with the note (a "# ..."
// comment) after the value there, if any. It follows strings and arrays
// over lines, as TOML writes them; a value it cannot follow (a string over
// several lines, or one never closed) ends on line i, and what is written
// then fails to load and is not written.
func valueEnd(lines []string, i int) (int, string) {
	depth := 0
	// The string the scan is in: '"', '\'' or none.
	var in byte
	escaped := false
	for j := i; j < len(lines); j++ {
		l := strings.TrimSuffix(lines[j], "\r")
		start := 0
		if j == i {
			// The value starts after the key's "=".
			start = strings.Index(l, "=") + 1
		}
		for k := start; k < len(l); k++ {
			c := l[k]
			switch {
			case in == '"' && escaped:
				escaped = false
			case in == '"' && c == '\\':
				escaped = true
			case in != 0 && c == in:
				in = 0
			case in != 0:
				// Inside a string: nothing counts.
			case strings.HasPrefix(l[k:], `"""`) || strings.HasPrefix(l[k:], `'''`):
				// A string over several lines: not followed.
				return i, ""
			case c == '"' || c == '\'':
				in = c
			case c == '[':
				depth++
			case c == ']':
				depth--
			case c == '#' && depth == 0:
				// The note after the value.
				return j, strings.TrimSpace(l[k:])
			case c == '#':
				// A note inside an array: the rest of its line.
				k = len(l)
			}
		}
		// A string ends on its line; one that does not was never closed.
		if in != 0 {
			return i, ""
		}
		if depth <= 0 {
			return j, ""
		}
	}
	// An array never closed.
	return i, ""
}
