//go:build windows

package winapi

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// The calls uninstall makes, tried on files and processes of the test's
// own. The PATH is never read or written here.

// holdAlone opens path with no sharing, as WSL's server holds the launcher,
// and returns how to let go of it.
func holdAlone(t *testing.T, path string) func() {
	t.Helper()
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	once := false
	release := func() {
		if !once {
			once = true
			windows.CloseHandle(h)
		}
	}
	t.Cleanup(release)
	return release
}

func TestInUseSeesAFileAnotherHandleHolds(t *testing.T) {
	p := filepath.Join(t.TempDir(), "wsl-shrinker")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Nobody has it: free to remove.
	if InUse(p) {
		t.Fatal("a file nobody holds reads as in use")
	}
	release := holdAlone(t, p)
	if !InUse(p) {
		t.Fatal("a file held reads as free")
	}
	release()
	if InUse(p) {
		t.Fatal("a file let go of still reads as in use")
	}
	// A program running does not hold its own file this way: Windows maps
	// it and closes the handle. Another copy running is found by
	// OthersRunning instead, and the program's own removal waits for it to
	// end (DeleteAfterExit).
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if InUse(self) {
		t.Fatal("the running test program reads as held: OthersRunning is then not needed")
	}
	// A file that is not there is not in use: it is not there to remove.
	if InUse(filepath.Join(t.TempDir(), "none")) {
		t.Fatal("a missing file reads as in use")
	}
}

func TestOthersRunningCountsOtherCopiesOfTheProgram(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// This process is not counted: it is the one asking.
	if n, err := OthersRunning(self); err != nil || n != 0 {
		t.Fatalf("alone: %d, %v", n, err)
	}
	// Another copy of the same file, running until its stdin closes.
	child := exec.Command(self)
	child.Env = append(os.Environ(), "WAIT_CHILD=1")
	in, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer child.Wait()
	defer in.Close()
	// Its path in another case is the same file.
	if n, err := OthersRunning(strings.ToUpper(self)); err != nil || n != 1 {
		t.Fatalf("with a copy running: %d, %v", n, err)
	}
	// A program of the same name in another folder is another program.
	if n, err := OthersRunning(filepath.Join(t.TempDir(), filepath.Base(self))); err != nil || n != 0 {
		t.Fatalf("another folder: %d, %v", n, err)
	}
}

// sysDir is Windows' system folder, where cmd.exe and ping.exe are.
func sysDir(t *testing.T) string {
	t.Helper()
	d, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// runLine runs a cmd.exe command line as DeleteAfterExit does, with no
// window, in the system folder, and waits for it.
func runLine(t *testing.T, line string) time.Duration {
	t.Helper()
	cmd := exec.Command(filepath.Join(sysDir(t), "cmd.exe"))
	cmd.Dir = sysDir(t)
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: line, HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	start := time.Now()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s\nline: %s", err, out, line)
	}
	return time.Since(start)
}

func TestDeleteLineWaitsForTheProgramThenRemovesItsFolder(t *testing.T) {
	// The folder of an install: the program alone is left in it, held as a
	// running program holds its file.
	dir := filepath.Join(t.TempDir(), "wsl-shrinker")
	exe := filepath.Join(dir, "wsl-shrinker.exe")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	release := holdAlone(t, exe)
	// The program ends two seconds in.
	go func() {
		time.Sleep(2 * time.Second)
		release()
	}()
	line, err := deleteLine(exe, dir, sysDir(t))
	if err != nil {
		t.Fatal(err)
	}
	took := runLine(t, line)
	// It tried while the file was held, and went on until it was not.
	if took < 2*time.Second || took > 20*time.Second {
		t.Fatalf("took %v", took)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the folder is still there: %v", err)
	}
}

func TestDeleteLineLeavesAFolderWithOtherFiles(t *testing.T) {
	for _, c := range []struct {
		name  string
		other bool
		dir   bool
	}{
		// The folder is not the install's own: only the program goes.
		{"no folder given", false, false},
		// rd without /s: a file of the person's keeps the folder.
		{"another file in it", true, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Every mark cmd.exe gives a meaning of its own, in quotes.
			dir := filepath.Join(t.TempDir(), "tools (x86) & ^more!")
			exe := filepath.Join(dir, "wsl-shrinker.exe")
			other := filepath.Join(dir, "other.txt")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			for _, p := range []string{exe, other} {
				if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if !c.other {
				if err := os.Remove(other); err != nil {
					t.Fatal(err)
				}
			}
			given := ""
			if c.dir {
				given = dir
			}
			line, err := deleteLine(exe, given, sysDir(t))
			if err != nil {
				t.Fatal(err)
			}
			runLine(t, line)
			// The program is gone; the folder stays, with what else is in it.
			if _, err := os.Stat(exe); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("the program is still there: %v", err)
			}
			if _, err := os.Stat(dir); err != nil {
				t.Fatalf("the folder went: %v", err)
			}
			if _, err := os.Stat(other); c.other && err != nil {
				t.Fatalf("the other file went: %v", err)
			}
		})
	}
}

func TestDeleteLineRefusesWhatCmdWouldRead(t *testing.T) {
	for _, p := range []string{`C:\a%PATH%\wsl-shrinker.exe`, `C:\a"b\wsl-shrinker.exe`} {
		if _, err := deleteLine(p, "", sysDir(t)); err == nil {
			t.Errorf("%q accepted", p)
		}
		if _, err := deleteLine(`C:\x\wsl-shrinker.exe`, filepath.Dir(p), sysDir(t)); err == nil {
			t.Errorf("folder %q accepted", filepath.Dir(p))
		}
	}
}

func TestWhatChangesThePCRefusesInATest(t *testing.T) {
	// Whatever path a test takes, it never reads or writes the person's
	// PATH, nor starts a removal through cmd.exe.
	if _, _, err := UserPath(); !errors.Is(err, errNotInTests) {
		t.Errorf("UserPath: %v", err)
	}
	if err := SetUserPath(`C:\x`, true); !errors.Is(err, errNotInTests) {
		t.Errorf("SetUserPath: %v", err)
	}
	if err := DeleteAfterExit(`C:\x\wsl-shrinker.exe`, ""); !errors.Is(err, errNotInTests) {
		t.Errorf("DeleteAfterExit: %v", err)
	}
}

func TestDeleteLineWaitsWithNoFolderToRemoveToo(t *testing.T) {
	// A folder that is not the install's own: no folder is given, and the
	// tries still wait a second each while the program holds its file.
	dir := t.TempDir()
	exe := filepath.Join(dir, "wsl-shrinker.exe")
	if err := os.WriteFile(exe, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	release := holdAlone(t, exe)
	go func() {
		time.Sleep(2 * time.Second)
		release()
	}()
	line, err := deleteLine(exe, "", sysDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if took := runLine(t, line); took < 2*time.Second || took > 20*time.Second {
		t.Fatalf("took %v: the tries did not wait", took)
	}
	if _, err := os.Stat(exe); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the program is still there: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("the folder went: %v", err)
	}
}

func TestAnEmptyFolderVariableReachesNoRealFolderInATest(t *testing.T) {
	// Outside a test, Windows' own folder stands in; inside one, nothing
	// does, so a test never reads or writes the person's files.
	t.Setenv("APPDATA", "")
	t.Setenv("LOCALAPPDATA", "")
	if a, l := AppData(), LocalAppData(); a != "" || l != "" {
		t.Fatalf("%q %q", a, l)
	}
	t.Setenv("APPDATA", `C:\x`)
	if a := AppData(); a != `C:\x` {
		t.Fatalf("%q", a)
	}
}
