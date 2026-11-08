package wsl

// The files in testdata are wsl.exe's own output, recorded once with WSL
// 3.0.3.0 on Windows 11 in English, stdout redirected to a file so the
// bytes are exact:
//
//	wsl.exe --list --quiet                  > list-quiet.utf16le
//	wsl.exe --list --running --quiet        > list-running-quiet.utf16le
//	wsl.exe --version                       > version.utf16le
//	wsl.exe --help                          > help.utf16le  (exits 255)
//
// and each again with WSL_UTF8=1 (WSLENV=WSL_UTF8 from WSL, as wslExe sets
// it), saved as *.utf8. Without it wsl.exe prints UTF-16LE with no byte
// order mark; with it, UTF-8 with no mark; both end lines with CRLF. The
// recorded distribution name was replaced with Ubuntu-24.04, and the list
// given two more lines (Debian, Work) in the recorded line's exact form.
// The failing form of --list --running (none running) was not recorded:
// producing it would mean stopping a distribution.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// encodings are the two forms wsl.exe prints: without and with WSL_UTF8.
var encodings = []string{"utf16le", "utf8"}

// recorded reads one recorded output, by command and encoding.
func recorded(t *testing.T, name, enc string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name+"."+enc))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The made-up registration the recorded lists describe.
var (
	// listed is what --list --quiet names, in its order.
	listed = []string{"Ubuntu-24.04", "Debian", "Work"}
	// runningNow is what --list --running --quiet names.
	runningNow = []string{"Ubuntu-24.04"}
)

func TestRecordedOutputDecodes(t *testing.T) {
	for _, name := range []string{"list-quiet", "list-running-quiet", "version", "help"} {
		// The UTF-16LE and the UTF-8 recording hold the same text.
		wide, narrow := recorded(t, name, "utf16le"), recorded(t, name, "utf8")
		// The UTF-16 one has no byte order mark: the zero high bytes tell it.
		if wide[0] == 0xff && wide[1] == 0xfe {
			t.Errorf("%s: recorded with a byte order mark; the comment above says none", name)
		}
		if got := decode(wide); got != string(narrow) {
			t.Errorf("%s: UTF-16LE decodes to %q, the UTF-8 recording is %q", name, got, narrow)
		}
		// UTF-8 output passes through, not misread as UTF-16 for its even length.
		if got := decode(narrow); got != string(narrow) {
			t.Errorf("%s: UTF-8 changed by decode: %q", name, got)
		}
	}
}

func TestRecordedListsSplitIntoNames(t *testing.T) {
	for _, enc := range encodings {
		// Every line is a name, with the CRLF and nothing else stripped.
		if got := lines(decode(recorded(t, "list-quiet", enc))); !slices.Equal(got, listed) {
			t.Errorf("%s --list --quiet: %q", enc, got)
		}
		if got := lines(decode(recorded(t, "list-running-quiet", enc))); !slices.Equal(got, runningNow) {
			t.Errorf("%s --list --running --quiet: %q", enc, got)
		}
	}
}

func TestRecordedVersionAndHelp(t *testing.T) {
	for _, enc := range encodings {
		// The package version, not the kernel's or WSLg's on later lines.
		if got := parseVersion(decode(recorded(t, "version", enc))); got != "3.0.3.0" {
			t.Errorf("%s --version: %q", enc, got)
		}
		// 3.0.3 lists --compact under --manage.
		help := decode(recorded(t, "help", enc))
		if !hasCompact(help) {
			t.Errorf("%s --help: --compact not found", enc)
		}
		// The check is on the option, not a word any help would hold.
		if hasCompact(strings.ReplaceAll(help, "--compact", "--resize")) {
			t.Errorf("%s --help: found without the option", enc)
		}
	}
}

func TestRecordedRunningNames(t *testing.T) {
	for _, enc := range encodings {
		run := decode(recorded(t, "list-running-quiet", enc))
		all := decode(recorded(t, "list-quiet", enc))
		// A running list that worked is the answer; names fold to lower case.
		got, err := runningNames(run, nil, "", nil)
		if err != nil || len(got) != 1 || !got["ubuntu-24.04"] {
			t.Errorf("%s: %v %v", enc, got, err)
		}
		// A failed running list with a working full list: none runs.
		got, err = runningNames("", errors.New("exit status 1"), all, nil)
		if err != nil || len(got) != 0 {
			t.Errorf("%s, none running: %v %v", enc, got, err)
		}
	}
}

// call is one wsl.exe call a scripted runner answers.
type call struct {
	// out is the decoded output it prints.
	out string
	// err is its failure, if it fails.
	err error
}

// scripted answers wsl.exe calls by their arguments and notes each one.
type scripted struct {
	// answers maps the joined arguments to what the call gives.
	answers map[string]call
	// asked lists the calls made, in order.
	asked []string
}

// run is the wslRunner over the scripted answers; an unscripted call fails
// the test through its error rather than reading as an answer.
func (s *scripted) run(_ context.Context, args ...string) (string, error) {
	key := strings.Join(args, " ")
	s.asked = append(s.asked, key)
	a, ok := s.answers[key]
	if !ok {
		return "", errors.New("unscripted call: " + key)
	}
	return a.out, a.err
}

// The two list calls Distros makes, as the runner sees them.
const (
	listRunning = "--list --running --quiet"
	listAll     = "--list --quiet"
)

