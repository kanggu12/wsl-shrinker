//go:build windows

package wsl

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

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
	cmd := exec.CommandContext(ctx, "wsl.exe", args...)
	// Ask for UTF-8 where wsl.exe honours it, and keep any window hidden.
	cmd.Env = append(os.Environ(), "WSL_UTF8=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	text := decode(out.Bytes())
	if err != nil {
		return text, fmt.Errorf("wsl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(text))
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
	running := map[string]bool{}
	// Without running distributions wsl.exe exits non-zero; that is no error.
	if out, err := wslExe(ctx, "--list", "--running", "--quiet"); err == nil {
		for _, l := range lines(out) {
			running[l] = true
		}
	}
	var out []Distro
	for _, id := range ids {
		k, err := registry.OpenKey(root, id, registry.READ)
		if err != nil {
			continue
		}
		name, _, _ := k.GetStringValue("DistributionName")
		base, _, _ := k.GetStringValue("BasePath")
		file, _, _ := k.GetStringValue("VhdFileName")
		ver, _, _ := k.GetIntegerValue("Version")
		k.Close()
		// Entries without a name are leftovers of failed installs.
		if name == "" {
			continue
		}
		d := Distro{Name: name, ID: id, Version: int(ver), Default: id == def, Running: running[name]}
		if d.Version == 2 {
			// The file name is ext4.vhdx unless the registration says otherwise.
			if file == "" {
				file = "ext4.vhdx"
			}
			// Store installs record the base path with a \\?\ prefix.
			d.VHD = filepath.Join(strings.TrimPrefix(base, `\\?\`), file)
		}
		out = append(out, d)
	}
	return out, nil
}

// Info runs `wsl --version` and `wsl --help`.
func (Real) Info(ctx context.Context) (Info, error) {
	v, err := wslExe(ctx, "--version")
	if err != nil {
		// Old inbox WSL has no --version; it has no --compact either.
		return Info{}, nil
	}
	help, _ := wslExe(ctx, "--help")
	return Info{Version: parseVersion(v), CanCompact: hasCompact(help)}, nil
}

// Run runs the script with sh, inside a distribution that already runs.
func (r Real) Run(ctx context.Context, distro, user, script string) (string, error) {
	// Refuse rather than boot a stopped distribution as a side effect.
	ds, err := r.Distros(ctx)
	if err != nil {
		return "", err
	}
	for _, d := range ds {
		if d.Name == distro && !d.Running {
			return "", fmt.Errorf("%s is not running", distro)
		}
	}
	return wslExe(ctx, "--distribution", distro, "--user", user, "--exec", "sh", "-c", script)
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
