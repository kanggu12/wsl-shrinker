package shrink

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/kanggu12/wsl-shrinker/internal/engine/disks"
	"github.com/kanggu12/wsl-shrinker/internal/engine/survey"
)

// kind is the disk a matrix cell runs on.
type kind string

const (
	// running is a distribution that runs, with two containers.
	running kind = "running"
	// stopped is a distribution that does not run.
	stopped kind = "stopped"
	// file is a file WSL does not register, as Docker Desktop's disk.
	file kind = "file"
)

// leftAs is how a run must leave things in one cell of the matrix.
type leftAs struct {
	// failed: the run reports a failure.
	failed bool
	// running: the distribution runs afterwards (distributions only).
	running bool
	// containers: its stopped containers were started again.
	containers bool
	// restart is the restart step's last status, "" when it has none.
	restart Status
	// left is what the result says about the distribution afterwards.
	left Left
}

// breaks sets up one way a run fails, on a world built for kind k. The
// "after" flag a break sets is the compaction having happened.
var breaks = map[string]func(w *world, k kind){
	// Nothing goes wrong.
	"none": func(*world, kind) {},
	// Removing the chosen items fails: nothing was stopped yet.
	"clean": func(w *world, _ kind) {
		inner := w.fake.Script
		w.fake.Script = func(a, b, s string) (string, error) {
			if strings.Contains(s, "apt-get clean") {
				return "", errors.New("wsl did not answer")
			}
			return inner(a, b, s)
		}
	},
	// The stop runs out of time after docker stop: the containers it
	// stopped are known, the distribution still runs.
	"stop": func(w *world, _ kind) {
		inner := w.fake.Script
		w.fake.Script = func(a, b, s string) (string, error) {
			// The containers are named, then stopping them runs out of time.
			if strings.Contains(s, "pre-stop.d") {
				return "", errors.New("context deadline exceeded")
			}
			return inner(a, b, s)
		}
	},
	// The compaction fails before WSL stopped the distribution.
	"compact-early": func(w *world, k kind) {
		w.fake.Fail = map[string]error{"compact:Dev": errors.New("E_ACCESSDENIED")}
		w.env.CompactFile = func(string) error { return errors.New("E_ACCESSDENIED") }
	},
	// The compaction fails once WSL has stopped the distribution.
	"compact-late": func(w *world, k kind) {
		w.fake.OnCompact = func(string) error { return errors.New("compaction failed") }
		w.env.CompactFile = func(string) error { return errors.New("compaction failed") }
	},
	// The file cannot be opened after the compaction (another program has
	// it a moment): nothing says the disk changed.
	"verify-open": func(w *world, k kind) {
		compacted := false
		w.fake.OnCompact = func(string) error { compacted, w.pad = true, 0; return nil }
		w.env.CompactFile = func(string) error { compacted, w.pad = true, 0; return nil }
		open := w.env.Disks.Open
		w.env.Disks.Open = func(p string) (disks.ReaderAtCloser, int64, error) {
			if compacted {
				return nil, 0, errors.New("the process cannot access the file")
			}
			return open(p)
		}
	},
	// The file no longer reads as a VHDX after the compaction.
	"verify-suspect": func(w *world, k kind) {
		damage := func(string) error {
			f, err := os.OpenFile(w.path, os.O_RDWR, 0)
			if err != nil {
				return err
			}
			defer f.Close()
			w.pad = 0
			_, err = f.WriteAt([]byte("XXXXXXXX"), 0)
			return err
		}
		w.fake.OnCompact = damage
		w.env.CompactFile = damage
	},
	// The distribution does not start again.
	"restart": func(w *world, _ kind) {
		w.fake.Fail = map[string]error{"start:Dev": errors.New("WSL_E_VM_MODE_INVALID_STATE")}
	},
	// The engine's own code panics after the compaction (a bug of ours,
	// not the disk's): the run must still settle what it stopped.
	"panic": func(w *world, k kind) {
		compacted := false
		w.fake.OnCompact = func(string) error { compacted, w.pad = true, 0; return nil }
		w.env.CompactFile = func(string) error { compacted, w.pad = true, 0; return nil }
		open := w.env.Disks.Open
		w.env.Disks.Open = func(p string) (disks.ReaderAtCloser, int64, error) {
			if compacted {
				panic("a bug while reading the disk again")
			}
			return open(p)
		}
	},
}

