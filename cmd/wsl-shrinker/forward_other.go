//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
)

// forward hands the command line to wsl-shrinker.exe when this Linux build
// runs inside a WSL distribution: the disks belong to Windows, so the work
// is done there. WSLENV carries this distribution's name across ("/w": from WSL to Windows only), so the
// Windows side knows which distribution its terminal runs in. It returns
// false to run here instead (the demo and its snapshots work anywhere).
func forward() bool {
	if os.Getenv("WSL_DISTRO_NAME") == "" || slices.ContainsFunc(os.Args[1:], func(a string) bool {
		return a == "--demo" || strings.HasPrefix(a, "--snapshot")
	}) {
		return false
	}
	exe := findExe()
	if exe == "" {
		fmt.Fprintln(os.Stderr, "wsl-shrinker: wsl-shrinker.exe not found next to this file or on the Windows PATH; install it on Windows first")
		os.Exit(1)
	}
	// The distribution's name, and the language chosen in this shell.
	wslenv := appendWSLENV(os.Getenv("WSLENV"), "WSL_DISTRO_NAME/w")
	wslenv = appendWSLENV(wslenv, "WSL_SHRINKER_LANG/w")
	env := withWSLENV(os.Environ(), wslenv)
	// Replace this process: the terminal, the exit code and Ctrl+C all
	// belong to the Windows program from here on.
	err := syscall.Exec(exe, append([]string{exe}, os.Args[1:]...), env)
	fmt.Fprintln(os.Stderr, "wsl-shrinker:", err)
	os.Exit(1)
	return true
}

// findExe looks next to this file first, then on the PATH, which in WSL
// includes Windows' own.
func findExe() string {
	if self, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(self), "wsl-shrinker.exe")
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	if p, err := exec.LookPath("wsl-shrinker.exe"); err == nil {
		return p
	}
	return ""
}

// withWSLENV returns env with WSLENV set to v. The entry is replaced, not
// added: with two, the first one counts, and that is the old value.
func withWSLENV(env []string, v string) []string {
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		if !strings.HasPrefix(e, "WSLENV=") {
			out = append(out, e)
		}
	}
	return append(out, "WSLENV="+v)
}

// appendWSLENV adds an entry to WSLENV's colon-separated list once.
func appendWSLENV(cur, entry string) string {
	for _, e := range strings.Split(cur, ":") {
		if e == entry {
			return cur
		}
	}
	if cur == "" {
		return entry
	}
	return cur + ":" + entry
}
