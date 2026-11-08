//go:build windows

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// TestMain lets this test binary stand in for the program: started with
// WSS_E2E_MAIN=1 it runs main instead of the tests. Windows test runs have
// no Go toolchain to build the program, and this way they need none.
func TestMain(m *testing.M) {
	if os.Getenv("WSS_E2E_MAIN") == "1" {
		main()
		return
	}
	os.Exit(m.Run())
}

func TestScreenInAConsole(t *testing.T) {
	// The screen as it runs in a Windows Terminal tab: in a pseudoconsole,
	// the headless console a tab is made of, so no window opens. It draws,
	// redraws when the console changes size, and q ends it with code 0.
	// What the program writes (its crash file) goes to the test's folder.
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("LOCALAPPDATA", dir)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := startConsole(t, 100, 30, windows.ComposeCommandLine([]string{exe, "--demo", "--lang", "en"}))
	c.waitFor(t, "can be given back now", 30*time.Second)
	// A smaller window: the screen draws itself again for it.
	before := c.size()
	c.resize(t, 80, 24)
	c.waitForGrowth(t, before, 10*time.Second)
	c.write(t, "q")
	if code := c.wait(t, 30*time.Second); code != 0 {
		t.Fatalf("exit %d; it drew:\n%s", code, c.text())
	}
	// A clean end leaves no crash file: the runtime let go of it first.
	if left, _ := filepath.Glob(filepath.Join(dir, "wsl-shrinker", "crash-*.txt")); len(left) != 0 {
		t.Fatalf("left %v", left)
	}
}

func TestSetupInAConsole(t *testing.T) {
	// The setup screen as a first start or the installer opens it in a
	// console: it draws, → shows the next language at once, and enter
	// writes the settings file. The settings go to the test's own folder.
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("LOCALAPPDATA", dir)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := startConsole(t, 100, 30, windows.ComposeCommandLine([]string{exe, "setup", "--lang", "en"}))
	c.waitFor(t, "First run.", 30*time.Second)
	// → on the language row: from English to Korean, shown at once.
	c.write(t, "\x1b[C")
	c.waitFor(t, "처음 실행", 10*time.Second)
	c.write(t, "\r")
	if code := c.wait(t, 30*time.Second); code != 0 {
		t.Fatalf("exit %d; it drew:\n%s", code, c.text())
	}
	b, err := os.ReadFile(filepath.Join(dir, "wsl-shrinker", "config.toml"))
	if err != nil || !strings.Contains(string(b), "language = \"ko\"") || !strings.Contains(string(b), "mode = \"tui\"") {
		t.Fatalf("%v\n%s", err, b)
	}
}

func TestAnErrorInAConsoleSaysWhatToDo(t *testing.T) {
	// A mistake in a console reads as a red cross and what to do under it,
	// not a bare line; exit 2 as for any mistake in how it was called.
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("LOCALAPPDATA", dir)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := startConsole(t, 100, 30, windows.ComposeCommandLine([]string{exe, "shrink", "--lang", "en"}))
	if code := c.wait(t, 30*time.Second); code != 2 {
		t.Fatalf("exit %d; it drew:\n%s", code, c.text())
	}
	c.waitFor(t, "✗ shrink takes the names of the disks", 5*time.Second)
	c.waitFor(t, "→ see wsl-shrinker list", 5*time.Second)
	// In color: the console was asked to read escape sequences.
	c.mu.Lock()
	raw := string(c.drawn)
	c.mu.Unlock()
	// The console redraws in its own sequences: red, maybe a move, the cross.
	if !regexp.MustCompile(`\x1b\[(31|91)m(\x1b\[[0-9;?]*[A-Za-z])*✗`).MatchString(raw) {
		t.Fatalf("no red cross in %q", raw)
	}
}

// console is one program running in a pseudoconsole of its own.
type console struct {
	hpc     windows.Handle
	in      windows.Handle
	process windows.Handle
	mu      sync.Mutex
	drawn   []byte
	done    chan struct{}
}