// matrix is the expected end of every break on every kind of disk.
var matrix = map[string]map[kind]leftAs{
	"none": {
		running: {running: true, containers: true, restart: Done, left: LeftRunning},
		stopped: {restart: Skipped, left: LeftStopped},
		file:    {restart: Skipped, left: LeftFile},
	},
	"clean": {
		running: {failed: true, running: true, left: LeftRunning},
		stopped: {restart: Skipped, left: LeftStopped},
		file:    {restart: Skipped, left: LeftFile},
	},
	"stop": {
		running: {failed: true, running: true, containers: true, restart: Done, left: LeftRunning},
		stopped: {restart: Skipped, left: LeftStopped},
		file:    {restart: Skipped, left: LeftFile},
	},
	"compact-early": {
		running: {failed: true, running: true, containers: true, restart: Done, left: LeftRunning},
		stopped: {failed: true, left: LeftStopped},
		file:    {failed: true, left: LeftFile},
	},
	"compact-late": {
		running: {failed: true, running: true, containers: true, restart: Done, left: LeftRunning},
		stopped: {failed: true, left: LeftStopped},
		file:    {failed: true, left: LeftFile},
	},
	"verify-open": {
		running: {failed: true, running: true, containers: true, restart: Done, left: LeftRunning},
		stopped: {failed: true, left: LeftStopped},
		file:    {failed: true, left: LeftFile},
	},
	"verify-suspect": {
		running: {failed: true, restart: Skipped, left: LeftSuspect},
		stopped: {failed: true, left: LeftSuspect},
		file:    {failed: true, left: LeftSuspect},
	},
	"restart": {
		running: {failed: true, restart: Failed, left: LeftDown},
		stopped: {restart: Skipped, left: LeftStopped},
		file:    {restart: Skipped, left: LeftFile},
	},
	"panic": {
		running: {failed: true, running: true, containers: true, restart: Done, left: LeftRunning},
		stopped: {failed: true, left: LeftStopped},
		file:    {failed: true, left: LeftFile},
	},
}

func TestRecoveryMatrix(t *testing.T) {
	// For every point a run can fail at and every kind of disk: whether the
	// distribution runs afterwards, whether its containers came back, what
	// the restart step said, and what the result says was left. Any change
	// to how failures are handled must keep this table.
	for name, cells := range matrix {
		for k, want := range cells {
			t.Run(name+"/"+string(k), func(t *testing.T) {
				w := newWorld(t)
				if k == stopped {
					w.fake.List[0].Running = false
				}
				d := w.disk()
				if k == file {
					// Docker Desktop's disk: no distribution, read as a file.
					d.Distro, d.Kind, d.Running = "", disks.KindDocker, false
					disks.Inspect(w.env.Disks, &d)
				}
				breaks[name](w, k)
				// Which scripts ran, to see the containers start again.
				var scripts []string
				inner := w.fake.Script
				w.fake.Script = func(a, b, s string) (string, error) {
					scripts = append(scripts, s)
					return inner(a, b, s)
				}
				items := []survey.Item{{ID: "apt", What: "apt", Command: "apt-get clean", Bytes: 1}}
				r := Run(context.Background(), w.env, d, Options{Clean: items}, nil)
				started := false
				for _, s := range scripts {
					started = started || strings.Contains(s, "docker start")
				}
				restart := Status("")
				if e, ok := lastEvent(r, StepRestart); ok {
					restart = e.Status
				}
				got := leftAs{failed: r.Err != "", containers: started, restart: restart, left: r.Left}
				if k != file {
					got.running = w.fake.List[0].Running
				}
				if got != want {
					t.Fatalf("got %+v\nwant %+v\nerror %q, events %v", got, want, r.Err, statuses(r))
				}
				// A file WSL does not register is never started or stopped.
				if k == file {
					for _, c := range w.fake.CallList() {
						if strings.HasPrefix(c, "start:") || strings.HasPrefix(c, "terminate:") || strings.HasPrefix(c, "compact:") {
							t.Fatalf("WSL was asked to %s for a file", c)
						}
					}
				}
			})
		}
	}
}
