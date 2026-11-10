//go:build windows

package winapi

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// What an uninstall asks of Windows: the user's PATH as it is stored, which
// files another program holds, which other copies of this program run, and
// the removal of this program's own file once it has ended.

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	procSendMessageTimeoutW = user32.NewProc("SendMessageTimeoutW")
)

// errNotInTests is what the calls that change this PC answer in a test
// binary: a test never reads or writes the person's PATH, nor removes a
// file through cmd.exe, whatever path it took to get here.
var errNotInTests = errors.New("a test does not change this PC")

// UserPath is the user's own PATH as stored under HKEY_CURRENT_USER\
// Environment, with its variables unexpanded, and whether Windows stores
// it as a value it expands (REG_EXPAND_SZ). A user with no PATH of their
// own has an empty one, to be stored expandable as Windows stores it.
func UserPath() (string, bool, error) {
	if testing.Testing() {
		return "", false, errNotInTests
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, "Environment", registry.QUERY_VALUE)
	if err != nil {
		return "", false, err
	}
	defer k.Close()
	// GetStringValue gives the value as stored: %USERPROFILE% stays.
	v, kind, err := k.GetStringValue("Path")
	if errors.Is(err, registry.ErrNotExist) {
		return "", true, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, kind == registry.EXPAND_SZ, nil
}

// SetUserPath stores the user's PATH, expandable or not as it was, and
// tells the programs that listen (Explorer, which starts the terminals)
// that the environment changed, as System Properties does when it saves.
func SetUserPath(value string, expand bool) error {
	if testing.Testing() {
		return errNotInTests
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, "Environment", registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	// The value keeps its type: a PATH stored plain stays plain.
	if expand {
		err = k.SetExpandStringValue("Path", value)
	} else {
		err = k.SetStringValue("Path", value)
	}
	if err != nil {
		return err
	}
	announceEnvironment()
	return nil
}

// announceEnvironment sends WM_SETTINGCHANGE for "Environment" to every
// top-level window, waiting a few seconds at most for one that does not
// answer. Nothing comes of its failure but terminals that see the change
// only after a sign-in.
func announceEnvironment() {
	env, err := windows.UTF16PtrFromString("Environment")
	if err != nil {
		return
	}
	var result uintptr
	// HWND_BROADCAST, WM_SETTINGCHANGE, SMTO_ABORTIFHUNG, 5 seconds.
	_, _, _ = procSendMessageTimeoutW.Call(0xffff, 0x001a, 0, uintptr(unsafe.Pointer(env)), 0x0002, 5000, uintptr(unsafe.Pointer(&result)))
}

// InUse reports whether another program has path open: opened with no
// sharing, its open fails with a sharing violation. WSL holds the launcher
// this way while a distribution runs it. A program running on Windows does
// not hold its own file so: Windows maps it and lets go of the handle.
func InUse(path string) bool {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return errors.Is(err, windows.ERROR_SHARING_VIOLATION)
	}
	windows.CloseHandle(h)
	return false
}

// OthersRunning counts the processes other than this one that run the
// program at exe, its full path compared without regard to case. A
// process this user may not look at is not counted: it is not one of
// this user's.
func OthersRunning(exe string) (int, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(snap)
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	self, name, n := uint32(os.Getpid()), filepath.Base(exe), 0
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		// The name first: only a process of the same name is asked its path.
		if e.ProcessID == self || !strings.EqualFold(windows.UTF16ToString(e.ExeFile[:]), name) {
			continue
		}
		if p, ok := imagePath(e.ProcessID); ok && strings.EqualFold(filepath.Clean(p), filepath.Clean(exe)) {
			n++
		}
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return n, err
	}
	return n, nil
}

// imagePath is the full path of the program process pid runs.
func imagePath(pid uint32) (string, bool) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", false
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return "", false
	}
	return windows.UTF16ToString(buf[:n]), true
}

// DeleteAfterExit has a hidden cmd.exe remove exe once this process has
// ended and let go of it, then dir when it is given and empty. It tries
// for about half a minute, once a second: the moment this process takes to
// end, and a virus scanner that looks at the file a little longer. It is
// started and not waited for, with no window, outside this console's
// process group, and with none of this process's handles: a terminal
// waiting for this program's output does not wait for it too. It runs in
// Windows' system folder, by the full paths of cmd.exe and ping.exe: a
// folder a process is in cannot be removed, and what the program's folder
// holds is never what runs.
func DeleteAfterExit(exe, dir string) error {
	if testing.Testing() {
		return errNotInTests
	}
	sys, err := windows.GetSystemDirectory()
	if err != nil {
		return err
	}
	line, err := deleteLine(exe, dir, sys)
	if err != nil {
		return err
	}
	start := func(flags uint32) error {
		cmd := exec.Command(filepath.Join(sys, "cmd.exe"))
		cmd.Dir = sys
		cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: line, HideWindow: true, CreationFlags: flags}
		if err := cmd.Start(); err != nil {
			return err
		}
		// It outlives this process by design: nothing here waits for it.
		return cmd.Process.Release()
	}
	flags := uint32(windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP)
	// Out of a job this process may be in, as an SSH session's, which ends
	// every process in it as the session closes. A job that does not let
	// it out refuses the start: then it starts inside the job.
	if start(flags|windows.CREATE_BREAKAWAY_FROM_JOB) == nil {
		return nil
	}
	return start(flags)
}

// deleteLine is DeleteAfterExit's command line, with sys the system
// folder that holds cmd.exe and ping.exe. cmd.exe reads %NAME% as a
// variable even inside quotes, and a quote would end the path: a path with
// either is refused, to be removed by hand. AutoRun commands and delayed
// expansion stay off, whatever the registry says.
func deleteLine(exe, dir, sys string) (string, error) {
	for _, p := range []string{exe, dir, sys} {
		if strings.ContainsAny(p, "\"%\r\n") {
			return "", fmt.Errorf("cannot remove %q through cmd.exe", p)
		}
	}
	// Once the file is gone, the folder goes if it is empty: rd without /s
	// never removes anything in it. The parentheses are what make cmd.exe
	// read the else that follows.
	done := "(exit)"
	if dir != "" {
		done = `(rd "` + dir + `" >nul 2>&1 & exit)`
	}
	// ping waits a second between tries: timeout.exe needs a console input.
	return `"` + filepath.Join(sys, "cmd.exe") + `" /d /v:off /c for /l %i in (1,1,30) do (del /f /q "` + exe + `" >nul 2>&1 & ` +
		`if not exist "` + exe + `" ` + done + ` else "` + filepath.Join(sys, "PING.EXE") + `" -n 2 127.0.0.1 >nul)`, nil
}
