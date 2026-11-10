//go:build windows

package wsl

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows/registry"
)

// lxssKey is where WSL registers each user's distributions.
const lxssKey = `Software\Microsoft\Windows\CurrentVersion\Lxss`

// Real is the System of this PC.
type Real struct{}

// New returns the System of this PC.
func New() System { return Real{} }

// wslExe runs wsl.exe without a console window of its own and returns its
// decoded output; the error carries that output, which says what went wrong.
func wslExe(ctx context.Context, args ...string) (string, error) {
	// A call with no limit of its own gets one: a WSL that never answers
	// must not hold the screen or a command forever.
	ctx, cancel := limited(ctx)
	defer cancel()
	limit := timeLeft(ctx)
	cmd := exec.CommandContext(ctx, "wsl.exe", args...)
	// Ask for UTF-8 where wsl.exe honours it, and keep any window hidden.
	cmd.Env = append(os.Environ(), "WSL_UTF8=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	// Ended at its limit, wsl.exe may leave something holding its output
	// open; the call returns a few seconds later all the same.
	cmd.WaitDelay = 5 * time.Second
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	text := decode(out.Bytes())
	if err != nil {
		// Out of time, it said nothing that counts as an answer.
		if na := noAnswer(ctx, args, limit); na != nil {
			return text, na
		}
		return text, fmt.Errorf("wsl %s: %w: %s", describe(args), err, strings.TrimSpace(text))
	}
	return text, nil
}

// Distros reads the registry and asks wsl.exe which distributions run.
func (Real) Distros(ctx context.Context) ([]Distro, error) {
	root, err := registry.OpenKey(registry.CURRENT_USER, lxssKey, registry.READ)
	if err != nil {
		// No key: WSL has no distribution for this user.
		if err == registry.ErrNotExist {
			return nil, nil
		}
		return nil, fmt.Errorf("reading the WSL registry: %w", err)
	}
	defer root.Close()
	def, _, _ := root.GetStringValue("DefaultDistribution")
	ids, err := root.ReadSubKeyNames(-1)
	if err != nil {
		return nil, fmt.Errorf("listing the WSL registry: %w", err)
	}
	var entries []regEntry
	for _, id := range ids {
		k, err := registry.OpenKey(root, id, registry.READ)
		// A subkey that cannot be opened is skipped, as if it were absent.
		if err != nil {
			continue
		}
		// Missing values read as empty or zero; buildDistros judges them.
		name, _, _ := k.GetStringValue("DistributionName")
		base, _, _ := k.GetStringValue("BasePath")
		file, _, _ := k.GetStringValue("VhdFileName")
		// A missing Flags is WSL's default flags, which make WSL 1; one
		// that is there but is not a number is noted as unread.
		flags, _, ferr := k.GetIntegerValue("Flags")
		ver, _, _ := k.GetIntegerValue("Version")
		k.Close()
		entries = append(entries, regEntry{id: id, name: name, base: base, file: file, flags: uint32(flags),
			flagsUnread: ferr != nil && ferr != registry.ErrNotExist, version: uint32(ver)})
	}
	// Building the list and asking which run is the part tested off Windows.
	return buildDistros(ctx, entries, def, wslExe)
}

// Info runs `wsl --version` and `wsl --help`.
func (Real) Info(ctx context.Context) (Info, error) {
	return info(ctx, wslExe)
}

// Run runs the script with sh, inside a distribution that already runs.
func (r Real) Run(ctx context.Context, distro, user, script string) (string, error) {
	// Refuse rather than boot a stopped distribution as a side effect, and
	// refuse a name WSL does not have before asking wsl.exe anything.
	ds, err := r.Distros(ctx)
	if err != nil {
		return "", err
	}
	d, err := find(ds, distro)
	if err != nil {
		return "", err
	}
	if !d.Running {
		return "", fmt.Errorf("%s is %w", d.Name, ErrNotRunning)
	}
	// The registered spelling of the name, whatever case was asked for.
	return wslExe(ctx, "--distribution", d.Name, "--user", user, "--exec", "sh", "-c", script)
}

// Start runs `true` in the distribution, which boots it.
func (Real) Start(ctx context.Context, distro string) error {
	_, err := wslExe(ctx, "--distribution", distro, "--exec", "true")
	return err
}

// Terminate stops the distribution.
func (Real) Terminate(ctx context.Context, distro string) error {
	_, err := wslExe(ctx, "--terminate", distro)
	return err
}

// Compact runs WSL's own compaction of the distribution's disk.
func (Real) Compact(ctx context.Context, distro string) error {
	_, err := wslExe(ctx, "--manage", distro, "--compact")
	return err
}
