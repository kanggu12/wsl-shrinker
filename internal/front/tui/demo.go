package tui

import (
	"context"
	"time"

	"github.com/kanggu12/wsl-shrinker/internal/engine/disks"
	"github.com/kanggu12/wsl-shrinker/internal/engine/estimate"
	"github.com/kanggu12/wsl-shrinker/internal/engine/history"
	"github.com/kanggu12/wsl-shrinker/internal/engine/journal"
	"github.com/kanggu12/wsl-shrinker/internal/engine/shrink"
	"github.com/kanggu12/wsl-shrinker/internal/engine/survey"
	"github.com/kanggu12/wsl-shrinker/internal/machine/winapi"
	"github.com/kanggu12/wsl-shrinker/internal/machine/wsl"
)

// gb is a gigabyte as Windows counts it.
const gb = int64(1) << 30

// Demo is a made-up PC with a few WSL disks, for trying the screen and for
// the screenshots. Its runs only pretend; nothing on the real PC is read.
type Demo struct {
	// Fast skips the pauses between steps (snapshots and tests).
	Fast bool
	list []disks.Disk
	hist []history.Entry
}

// NewDemo returns the made-up PC.
func NewDemo() *Demo {
	at := func(file, after, used int64) *estimate.Result {
		return &estimate.Result{FileSize: file, After: after, Reclaim: file - after, Used: used, Stored: after, Filesystem: true}
	}
	ubuntu := at(312*gb, 248*gb, 231*gb)
	ubuntu.InUse, ubuntu.Mounted = true, true
	docker := at(88*gb, 52*gb, 49*gb)
	docker.InUse = true
	d := &Demo{list: []disks.Disk{
		{Name: "Ubuntu-24.04", Kind: disks.KindDistro, Distro: "Ubuntu-24.04", Default: true, Running: true,
			Path: `C:\Users\ann\AppData\Local\Packages\CanonicalGroupLimited.Ubuntu24.04LTS_79rhkp1fndgsc\LocalState\ext4.vhdx`,
			File: winapi.FileInfo{Size: 312 * gb, OnDisk: 312 * gb, Held: true, DriveFree: 61 * gb}, Estimate: ubuntu},
		{Name: "Docker Desktop data", Kind: disks.KindDocker,
			Path: `C:\Users\ann\AppData\Local\Docker\wsl\disk\docker_data.vhdx`,
			File: winapi.FileInfo{Size: 88 * gb, OnDisk: 88 * gb, Held: true, DriveFree: 61 * gb}, Estimate: docker},
		{Name: "Debian", Kind: disks.KindDistro, Distro: "Debian",
			Path: `D:\WSL\Debian\ext4.vhdx`, File: winapi.FileInfo{Size: 41 * gb, OnDisk: 41 * gb, DriveFree: 402 * gb}, Estimate: at(41*gb, 18*gb, 16*gb)},
		{Name: "kali-linux", Kind: disks.KindDistro, Distro: "kali-linux",
			Path: `D:\WSL\kali\ext4.vhdx`, File: winapi.FileInfo{Size: 9 * gb, OnDisk: 9 * gb, DriveFree: 402 * gb}, Estimate: at(9*gb, 9*gb-300<<20, 8*gb)},
		{Name: "Work", Kind: disks.KindDistro, Distro: "Work",
			Path: `D:\WSL\Work\ext4.vhdx`, File: winapi.FileInfo{Size: 64 * gb, OnDisk: 64 * gb, DriveFree: 402 * gb}, Estimate: at(64*gb, 51*gb, 49*gb)},
	}}
	// Past runs at fixed times, in the local zone: the README's pictures of
	// the demo come out the same each time they are made.
	d.hist = []history.Entry{
		{At: time.Date(2026, 10, 18, 9, 12, 0, 0, time.Local), Disk: "Ubuntu-24.04", Before: 274 * gb, After: 219 * gb, Freed: 55 * gb, Took: 48},
		{At: time.Date(2026, 10, 30, 21, 40, 0, 0, time.Local), Disk: "Debian", Before: 37 * gb, After: 17 * gb, Freed: 20 * gb, Took: 21},
	}
	return d
}

// Info is a recent WSL.
func (d *Demo) Info(context.Context) wsl.Info { return wsl.Info{Version: "3.0.3.0", CanCompact: true} }

// Disks returns the made-up disks.
func (d *Demo) Disks(context.Context) ([]disks.Disk, error) {
	if !d.Fast {
		time.Sleep(300 * time.Millisecond)
	}
	return append([]disks.Disk(nil), d.list...), nil
}