// registration is the registry the recorded lists go with: the three
// names (one in another case than wsl.exe prints), a WSL 1 one, a store
// install with the \\?\ prefix, a custom disk file name, and a leftover.
// Flags are as WSL writes them: 0xf for WSL 2, 0x7 for WSL 1.
func registration() []regEntry {
	return []regEntry{
		{id: "{a}", name: "ubuntu-24.04", base: `\\?\C:\Distros\Ubuntu`, flags: 0xf},
		{id: "{b}", name: "Debian", base: `C:\Distros\Debian`, file: "disk.vhdx", flags: 0xf},
		{id: "{c}", name: "Work", base: `C:\Distros\Work`, flags: 0x7},
		// A failed install leaves a key without a name.
		{id: "{d}", base: `C:\Distros\Broken`, flags: 0xf},
	}
}

func TestBuildDistrosFromRecordedLists(t *testing.T) {
	for _, enc := range encodings {
		s := &scripted{answers: map[string]call{
			listRunning: {out: decode(recorded(t, "list-running-quiet", enc))},
		}}
		got, err := buildDistros(context.Background(), registration(), "{b}", s.run)
		if err != nil {
			t.Fatalf("%s: %v", enc, err)
		}
		// A running list that worked needs no full list.
		if !slices.Equal(s.asked, []string{listRunning}) {
			t.Errorf("%s: asked %q", enc, s.asked)
		}
		want := []Distro{
			// Matched to wsl.exe's spelling without case; the prefix goes.
			{Name: "ubuntu-24.04", ID: "{a}", VHD: filepath.Join(`C:\Distros\Ubuntu`, "ext4.vhdx"), Version: 2, Running: true},
			// The registered file name wins over ext4.vhdx.
			{Name: "Debian", ID: "{b}", VHD: filepath.Join(`C:\Distros\Debian`, "disk.vhdx"), Version: 2, Default: true},
			// WSL 1 has no disk file.
			{Name: "Work", ID: "{c}", Version: 1},
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s:\n got %+v\nwant %+v", enc, got, want)
		}
	}
}

func TestBuildDistrosWhenTheRunningListFails(t *testing.T) {
	boom := errors.New("exit status 1")
	all := decode(recorded(t, "list-quiet", "utf8"))
	// The running list fails and the full list answers: none runs.
	s := &scripted{answers: map[string]call{listRunning: {err: boom}, listAll: {out: all}}}
	got, err := buildDistros(context.Background(), registration(), "", s.run)
	if err != nil || len(got) != 3 {
		t.Fatalf("none running: %+v %v", got, err)
	}
	for _, d := range got {
		if d.Running {
			t.Errorf("%s read as running", d.Name)
		}
	}
	// The full list is asked for after, and only because, the running one failed.
	if !slices.Equal(s.asked, []string{listRunning, listAll}) {
		t.Errorf("asked %q", s.asked)
	}
	// Both fail: WSL did not answer, an error, never "all stopped".
	s = &scripted{answers: map[string]call{listRunning: {err: boom}, listAll: {err: boom}}}
	if got, err := buildDistros(context.Background(), registration(), "", s.run); err == nil {
		t.Fatalf("WSL not answering read as %+v", got)
	}
	// The full list answers with nothing: the failure is not explained either.
	s = &scripted{answers: map[string]call{listRunning: {err: boom}, listAll: {out: "\r\n"}}}
	if got, err := buildDistros(context.Background(), registration(), "", s.run); err == nil {
		t.Fatalf("an empty full list read as %+v", got)
	}
}

func TestBuildDistrosWithNothingNamedAsksNothing(t *testing.T) {
	for _, entries := range [][]regEntry{nil, {{id: "{d}", flags: 0xf}}} {
		s := &scripted{}
		got, err := buildDistros(context.Background(), entries, "", s.run)
		// Nothing to ask about: no list, no error, and wsl.exe is not run.
		if got != nil || err != nil || len(s.asked) != 0 {
			t.Errorf("%d entries: %+v %v, asked %q", len(entries), got, err, s.asked)
		}
	}
}

func TestWSL2IsTheVMModeFlagNotTheVersionValue(t *testing.T) {
	// WSL decides WSL 1 or 2 by the VM mode bit of Flags. A distribution
	// whose Flags lack it is WSL 1, with no disk file, however its other
	// values read; one with no Flags value at all takes WSL's default
	// flags, which lack it too.
	s := &scripted{answers: map[string]call{listRunning: {out: "\r\n"}}}
	got, err := buildDistros(context.Background(), []regEntry{
		{id: "{1}", name: "Old", base: `C:\Distros\Old`, flags: 0x7},
		{id: "{2}", name: "New", base: `C:\Distros\New`, flags: 0xf},
		{id: "{3}", name: "Bare", base: `C:\Distros\Bare`},
		// Interop and the Windows PATH turned off: still WSL 2.
		{id: "{4}", name: "Quiet", base: `C:\Distros\Quiet`, flags: 0x8},
		// Flags that could not be read: Version says, as it is all there is.
		{id: "{5}", name: "Odd", base: `C:\Distros\Odd`, flagsUnread: true, version: 2},
	}, "", s.run)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"Old": 1, "New": 2, "Bare": 1, "Quiet": 2, "Odd": 2}
	for _, d := range got {
		if d.Version != want[d.Name] || (d.Version == 2) != (d.VHD != "") {
			t.Errorf("%s: version %d, disk %q", d.Name, d.Version, d.VHD)
		}
	}
}
