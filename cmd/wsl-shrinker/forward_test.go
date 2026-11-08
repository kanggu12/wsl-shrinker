//go:build !windows

package main

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/kanggu12/wsl-shrinker/internal/app/remote"
)

func TestWSLENV(t *testing.T) {
	// Windows Terminal sets WSLENV already; the entry is added once.
	if got := appendWSLENV("WT_SESSION:WT_PROFILE_ID:", "WSL_DISTRO_NAME/w"); got != "WT_SESSION:WT_PROFILE_ID::WSL_DISTRO_NAME/w" {
		t.Fatalf("%q", got)
	}
	if got := appendWSLENV("WSL_DISTRO_NAME/w", "WSL_DISTRO_NAME/w"); got != "WSL_DISTRO_NAME/w" {
		t.Fatalf("%q", got)
	}
	// The environment holds one WSLENV, the new one.
	env := withWSLENV([]string{"A=1", "WSLENV=old", "B=2"}, "new")
	n := 0
	for _, e := range env {
		if len(e) > 7 && e[:7] == "WSLENV=" {
			n++
		}
	}
	if n != 1 || !slices.Contains(env, "WSLENV=new") || !slices.Contains(env, "B=2") {
		t.Fatalf("%v", env)
	}
}

func TestHelpAndVersionNeedNoWindows(t *testing.T) {
	// Without wsl-shrinker.exe these still answer; anything else needs it.
	for args, want := range map[string]bool{"version": true, "help": true, "--help": true, "shrink -h": true,
		"list": false, "": false, "shrink Ubuntu": false} {
		var a []string
		if args != "" {
			a = strings.Fields(args)
		}
		if got := answersAlone(a); got != want {
			t.Errorf("%q: %v", args, got)
		}
	}
}

func TestOnlyTheScreenRunsHere(t *testing.T) {
	// The screen alone, with the flags it takes, runs in the Linux terminal;
	// everything else goes to the .exe, which says what is wrong with it.
	for args, want := range map[string]bool{"": true, "--lang ko": true, "--theme=light": true, "--lang en --theme dark": true,
		"tui": true, "tui --theme light": true, "tui list": false, "setup": false,
		"list": false, "--demo": false, "--lang fr": false, "--theme": false, "--run Ubuntu": false, "shrink Ubuntu --yes": false} {
		var a []string
		if args != "" {
			a = strings.Fields(args)
		}
		if _, got := screenOnly(a); got != want {
			t.Errorf("%q: %v", args, got)
		}
	}
	if s, _ := screenOnly([]string{"--theme", "light", "--lang=ko"}); s.lang != "ko" || s.theme != "light" || s.tui {
		t.Fatalf("%+v", s)
	}
	// tui is the screen whatever the mode in the settings.
	if s, _ := screenOnly([]string{"tui", "--lang", "en"}); !s.tui || s.lang != "en" {
		t.Fatalf("%+v", s)
	}
}

