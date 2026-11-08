//go:build !windows

package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// The machine types a Windows program's header names (IMAGE_FILE_MACHINE_*).
const (
	machineI386  = 0x014c
	machineARMNT = 0x01c4
	machineAMD64 = 0x8664
	machineARM64 = 0xaa64
)

// machineNames are the words for a machine type in a message.
var machineNames = map[uint16]string{machineI386: "32-bit x86", machineARMNT: "32-bit Arm", machineAMD64: "x64", machineARM64: "ARM64"}

// firstX64Emulation is the first Windows build on ARM that runs x64
// programs: an Insider build of Windows 10, then every Windows 11 on ARM.
// Windows 10 on ARM as released (builds 19041 to 19045) runs ARM64 and
// 32-bit x86 programs only.
const firstX64Emulation = 21277

// firstWithoutARM32 is the first Windows build on ARM that no longer runs
// 32-bit Arm programs: an Insider build of Windows 11, then 24H2 (26100)
// and every later one.
const firstWithoutARM32 = 25905

// What the header check asks of the PC: tests set them, so no test reads
// another PC's answers or starts cmd.exe.
var (
	// hostMachine is the machine type of the PC this runs on.
	hostMachine = unameMachine
	// windowsBuild is the build of the Windows this WSL runs on, and
	// whether it could be read.
	windowsBuild = askWindowsBuild
)

// peMachine reads the machine type from a Windows program's header: the
// "MZ" header points at the "PE" one, whose first field it is. ok is false
// for a file that is no Windows program, or that cannot be read.
func peMachine(path string) (uint16, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	// The DOS header: "MZ", and at 0x3c where the PE header starts.
	var dos [0x40]byte
	if _, err := io.ReadFull(f, dos[:]); err != nil || dos[0] != 'M' || dos[1] != 'Z' {
		return 0, false
	}
	at := int64(binary.LittleEndian.Uint32(dos[0x3c:]))
	// "PE\0\0", then the machine type.
	var pe [6]byte
	if _, err := f.ReadAt(pe[:], at); err != nil || string(pe[:4]) != "PE\x00\x00" {
		return 0, false
	}
	return binary.LittleEndian.Uint16(pe[4:]), true
}

// unameMachine is the machine type of the kernel this runs on: WSL's
// virtual machine has the PC's own processor. 0 when it is neither x64 nor
// ARM64.
func unameMachine() uint16 {
	var u unix.Utsname
	if err := unix.Uname(&u); err != nil {
		return 0
	}
	switch unix.ByteSliceToString(u.Machine[:]) {
	case "x86_64":
		return machineAMD64
	case "aarch64":
		return machineARM64
	}
	return 0
}

// askWindowsBuild asks cmd.exe which Windows this is: Windows' own program,
// built for the PC's processor, so starting it never brings up the dialog
// the header check is there to keep away. It waits five seconds at most,
// for finding cmd.exe too: a drive that does not answer leaves the build
// unknown, and Windows answers instead.
func askWindowsBuild() (int, bool) {
	type answer struct {
		build int
		ok    bool
	}
	got := make(chan answer, 1)
	go func() {
		b, ok := readWindowsBuild()
		got <- answer{b, ok}
	}()
	select {
	case a := <-got:
		return a.build, a.ok
	case <-time.After(5 * time.Second):
		return 0, false
	}
}

// readWindowsBuild is askWindowsBuild without its wait.
func readWindowsBuild() (int, bool) {
	cmd, err := exec.LookPath("cmd.exe")
	if err != nil {
		// Windows' folders left off the PATH: cmd.exe where the drives are.
		if cmd = systemFile("cmd.exe"); cmd == "" {
			return 0, false
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, cmd, "/d", "/c", "ver")
	// Started in a Linux folder, cmd.exe warns it cannot use it: its own
	// folder is a Windows one.
	c.Dir = filepath.Dir(cmd)
	out, err := c.Output()
	if err != nil {
		return 0, false
	}
	return parseVer(string(out))
}

// verBuild finds the build in ver's line, "Microsoft Windows [Version
// 10.0.22631.4317]": the third of its numbers, whatever words are around.
var verBuild = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// parseVer reads the build from what ver printed.
func parseVer(s string) (int, bool) {
	m := verBuild.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	build, err := strconv.Atoi(m[3])
	return build, err == nil
}

// systemFile finds a file in Windows' System32 through the Windows drives
// WSL mounted (drvfs, as /proc/mounts lists them), or "" when none has it.
func systemFile(name string) string {
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return ""
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		// "C:\134 /mnt/c 9p rw,...,aname=drvfs;path=C:\;..." for a drive.
		fields := strings.Fields(s.Text())
		if len(fields) < 4 || !(fields[2] == "drvfs" || fields[2] == "virtiofs" || fields[2] == "9p" && strings.Contains(fields[3], "aname=drvfs")) {
			continue
		}
		// /proc/mounts writes a space in a folder's name as \040.
		p := filepath.Join(strings.ReplaceAll(fields[1], `\040`, " "), "Windows", "System32", name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// runsOn reports whether Windows on a host of one machine type starts a
// program built for another. x64 Windows runs x64 and 32-bit x86 programs.
// ARM64 Windows runs its own and 32-bit x86 ones, x64 ones from build
// firstX64Emulation, and 32-bit Arm ones before build firstWithoutARM32,
// which build says; a build it cannot read is let through, and Windows
// answers. An unknown host is let through as well.
func runsOn(program, host uint16, build func() (int, bool)) bool {
	switch host {
	case machineAMD64:
		return program == machineAMD64 || program == machineI386
	case machineARM64:
		switch program {
		case machineARM64, machineI386:
			return true
		case machineAMD64:
			b, ok := build()
			return !ok || b >= firstX64Emulation
		case machineARMNT:
			b, ok := build()
			return !ok || b < firstWithoutARM32
		}
		return false
	}
	return true
}

// wrongBuild says why the .exe at path cannot start on this PC, or "" when
// it can (or its header does not say). Started anyway, Windows would stop
// it with a dialog on the desktop, far from this terminal.
func wrongBuild(path string) string {
	program, ok := peMachine(path)
	if !ok {
		return ""
	}
	host := hostMachine()
	// The build is asked once at most, and only when it decides.
	asked, build, known := false, 0, false
	ask := func() (int, bool) {
		if !asked {
			asked = true
			build, known = windowsBuild()
		}
		return build, known
	}
	if runsOn(program, host, ask) {
		return ""
	}
	built := machineNames[program]
	if built == "" {
		built = "another kind of processor"
	}
	// On an ARM64 PC, its Windows decides: too old to emulate x64, or too
	// new to run 32-bit Arm.
	switch {
	case host == machineARM64 && program == machineAMD64:
		return path + " is built for x64 Windows; this PC runs Windows 10 on ARM, which runs ARM64 and 32-bit x86 programs only"
	case host == machineARM64 && program == machineARMNT:
		return path + " is built for 32-bit Arm Windows; this PC runs a Windows on ARM that no longer runs 32-bit Arm programs"
	}
	return path + " is built for " + built + " Windows; this PC runs " + machineNames[host] + " Windows"
}
