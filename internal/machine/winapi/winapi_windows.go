//go:build windows

package winapi

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32                = windows.NewLazySystemDLL("kernel32.dll")
	procGetCompressedSizeW  = kernel32.NewProc("GetCompressedFileSizeW")
	procGetUserDefaultUILng = kernel32.NewProc("GetUserDefaultUILanguage")
	procGetConsoleWindow    = kernel32.NewProc("GetConsoleWindow")
	procGetConsoleOutputCP  = kernel32.NewProc("GetConsoleOutputCP")
)

// Stat returns what Windows says about the file at path.
func Stat(path string) (FileInfo, error) {
	var fi FileInfo
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return fi, err
	}
	// Length and attributes in one call.
	var data windows.Win32FileAttributeData
	if err := windows.GetFileAttributesEx(p, windows.GetFileExInfoStandard, (*byte)(unsafe.Pointer(&data))); err != nil {
		return fi, &os.PathError{Op: "stat", Path: path, Err: err}
	}
	fi.Size = int64(data.FileSizeHigh)<<32 | int64(data.FileSizeLow)
	a := data.FileAttributes
	fi.Sparse = a&windows.FILE_ATTRIBUTE_SPARSE_FILE != 0
	fi.Compressed = a&windows.FILE_ATTRIBUTE_COMPRESSED != 0
	fi.Encrypted = a&windows.FILE_ATTRIBUTE_ENCRYPTED != 0
	// The space allocated on the drive: for a sparse or compressed file it
	// is less than the length, and that is what a user sees free up.
	fi.OnDisk = fi.Size
	var high uint32
	low, _, callErr := procGetCompressedSizeW.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&high)))
	if uint32(low) != 0xFFFFFFFF || callErr == windows.ERROR_SUCCESS {
		fi.OnDisk = int64(high)<<32 | int64(uint32(low))
	}
	// Free space of the drive the file is on.
	dir, _ := windows.UTF16PtrFromString(filepath.Dir(path))
	var free, total, totalFree uint64
	if windows.GetDiskFreeSpaceEx(dir, &free, &total, &totalFree) == nil {
		fi.DriveFree = int64(free)
	}
	// Opening for reading while denying other writers fails when anyone
	// writes to it: WSL's VM, while the disk is attached.
	h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		fi.Held = err == windows.ERROR_SHARING_VIOLATION
	} else {
		windows.CloseHandle(h)
	}
	return fi, nil
}

// FromWSL reports whether this process was started from inside a WSL
// distribution: then one of its ancestors is wsl.exe or wslhost.exe, and
// stopping that distribution would close this program's terminal.
func FromWSL() bool {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(snap)
	// Map every process to its parent and name.
	parent := map[uint32]uint32{}
	name := map[uint32]string{}
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err := windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		parent[e.ProcessID] = e.ParentProcessID
		name[e.ProcessID] = strings.ToLower(windows.UTF16ToString(e.ExeFile[:]))
	}
	// Walk up from this process; process ids are reused, so stop at a loop.
	seen := map[uint32]bool{}
	for pid := uint32(os.Getpid()); pid != 0 && !seen[pid]; pid = parent[pid] {
		seen[pid] = true
		if n := name[pid]; n == "wsl.exe" || n == "wslhost.exe" {
			return true
		}
	}
	return false
}

// Reopen starts this program again in a new terminal window with args, so
// it keeps running when the distribution it was started from stops. It
// uses `start`, which opens the default terminal (Windows Terminal where it
// is set as the default, the console host otherwise).
func Reopen(args []string) error {
	// A test never opens a window on the person's screen, whatever path it
	// took to get here: the tests check the command line with `start /b`.
	if testing.Testing() {
		return errNoWindowInTests
	}
	line, err := reopenLine(args, false)
	if err != nil {
		return err
	}
	return runCmdLine(line)
}

// errNoWindowInTests is Reopen refusing inside a test binary.
var errNoWindowInTests = errors.New("a test does not open windows")

// reopenLine builds the cmd.exe command line for Reopen. inPlace uses
// `start /b`, which runs in this console instead of a new window: the
// tests use it to check the arguments arrive whole without opening one.
func reopenLine(args []string, inPlace bool) (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	// cmd.exe gives these characters a meaning of their own even inside
	// quotes; the arguments here are disk names and item ids, which never
	// hold them, so refuse rather than escape.
	if strings.ContainsAny(self, "\"%") {
		return "", fmt.Errorf("cannot start %q in a new window", self)
	}
	for _, a := range args {
		if strings.ContainsAny(a, "&|<>^%\"\r\n") {
			return "", fmt.Errorf("cannot pass %q to a new window", a)
		}
	}
	b := strings.Builder{}
	// The first quoted argument of start is the window title.
	b.WriteString(`cmd.exe /d /c start "wsl-shrinker" `)
	if inPlace {
		b.WriteString("/b ")
	}
	b.WriteString(`"` + self + `"`)
	for _, a := range args {
		b.WriteString(` "` + a + `"`)
	}
	return b.String(), nil
}

// runCmdLine runs a full command line with no window of its own.
func runCmdLine(line string) error {
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: line, HideWindow: true}
	return cmd.Run()
}

// Korean reports whether Windows shows its interface in Korean.
func Korean() bool {
	id, _, _ := procGetUserDefaultUILng.Call()
	// The primary language is the low ten bits; 0x12 is Korean.
	return id&0x3ff == 0x12
}