// fakeExe writes the smallest header that reads as a Windows program for
// machine: "MZ", the PE header's place at 0x3c, "PE\0\0" there, then the
// machine type. Like every fake program here, it has no execute bit: a
// mistake could not start it.
func fakeExe(t *testing.T, dir, name string, machine uint16) string {
	t.Helper()
	p := filepath.Join(dir, name)
	b := make([]byte, 0x50)
	copy(b, "MZ")
	binary.LittleEndian.PutUint32(b[0x3c:], 0x40)
	copy(b[0x40:], "PE\x00\x00")
	binary.LittleEndian.PutUint16(b[0x44:], machine)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// onHost makes the header check see a PC of machine type host, whose
// Windows says build (ok false: it could not be read); asked counts how
// often it was asked.
func onHost(t *testing.T, host uint16, build int, ok bool) (asked *int) {
	t.Helper()
	h, w := hostMachine, windowsBuild
	t.Cleanup(func() { hostMachine, windowsBuild = h, w })
	asked = new(int)
	hostMachine = func() uint16 { return host }
	windowsBuild = func() (int, bool) { *asked++; return build, ok }
	return asked
}

func TestAProgramForAnotherProcessorIsNeverStarted(t *testing.T) {
	// Windows would stop it with a dialog on the desktop: the launcher reads
	// the header first and says why in the terminal.
	dir := t.TempDir()
	arm, x64, x86 := fakeExe(t, dir, "arm.exe", machineARM64), fakeExe(t, dir, "x64.exe", machineAMD64), fakeExe(t, dir, "x86.exe", machineI386)
	if m, ok := peMachine(arm); !ok || m != machineARM64 {
		t.Fatalf("read %#x %v", m, ok)
	}
	// An x64 PC runs x64 and 32-bit x86 programs, never ARM64 ones; nothing
	// asks Windows which it is.
	asked := onHost(t, machineAMD64, 0, false)
	if why := wrongBuild(arm); !strings.Contains(why, "built for ARM64 Windows; this PC runs x64 Windows") {
		t.Fatalf("%q", why)
	}
	if wrongBuild(x64) != "" || wrongBuild(x86) != "" || *asked != 0 {
		t.Fatalf("x64 or x86 refused, or Windows asked %d times", *asked)
	}
	// An ARM64 PC runs its own and x86 programs without asking; x64 ones
	// from Windows 11 on ARM, which only its build tells apart.
	asked = onHost(t, machineARM64, 19045, true)
	if wrongBuild(arm) != "" || wrongBuild(x86) != "" || *asked != 0 {
		t.Fatalf("ARM64 or x86 refused, or Windows asked %d times", *asked)
	}
	if why := wrongBuild(x64); !strings.Contains(why, "built for x64 Windows; this PC runs Windows 10 on ARM") || *asked != 1 {
		t.Fatalf("%q, asked %d", why, *asked)
	}
	onHost(t, machineARM64, 22631, true)
	if why := wrongBuild(x64); why != "" {
		t.Fatalf("Windows 11 on ARM: %q", why)
	}
	// A Windows that does not say is let through: Windows answers then.
	onHost(t, machineARM64, 0, false)
	if why := wrongBuild(x64); why != "" {
		t.Fatalf("unknown build: %q", why)
	}
	// 32-bit Arm ran on ARM64 Windows until a Windows 11 build stopped it.
	arm32 := fakeExe(t, dir, "arm32.exe", machineARMNT)
	onHost(t, machineARM64, 26100, true)
	if why := wrongBuild(arm32); !strings.Contains(why, "built for 32-bit Arm Windows; this PC runs a Windows on ARM that no longer runs 32-bit Arm programs") {
		t.Fatalf("%q", why)
	}
	onHost(t, machineARM64, 19045, true)
	if why := wrongBuild(arm32); why != "" {
		t.Fatalf("Windows 10 on ARM: %q", why)
	}
	// What runs where, as Windows has it.
	at := func(b int) func() (int, bool) { return func() (int, bool) { return b, true } }
	for _, c := range []struct {
		program, host uint16
		build         int
		runs          bool
	}{
		{machineAMD64, machineAMD64, 0, true}, {machineI386, machineAMD64, 0, true}, {machineARM64, machineAMD64, 0, false},
		{machineARM64, machineARM64, 0, true}, {machineI386, machineARM64, 0, true}, {0x0200, machineARM64, 0, false},
		{machineARMNT, machineARM64, 22631, true}, {machineARMNT, machineARM64, firstWithoutARM32, false},
		{machineAMD64, machineARM64, firstX64Emulation - 1, false}, {machineAMD64, machineARM64, firstX64Emulation, true},
		{machineARM64, 0, 0, true},
	} {
		if got := runsOn(c.program, c.host, at(c.build)); got != c.runs {
			t.Errorf("%#x on %#x, build %d: %v", c.program, c.host, c.build, got)
		}
	}
	// A file that is no Windows program is let through: the header does not
	// say, and Windows itself answers.
	text := filepath.Join(dir, "text.exe")
	if err := os.WriteFile(text, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if why := wrongBuild(text); why != "" {
		t.Fatalf("%q", why)
	}
}

func TestTheWindowsBuildIsReadFromVer(t *testing.T) {
	// ver's line as cmd.exe prints it, the same in Korean Windows: "Version"
	// and a blank line before it, the build third.
	for out, want := range map[string]int{
		"\r\nMicrosoft Windows [Version 10.0.22631.4317]\r\n": 22631,
		"\r\nMicrosoft Windows [Version 10.0.19045.3803]\r\n": 19045,
	} {
		if got, ok := parseVer(out); !ok || got != want {
			t.Errorf("%q: %d %v", out, got, ok)
		}
	}
	if _, ok := parseVer("'cmd.exe' is not recognized"); ok {
		t.Fatal("read a build from nothing")
	}
	// This PC's own processor is one the check knows.
	if m := unameMachine(); m != machineAMD64 && m != machineARM64 && m != 0 {
		t.Fatalf("%#x", m)
	}
}

// stopped is how a test's exit ends forward: with the code it was given.
type stopped int

// launch runs forward on args with the .exe at exe, with nothing reaching
// the PC: it returns the exit code (-1 when forward returned) and what
// would have started.
func launch(t *testing.T, exe string, terminal bool, args ...string) (code int, started []string) {
	t.Helper()
	t.Setenv("WSL_DISTRO_NAME", "Test")
	saved := []any{os.Args, locate, execve, exit, inTerminal, openRemote, runChild, watch, finish}
	t.Cleanup(func() {
		os.Args = saved[0].([]string)
		locate, execve, exit = saved[1].(func() string), saved[2].(func(string, []string, []string) error), saved[3].(func(int))
		inTerminal = saved[4].(func() bool)
		openRemote = saved[5].(func(context.Context, remote.Runner, string, string) (*remote.Backend, error))
		runChild = saved[6].(func(*exec.Cmd) error)
		watch, finish = saved[7].(func() func()), saved[8].(func())
	})
	// The screen here would keep its crashes in this PC's cache folder.
	watch = func() func() {
		started = append(started, "watch")
		return func() {}
	}
	os.Args = append([]string{"wsl-shrinker"}, args...)
	locate = func() string { return exe }
	inTerminal = func() bool { return terminal }
	execve = func(argv0 string, _ []string, _ []string) error {
		started = append(started, "exec "+argv0)
		return errors.New("not started in a test")
	}
	openRemote = func(context.Context, remote.Runner, string, string) (*remote.Backend, error) {
		started = append(started, "pipes")
		return nil, errors.New("not started in a test")
	}
	runChild = func(c *exec.Cmd) error {
		started = append(started, "child "+c.Path)
		return errors.New("not started in a test")
	}
	exit = func(c int) { panic(stopped(c)) }
	defer func() {
		if r := recover(); r != nil {
			c, ok := r.(stopped)
			if !ok {
				panic(r)
			}
			code = int(c)
		}
	}()
	forward()
	return -1, started
}

func TestTheLauncherStartsNothingBeforeItReadTheHeader(t *testing.T) {
	// The order that keeps the dialog away: the header is read before
	// anything can start the .exe, by pipes, as a child or in its place.
	dir := t.TempDir()
	// An x64 PC with an ARM64 build beside it, and the right one.
	onHost(t, machineAMD64, 0, false)
	wrong, right := fakeExe(t, dir, "arm.exe", machineARM64), fakeExe(t, dir, "x64.exe", machineAMD64)
	for _, terminal := range []bool{false, true} {
		for _, args := range [][]string{nil, {"tui"}, {"list"}, {"setup"}} {
			code, started := launch(t, wrong, terminal, args...)
			if code != 1 || len(started) != 0 {
				t.Errorf("%v in a terminal %v: exit %d, started %v", args, terminal, code, started)
			}
		}
	}
	// The version is answered here, the .exe untouched.
	if code, started := launch(t, wrong, false, "version"); code != -1 || len(started) != 0 {
		t.Fatalf("version: exit %d, started %v", code, started)
	}
	// The right build goes on: the screen asks it through pipes, anything
	// else starts it in this process's place.
	if _, started := launch(t, right, true); strings.Join(started, " ") != "pipes exec "+right {
		t.Fatalf("started %v", started)
	}
	if _, started := launch(t, right, false, "list"); strings.Join(started, " ") != "exec "+right {
		t.Fatalf("started %v", started)
	}
}

func TestWhatAStartOpens(t *testing.T) {
	// Setup first on a first start, then the screen here unless CLI mode
	// prints instead; tui is the screen, with no setup, whatever the mode.
	for _, c := range []struct {
		s           screenArgs
		st          remote.Settings
		setup, here bool
	}{
		{screenArgs{}, remote.Settings{Mode: "tui"}, false, true},
		{screenArgs{}, remote.Settings{Mode: "cli"}, false, false},
		{screenArgs{}, remote.Settings{Mode: "tui", First: true}, true, true},
		{screenArgs{tui: true}, remote.Settings{Mode: "cli", First: true}, false, true},
	} {
		if setup, here := startAs(c.s, c.st); setup != c.setup || here != c.here {
			t.Errorf("%+v %+v: setup %v here %v", c.s, c.st, setup, here)
		}
	}
}

func TestSetupEndedBySignalIsAPlainFailure(t *testing.T) {
	// The exit code of setup's child, as the launcher passes it on: its own,
	// or 1 when a signal ended it, never the 255 that -1 would become.
	saved := runChild
	t.Cleanup(func() { runChild = saved })
	for script, want := range map[string]int{"exit 0": 0, "exit 3": 3, "kill -INT $$": 1, "kill -HUP $$": 1} {
		runChild = func(*exec.Cmd) error { return exec.Command("sh", "-c", script).Run() }
		if got := setupFirst("wsl-shrinker.exe", nil, ""); got != want {
			t.Errorf("%s: %d, want %d", script, got, want)
		}
	}
}

func TestAWindowsProgramThatCannotStartSaysWhy(t *testing.T) {
	// With WSL's interop off, starting the .exe fails with "exec format
	// error": the hint says how to turn it on. Other errors have none.
	err := &os.PathError{Op: "fork/exec", Path: "/mnt/c/x/wsl-shrinker.exe", Err: syscall.ENOEXEC}
	if h := startHint(err); !strings.Contains(h, "[interop]") || !strings.Contains(h, "wsl --shutdown") {
		t.Fatalf("%q", h)
	}
	if h := startHint(&os.PathError{Op: "fork/exec", Path: "x", Err: syscall.EACCES}); h != "" {
		t.Fatalf("%q", h)
	}
}