// startConsole runs cmdline in a new pseudoconsole of cols x rows. The
// program sees a real console, as in a terminal tab; what it draws is read
// as it comes. A child that failed to attach would share this test's own
// windowless console, so no window can open either way.
func startConsole(t *testing.T, cols, rows int, cmdline string) *console {
	t.Helper()
	var inRead, inWrite, outRead, outWrite windows.Handle
	if err := windows.CreatePipe(&inRead, &inWrite, nil, 0); err != nil {
		t.Fatal(err)
	}
	if err := windows.CreatePipe(&outRead, &outWrite, nil, 0); err != nil {
		t.Fatal(err)
	}
	c := &console{in: inWrite, done: make(chan struct{})}
	if err := windows.CreatePseudoConsole(windows.Coord{X: int16(cols), Y: int16(rows)}, inRead, outWrite, 0, &c.hpc); err != nil {
		t.Fatalf("pseudoconsole: %v", err)
	}
	// The pseudoconsole holds its own copies of these ends now.
	windows.CloseHandle(inRead)
	windows.CloseHandle(outWrite)
	// Everything drawn is read as it comes: a full pipe would stall the
	// console, and the program with it.
	go func() {
		defer close(c.done)
		buf := make([]byte, 32<<10)
		for {
			var n uint32
			if err := windows.ReadFile(outRead, buf, &n, nil); err != nil || n == 0 {
				return
			}
			c.mu.Lock()
			c.drawn = append(c.drawn, buf[:n]...)
			c.mu.Unlock()
		}
	}()
	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		t.Fatal(err)
	}
	defer attrs.Delete()
	// The attribute's value is the handle itself, not a pointer to it: its
	// bits are read as a pointer, which is what the API takes.
	if err := attrs.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, *(*unsafe.Pointer)(unsafe.Pointer(&c.hpc)), unsafe.Sizeof(c.hpc)); err != nil {
		t.Fatal(err)
	}
	si := new(windows.StartupInfoEx)
	// Standard handles empty: the program takes the pseudoconsole's, not
	// this test's (pipes, when it runs from WSL).
	si.Flags = windows.STARTF_USESTDHANDLES
	si.ProcThreadAttributeList = attrs.List()
	si.Cb = uint32(unsafe.Sizeof(*si))
	cmd, err := windows.UTF16PtrFromString(cmdline)
	if err != nil {
		t.Fatal(err)
	}
	// The program is this binary: it runs main when it sees this.
	t.Setenv("WSS_E2E_MAIN", "1")
	var pi windows.ProcessInformation
	// No CREATE_NO_WINDOW: it would keep the program off the pseudoconsole.
	if err := windows.CreateProcess(nil, cmd, nil, nil, false, windows.EXTENDED_STARTUPINFO_PRESENT, nil, nil, &si.StartupInfo, &pi); err != nil {
		windows.ClosePseudoConsole(c.hpc)
		t.Fatalf("start: %v", err)
	}
	windows.CloseHandle(pi.Thread)
	c.process = pi.Process
	t.Cleanup(func() {
		// Whatever happened, nothing is left running.
		windows.TerminateProcess(c.process, 1)
		windows.CloseHandle(c.process)
		windows.ClosePseudoConsole(c.hpc)
		windows.CloseHandle(c.in)
	})
	return c
}

// vt matches the escape sequences a screen is drawn with.
var vt = regexp.MustCompile(`\x1b\[[0-9;?<>=]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b[()][0-9A-B]|\x1b[=>78]`)

// text is what was drawn so far, without its escape sequences.
func (c *console) text() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return vt.ReplaceAllString(string(c.drawn), "")
}

// size is how many bytes were drawn so far.
func (c *console) size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.drawn)
}

// waitFor waits until the screen shows want.
func (c *console) waitFor(t *testing.T, want string, limit time.Duration) {
	t.Helper()
	for end := time.Now().Add(limit); time.Now().Before(end); time.Sleep(100 * time.Millisecond) {
		if strings.Contains(c.text(), want) {
			return
		}
	}
	t.Fatalf("%q never showed; it drew:\n%s", want, c.text())
}

// waitForGrowth waits until more than from bytes were drawn.
func (c *console) waitForGrowth(t *testing.T, from int, limit time.Duration) {
	t.Helper()
	for end := time.Now().Add(limit); time.Now().Before(end); time.Sleep(100 * time.Millisecond) {
		if c.size() > from {
			return
		}
	}
	t.Fatal("nothing was drawn again after the console changed size")
}

// resize changes the console's size, as dragging the window does.
func (c *console) resize(t *testing.T, cols, rows int) {
	t.Helper()
	if err := windows.ResizePseudoConsole(c.hpc, windows.Coord{X: int16(cols), Y: int16(rows)}); err != nil {
		t.Fatal(err)
	}
}

// write types keys.
func (c *console) write(t *testing.T, keys string) {
	t.Helper()
	var n uint32
	if err := windows.WriteFile(c.in, []byte(keys), &n, nil); err != nil {
		t.Fatal(err)
	}
}

// wait waits for the program to end and returns its exit code.
func (c *console) wait(t *testing.T, limit time.Duration) uint32 {
	t.Helper()
	ev, err := windows.WaitForSingleObject(c.process, uint32(limit/time.Millisecond))
	if err != nil || ev != windows.WAIT_OBJECT_0 {
		t.Fatalf("still running after %s; it drew:\n%s", limit, c.text())
	}
	var code uint32
	if err := windows.GetExitCodeProcess(c.process, &code); err != nil {
		t.Fatal(err)
	}
	return code
}