// Survey returns what fills the running distribution.
func (d *Demo) Survey(context.Context, string) (survey.Result, error) {
	if !d.Fast {
		time.Sleep(700 * time.Millisecond)
	}
	it := func(id, what, label string, n int64, k survey.Kind, note string) survey.Item {
		return survey.Item{ID: id, What: what, Label: label, Bytes: n, Kind: k, Note: note, Command: "true"}
	}
	mb := int64(1) << 20
	return survey.Result{Docker: true, Containers: 3, Items: []survey.Item{
		it("docker-build-cache", "docker-build-cache", "Docker build cache", 14*gb+800*mb, survey.KindCache, ""),
		it("go-mod:/home/ann/go/pkg/mod", "go-mod", "/home/ann/go/pkg/mod", 6*gb+100*mb, survey.KindDownload, ""),
		it("huggingface:/home/ann/.cache/huggingface/hub", "huggingface", "/home/ann/.cache/huggingface/hub", 4*gb+200*mb, survey.KindDownload, ""),
		it("npm:/home/ann/.npm/_cacache", "npm", "/home/ann/.npm/_cacache", 3*gb+900*mb, survey.KindCache, ""),
		it("docker-dangling", "docker-dangling", "Docker images with no name", 3*gb+400*mb, survey.KindImage, ""),
		it("pip:/home/ann/.cache/pip", "pip", "/home/ann/.cache/pip", 2*gb+700*mb, survey.KindCache, ""),
		it("image:4f8a2c1d9e7b", "image", "node:22", 1*gb+100*mb, survey.KindImage, "5 weeks ago"),
		it("go-build:/home/ann/.cache/go-build", "go-build", "/home/ann/.cache/go-build", 900*mb, survey.KindCache, ""),
		it("journal", "journal", "/var/log/journal", 800*mb, survey.KindLog, ""),
		it("image:9b1e0c7a3f52", "image", "python:3.12-slim", 420*mb, survey.KindImage, "2 months ago"),
		it("apt", "apt", "/var/cache/apt/archives", 310*mb, survey.KindCache, ""),
	}}, nil
}

// Shrink plays the steps with pauses like a real run's.
func (d *Demo) Shrink(_ context.Context, disk disks.Disk, opt shrink.Options, emit func(shrink.Event)) shrink.Result {
	res := shrink.Result{Disk: disk.Name, Before: disk.Estimate}
	pause := func(ms int) {
		if !d.Fast {
			time.Sleep(time.Duration(ms) * time.Millisecond)
		}
	}
	// The run's length as the demo tells it, the same with or without the
	// pauses: each step's pause, but the compaction at the length its step
	// says, not the shorter pause that stands in for it.
	var took time.Duration
	step := func(ms int, done shrink.Event) {
		e := shrink.Event{Step: done.Step, Status: shrink.Running, At: time.Now()}
		emit(e)
		res.Events = append(res.Events, e)
		pause(ms)
		if done.Took > 0 {
			took += done.Took
		} else {
			took += time.Duration(ms) * time.Millisecond
		}
		done.At = time.Now()
		emit(done)
		res.Events = append(res.Events, done)
	}
	var cleaned int64
	for _, it := range opt.Clean {
		cleaned += it.Bytes
	}
	// What the run gives back with what was chosen, as the engine says it.
	step(400, shrink.Event{Step: shrink.StepCheck, Status: shrink.Done, Bytes: disk.Estimate.Reclaim + cleaned})
	if len(opt.Clean) > 0 {
		step(1500, shrink.Event{Step: shrink.StepClean, Status: shrink.Done, Count: len(opt.Clean), Bytes: cleaned})
	} else {
		emit(shrink.Event{Step: shrink.StepClean, Status: shrink.Skipped, At: time.Now()})
	}
	if disk.Running {
		step(1800, shrink.Event{Step: shrink.StepStop, Status: shrink.Done, Count: 3, Hooks: 1})
	} else {
		emit(shrink.Event{Step: shrink.StepStop, Status: shrink.Skipped, At: time.Now()})
	}
	step(4200, shrink.Event{Step: shrink.StepCompact, Status: shrink.Done, Took: 38400 * time.Millisecond})
	freed := disk.Estimate.Reclaim + cleaned
	step(700, shrink.Event{Step: shrink.StepVerify, Status: shrink.Done, Bytes: freed})
	if disk.Running {
		step(1600, shrink.Event{Step: shrink.StepRestart, Status: shrink.Done, Count: 3})
	} else {
		emit(shrink.Event{Step: shrink.StepRestart, Status: shrink.Skipped, At: time.Now()})
	}
	// Left as the engine leaves it: a distribution that ran runs again,
	// with the three containers it stopped, so the screen says what it
	// says after a real run.
	if disk.Running {
		res.Left, res.ContainersStarted, res.DistroStarted = shrink.LeftRunning, 3, true
	}
	after := disk.File
	after.OnDisk = disk.Estimate.FileSize - freed
	res.File, res.Freed, res.Took = &after, freed, took
	d.hist = append(d.hist, history.Entry{At: time.Now(), Disk: disk.Name, Before: disk.Estimate.FileSize, After: after.OnDisk, Freed: freed, Took: res.Took.Seconds()})
	// The disk shows what it is now.
	for i := range d.list {
		if d.list[i].Name == disk.Name {
			e := *d.list[i].Estimate
			e.FileSize, e.Reclaim = after.OnDisk, 0
			e.After = after.OnDisk
			d.list[i].Estimate = &e
			// The estimate read again, as the engine's result carries it.
			res.After = &e
		}
	}
	pause(300)
	return res
}

// History returns the made-up past runs.
func (d *Demo) History() []history.Entry { return append([]history.Entry(nil), d.hist...) }

// Protected protects the disk named Work, as the settings could.
func (d *Demo) Protected(name string) bool { return name == "Work" }

// MinGain is 1 GB.
func (d *Demo) MinGain() int64 { return gb }

// Hosts is false: the demo runs anywhere.
func (d *Demo) Hosts(string) bool { return false }

// Reopen does nothing in the demo.
func (d *Demo) Reopen([]string) error { return nil }

// Pending: the made-up PC has no run left unfinished.
func (d *Demo) Pending() (journal.Entry, journal.State) { return journal.Entry{}, journal.None }

// Recover has nothing to finish on the made-up PC.
func (d *Demo) Recover(context.Context) (string, error) { return "", nil }
