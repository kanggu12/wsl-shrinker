// Package app puts the parts together for this PC: the settings, WSL, the
// disk files, the history, and the backend the screen uses.
package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/kanggu12/wsl-shrinker/internal/engine/disks"
	"github.com/kanggu12/wsl-shrinker/internal/engine/history"
	"github.com/kanggu12/wsl-shrinker/internal/engine/shrink"
	"github.com/kanggu12/wsl-shrinker/internal/engine/survey"
	"github.com/kanggu12/wsl-shrinker/internal/machine/winapi"
	"github.com/kanggu12/wsl-shrinker/internal/machine/wsl"
	"github.com/kanggu12/wsl-shrinker/internal/settings/config"
)

// App is the program's parts for this PC.
type App struct {
	Config     config.Config
	ConfigPath string
	WSL        wsl.System
	History    history.Store
	// info is WSL's answer about itself, asked once.
	info     wsl.Info
	infoOnce sync.Once
}

// New reads the settings and sets up the parts. A settings file that does
// not load is an error: running with defaults the person did not choose
// would be a surprise.
func New() (*App, error) {
	path := config.Path(winapi.AppData())
	c, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	return &App{Config: c, ConfigPath: path, WSL: wsl.New(),
		History: history.Open(filepath.Join(winapi.LocalAppData(), "wsl-shrinker"))}, nil
}

// Info asks WSL about itself, once.
func (a *App) Info(ctx context.Context) wsl.Info {
	a.infoOnce.Do(func() { a.info, _ = a.WSL.Info(ctx) })
	return a.info
}

// DisksEnv is what disk discovery reads from.
func (a *App) DisksEnv() disks.Env {
	return disks.Env{WSL: a.WSL, Stat: winapi.Stat, Open: disks.OpenFile,
		LocalAppData: winapi.LocalAppData(), Extra: a.Config.ExtraDisks}
}

// ShrinkEnv is what a run uses.
func (a *App) ShrinkEnv(ctx context.Context) shrink.Env {
	return shrink.Env{Disks: a.DisksEnv(), CompactFile: winapi.CompactFile, CanCompact: a.Info(ctx).CanCompact}
}

// Shrink runs one disk and records it in the history.
func (a *App) Shrink(ctx context.Context, d disks.Disk, opt shrink.Options, emit func(shrink.Event)) shrink.Result {
	res := shrink.Run(ctx, a.ShrinkEnv(ctx), d, opt, emit)
	e := history.Entry{At: timeNow(), Disk: res.Disk, Freed: res.Freed, Took: res.Took.Seconds(), Err: res.Err}
	if res.Before != nil {
		e.Before = res.Before.FileSize
	}
	if res.File != nil {
		e.After = res.File.OnDisk
	}
	for _, it := range opt.Clean {
		e.Cleaned += it.Bytes
	}
	// A run stopped by its first check changed nothing: it is not history.
	if len(res.Events) > 2 || res.Err == "" {
		_ = a.History.Add(e)
	}
	return res
}

// Backend is the screen's view of the App.
type Backend struct {
	A *App
	// Reopened is set in the window a run moved to from inside WSL.
	Reopened bool
}

// Info is WSL's version.
func (b Backend) Info(ctx context.Context) wsl.Info { return b.A.Info(ctx) }

// Disks finds the disks.
func (b Backend) Disks(ctx context.Context) ([]disks.Disk, error) {
	return disks.Find(ctx, b.A.DisksEnv())
}

// Survey looks inside a distribution.
func (b Backend) Survey(ctx context.Context, distro string) (survey.Result, error) {
	return survey.Run(ctx, b.A.WSL, distro)
}

// Shrink runs a disk.
func (b Backend) Shrink(ctx context.Context, d disks.Disk, opt shrink.Options, emit func(shrink.Event)) shrink.Result {
	return b.A.Shrink(ctx, d, opt, emit)
}

// History is the past runs.
func (b Backend) History() []history.Entry {
	all, _ := b.A.History.All()
	return all
}

// Protected follows the settings.
func (b Backend) Protected(name string) bool { return b.A.Config.Protected(name) }

// MinGain follows the settings.
func (b Backend) MinGain() int64 { return int64(b.A.Config.MinGainGB * (1 << 30)) }

// Hosts reports whether this program's terminal runs inside distro. The
// window a run moved to never moves again.
func (b Backend) Hosts(distro string) bool { return !b.Reopened && Hosts(distro) }

// Hosts reports whether this process was started from inside distro. Started
// from WSL, the launcher there passes its distribution's name through
// WSLENV; without it, any distribution may be this terminal's.
func Hosts(distro string) bool {
	if !winapi.FromWSL() {
		return false
	}
	here := os.Getenv("WSL_DISTRO_NAME")
	return here == "" || strings.EqualFold(here, distro)
}

// Reopen starts this program again in a new window.
func (b Backend) Reopen(args []string) error { return winapi.Reopen(args) }
