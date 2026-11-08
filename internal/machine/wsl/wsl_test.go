package wsl

import (
	"context"
	"errors"
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
	if got := decode(utf16le("Ubuntu-24.04\r\n")); got != "Ubuntu-24.04\r\n" {
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
	// The first lines of `wsl --version` as WSL prints them, and a translated one.
	for in, want := range map[string]string{
		"WSL version: 3.0.3.0\r\nKernel version: 6.6.87.2-1\r\n": "3.0.3.0",
		"WSL 버전: 2.6.1.0\r\n커널 버전: 6.6.87.2-1\r\n":               "2.6.1.0",
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

func TestUnknownNamesAreRefusedAndCaseIsIgnored(t *testing.T) {
	ds := []Distro{{Name: "Ubuntu-24.04", Running: true}}
	// wsl.exe ignores case in names: so does the lookup.
	if d, err := find(ds, "ubuntu-24.04"); err != nil || d.Name != "Ubuntu-24.04" {
		t.Fatalf("%+v %v", d, err)
	}
	// A name WSL does not have is told apart from other failures.
	if _, err := find(ds, "nosuch"); !errors.Is(err, ErrNoDistro) {
		t.Fatalf("got %v", err)
	}
	// The fake refuses it the same way, before running anything.
	f := &Fake{List: ds}
	if _, err := f.Run(context.Background(), "nosuch", "root", "true"); !errors.Is(err, ErrNoDistro) {
		t.Fatalf("fake: %v", err)
	}
	if _, err := f.Run(context.Background(), "UBUNTU-24.04", "root", "true"); err != nil {
		t.Fatalf("fake, other case: %v", err)
	}
}

func TestErrorsLeaveTheScriptOut(t *testing.T) {
	// An error names the call, not the tens of lines of script it ran.
	got := describe([]string{"--distribution", "Dev", "--user", "root", "--exec", "sh", "-c", "line one\nline two"})
	if got != "--distribution Dev --user root --exec sh -c <script>" {
		t.Fatalf("got %q", got)
	}
}

func TestRunningListFailureIsNotAllStopped(t *testing.T) {
	boom := errors.New("exit status 1")
	// Listing the running ones fails, listing all works: none runs.
	if got, err := runningNames("", boom, "Dev\r\nWork\r\n", nil); err != nil || len(got) != 0 {
		t.Fatalf("none running: %v %v", got, err)
	}
	// Both fail: WSL did not answer, which is an error, not "all stopped".
	if _, err := runningNames("", boom, "", boom); err == nil {
		t.Fatal("a WSL that did not answer read as every distribution stopped")
	}
	// Running names are matched without case.
	if got, err := runningNames("Dev\r\n", nil, "", nil); err != nil || !got["dev"] {
		t.Fatalf("%v %v", got, err)
	}
}

func TestACallThatRanOutOfTimeIsNoAnswer(t *testing.T) {
	// "--list --running" fails both when none runs and when WSL did not
	// answer in time: only the first is "none running".
	if _, err := runningNames("", context.DeadlineExceeded, "Dev\n", nil); err == nil {
		t.Fatal("a timeout was taken for none running")
	}
	if names, err := runningNames("", errors.New("exit status 1"), "Dev\n", nil); err != nil || len(names) != 0 {
		t.Fatalf("%v %v", names, err)
	}
}
