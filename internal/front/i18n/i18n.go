// Package i18n holds the words wsl-shrinker shows people, in English and
// Korean. The language follows the settings, then WSL_SHRINKER_LANG, then the
// Windows display language; English is the default.
package i18n

import (
	"fmt"
	"os"
	"strings"
)

// Lang is a language code: "en" or "ko".
type Lang string

const (
	// English is the default.
	English Lang = "en"
	// Korean is used on a Korean Windows.
	Korean Lang = "ko"
)

// current is the language in use.
var current = English

// Choose picks the language: the setting when it is not "auto", then the
// environment variable, then the system's own language.
func Choose(setting string, systemKorean bool) Lang {
	switch setting {
	case "en":
		return English
	case "ko":
		return Korean
	}
	if v := strings.ToLower(os.Getenv("WSL_SHRINKER_LANG")); v != "" {
		if strings.HasPrefix(v, "ko") {
			return Korean
		}
		return English
	}
	if systemKorean {
		return Korean
	}
	return English
}

// Set makes l the language in use.
func Set(l Lang) {
	if l == Korean {
		current = Korean
	} else {
		current = English
	}
}

// Current returns the language in use.
func Current() Lang { return current }

// T returns the text for key in the current language, formatted with args.
// A key missing from Korean falls back to English; a key missing from both
// shows as itself, which the catalog test turns into a failure.
func T(key string, args ...any) string {
	s, ok := catalogs[current][key]
	if !ok {
		s, ok = catalogs[English][key]
	}
	if !ok {
		s = key
	}
	if len(args) == 0 {
		return s
	}
	return fmt.Sprintf(s, args...)
}

// TN is T for a count n: English has a ".one" form for n == 1.
func TN(key string, n int, args ...any) string {
	if n == 1 {
		if _, ok := catalogs[current][key+".one"]; ok {
			return T(key+".one", args...)
		}
	}
	return T(key, args...)
}

// catalogs maps each language to its words.
var catalogs = map[Lang]map[string]string{English: en, Korean: ko}

// Has reports whether a language's catalog holds key (for the tests).
func Has(l Lang, key string) bool {
	_, ok := catalogs[l][key]
	return ok
}
