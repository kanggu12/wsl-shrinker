package wsl

import (
	"testing"
	"unicode/utf16"
)

// utf16le encodes s the way wsl.exe prints its own messages.
func utf16le(s string) []byte {
	var b []byte
	for _, u := range utf16.Encode([]rune(s)) {
		b = append(b, byte(u), byte(u>>8))
	}
	return b
}

func TestDecode(t *testing.T) {
	// wsl.exe's own output, as captured on Windows 11 with WSL 3.0.3.
	if got := decode(utf16le("Ubuntu-22.04\r\n")); got != "Ubuntu-22.04\r\n" {
		t.Fatalf("UTF-16: %q", got)
	}
	// A byte order mark is dropped.
	if got := decode(utf16le("\ufeffx\r\n")); got != "x\r\n" {
		t.Fatalf("BOM: %q", got)
	}
	// An older wsl.exe prints UTF-16 without a mark; a first name outside
	// Latin-1 must not make it read as UTF-8.
	if got := decode(utf16le("우분투\r\nUbuntu\r\n")); got != "우분투\r\nUbuntu\r\n" {
		t.Fatalf("no mark, Korean first: %q", got)
	}
	// Output from inside a distribution is UTF-8, Korean included.
	if got := decode([]byte("디스크 ok\n")); got != "디스크 ok\n" {
		t.Fatalf("UTF-8: %q", got)
	}
}

func TestParseVersion(t *testing.T) {
	// The first lines of `wsl --version` on this PC, and a translated one.
	for in, want := range map[string]string{
		"WSL version: 3.0.3.0\r\nKernel version: 6.18.54.1-1\r\n": "3.0.3.0",
		"WSL 버전: 2.6.1.0\r\n커널 버전: 6.6.87.2-1\r\n":                "2.6.1.0",
		"nothing here": "",
	} {
		if got := parseVersion(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

func TestLinesAndCompact(t *testing.T) {
	if got := lines("a\r\n\r\n  b \n"); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("%q", got)
	}
	// The option as `wsl --help` lists it on 3.0.3.
	if !hasCompact("            --compact\r\n                Compact the VHDX file") || hasCompact("--resize") {
		t.Fatal("compact detection")
	}
}