// LocalAppData is the per-user folder for this tool's history.
func LocalAppData() string { return os.Getenv("LOCALAPPDATA") }

// AppData is the per-user folder for its settings.
func AppData() string { return os.Getenv("APPDATA") }

var (
	virtdisk            = windows.NewLazySystemDLL("virtdisk.dll")
	procOpenVirtualDisk = virtdisk.NewProc("OpenVirtualDisk")
	procCompactVirtual  = virtdisk.NewProc("CompactVirtualDisk")
)

// virtualStorageType names a VHDX made by Microsoft (VIRTUAL_STORAGE_TYPE).
type virtualStorageType struct {
	DeviceID uint32
	VendorID windows.GUID
}

// CompactFile compacts a VHDX file that no one holds, in place, the way
// `wsl --manage --compact` does after its trim: blocks the guest trimmed or
// never wrote are dropped from the file. For disks WSL does not register,
// such as Docker Desktop's data disk.
func CompactFile(path string) error {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	// VIRTUAL_STORAGE_TYPE_DEVICE_VHDX and the Microsoft vendor GUID.
	st := virtualStorageType{DeviceID: 3, VendorID: windows.GUID{Data1: 0xEC984AEC, Data2: 0xA0F9, Data3: 0x47E9,
		Data4: [8]byte{0x90, 0x1F, 0x71, 0x41, 0x5A, 0x66, 0x34, 0x5B}}}
	// OPEN_VIRTUAL_DISK_PARAMETERS version 1, no read-write depth.
	params := [2]uint32{1, 0}
	var h windows.Handle
	// Access: VIRTUAL_DISK_ACCESS_METAOPS (0x200000), what compaction needs.
	r, _, _ := procOpenVirtualDisk.Call(uintptr(unsafe.Pointer(&st)), uintptr(unsafe.Pointer(p)), 0x200000, 0,
		uintptr(unsafe.Pointer(&params)), uintptr(unsafe.Pointer(&h)))
	if r != 0 {
		return &os.PathError{Op: "open virtual disk", Path: path, Err: syscall.Errno(r)}
	}
	defer windows.CloseHandle(h)
	// COMPACT_VIRTUAL_DISK_PARAMETERS version 1, no flags, no overlapped.
	cparams := [2]uint32{1, 0}
	if r, _, _ := procCompactVirtual.Call(uintptr(h), 0, uintptr(unsafe.Pointer(&cparams)), 0); r != 0 {
		return &os.PathError{Op: "compact virtual disk", Path: path, Err: syscall.Errno(r)}
	}
	return nil
}

// Running reports whether process pid runs and is the program named exe. A
// process id is given to another program once its own ends, so the name is
// compared too. A process this user may not look at counts as not running:
// it is not one of this user's runs.
func Running(pid int, exe string) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	// An ended process keeps its id while a handle is open: ask whether it
	// still runs.
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil || code != stillActive {
		return false
	}
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return false
	}
	return strings.EqualFold(filepath.Base(windows.UTF16ToString(buf[:n])), exe)
}

// stillActive is the exit code of a process that has not ended (STILL_ACTIVE).
const stillActive = 259

// Colors turns on escape-code colors on the console behind fd, and reports
// whether the console takes them; false for a pipe or a file. The mode it
// changes is put back by RestoreConsole.
func Colors(fd uintptr) bool {
	var mode uint32
	h := windows.Handle(fd)
	if windows.GetConsoleMode(h, &mode) != nil {
		return false
	}
	if mode&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING != 0 {
		return true
	}
	if windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) != nil {
		return false
	}
	// The first mode seen is the one the console had.
	consoleMu.Lock()
	defer consoleMu.Unlock()
	if _, ok := consoleModes[h]; !ok {
		consoleModes[h] = mode
	}
	return true
}

// consoleModes are the consoles' modes before Colors changed them.
var (
	consoleMu    sync.Mutex
	consoleModes = map[windows.Handle]uint32{}
)

// ClassicConsole reports whether this process draws, on its standard
// output, in a console window of its own: the Windows console host, with the
// console's own fonts, rather than a terminal that runs the console as a
// pseudoconsole (Windows Terminal, VS Code's, any other), which draws with
// fonts of its own and never shows that console's window. The window decides,
// not WT_SESSION: a console window opened from Windows Terminal inherits that
// variable.
func ClassicConsole() bool {
	// Output to a file or a pipe is drawn by nobody.
	var mode uint32
	if windows.GetConsoleMode(windows.Handle(os.Stdout.Fd()), &mode) != nil {
		return false
	}
	// No console window at all: nothing to draw in.
	h, _, _ := procGetConsoleWindow.Call()
	if h == 0 {
		return false
	}
	return windows.IsWindowVisible(windows.HWND(h))
}

// ConsoleEastAsian reports whether the console writes in an East Asian code
// page (Japanese 932, Simplified Chinese 936, Korean 949, Traditional
// Chinese 950), where the console host draws characters Unicode leaves
// ambiguous two cells wide.
func ConsoleEastAsian() bool {
	cp, _, _ := procGetConsoleOutputCP.Call()
	switch cp {
	case 932, 936, 949, 950:
		return true
	}
	return false
}

// RestoreConsole puts back the modes Colors changed, as the program ends:
// the console is left as the shell had it.
func RestoreConsole() {
	consoleMu.Lock()
	defer consoleMu.Unlock()
	for h, mode := range consoleModes {
		_ = windows.SetConsoleMode(h, mode)
		delete(consoleModes, h)
	}
}
