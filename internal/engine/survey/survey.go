// Package survey looks inside a running distribution for space that can go:
// caches tools rebuild, the system journal, and Docker's build cache and
// images. Each finding carries the one command that frees it.
//
// What can be removed is a fixed list in this file. Nothing else is ever
// removed: no Docker volume, no container, no file outside these paths. The
// tests check that no command here can touch those.
package survey

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kanggu12/wsl-shrinker/internal/machine/wsl"
)

// Kind groups findings by what removing them costs.
type Kind string

const (
	// KindCache is rebuilt on its own the next time a tool needs it.
	KindCache Kind = "cache"
	// KindDownload comes back only by downloading it again (models, browsers).
	KindDownload Kind = "download"
	// KindLog is history that is not rebuilt.
	KindLog Kind = "log"
	// KindImage is a Docker image, pulled or built again when needed.
	KindImage Kind = "image"
)

// Item is one thing that can be removed.
type Item struct {
	// ID is unique within one survey, like "pip:/home/ann" or "image:sha256…".
	ID string `json:"id"`
	// What names the kind of thing ("pip", "docker-build-cache", "image").
	What string `json:"what"`
	// Label describes it on screen: a path, or an image's name.
	Label string `json:"label"`
	// Bytes is how much space it takes.
	Bytes int64 `json:"bytes"`
	// Kind says what removing it costs.
	Kind Kind `json:"kind"`
	// Command removes it; it runs as root with sh.
	Command string `json:"-"`
	// Note is extra context ("used by a container", "3 weeks old").
	Note string `json:"note,omitempty"`
	// InUse is set for an image a container uses: Docker refuses to remove it.
	InUse bool `json:"in_use,omitempty"`
}

// Result is what one survey found.
type Result struct {
	// Size, Used and Avail are the root filesystem's numbers, in bytes.
	Size  int64 `json:"size_bytes"`
	Used  int64 `json:"used_bytes"`
	Avail int64 `json:"avail_bytes"`
	// Options are the root mount's options ("rw,discard,…").
	Options string `json:"options"`
	// Items are the findings, largest first.
	Items []Item `json:"items"`
	// Docker is set when a Docker engine answered inside the distribution.
	Docker bool `json:"docker"`
	// Containers counts the containers running now, which a shrink stops
	// and starts again.
	Containers int `json:"containers"`
	// DockerDesktop is set when the docker command here is Docker Desktop's
	// integration: its images are on Docker Desktop's own disk.
	DockerDesktop bool `json:"docker_desktop"`
}

// cache is one known cache under a home directory.
type cache struct {
	what, rel string
	kind      Kind
}

// caches are the per-user caches looked for under /root and /home/*. Each is
// a directory its tool recreates; removing it costs a slower first run, or a
// download for KindDownload.
var caches = []cache{
	{"pip", ".cache/pip", KindCache},
	{"npm", ".npm/_cacache", KindCache},
	{"yarn", ".cache/yarn", KindCache},
	{"go-build", ".cache/go-build", KindCache},
	{"go-mod", "go/pkg/mod", KindDownload},
	{"cargo", ".cargo/registry/cache", KindCache},
	// The models and datasets only: the folder above also holds the login
	// token and accelerate's settings, which no download brings back.
	{"huggingface", ".cache/huggingface/hub", KindDownload},
	{"playwright", ".cache/ms-playwright", KindDownload},
	{"trash", ".local/share/Trash", KindLog},
}

// script prints one line per finding. It reads only; du's sizes are in KiB
// because BusyBox's du (Alpine) has no byte option.
func script() string {
	var b strings.Builder
	// The root filesystem's numbers and mount options.
	b.WriteString(`df -Pk / 2>/dev/null | awk 'NR==2{print "fs|"$2"|"$3"|"$4}'` + "\n")
	b.WriteString(`awk '$2=="/"{print "opts|"$4}' /proc/mounts | tail -1` + "\n")
	// The package cache and the journal.
	b.WriteString(`[ -d /var/cache/apt/archives ] && echo "sys|apt|/var/cache/apt/archives|$(du -sk /var/cache/apt/archives 2>/dev/null | cut -f1)"` + "\n")
	b.WriteString(`[ -d /var/log/journal ] && echo "sys|journal|/var/log/journal|$(du -sk /var/log/journal 2>/dev/null | cut -f1)"` + "\n")
	// Every home directory's caches. A cache counts only where it really is:
	// no link anywhere on its path (readlink -f gives the path back as it is)
	// and on the root filesystem, the one the disk file holds. A cache moved
	// to another drive and linked takes no space in the disk anyway, and
	// following a link would show, and later remove, somewhere else.
	b.WriteString("root_dev=$(stat -c %d /)\n")
	b.WriteString("for h in /root /home/*; do [ -d \"$h\" ] || continue\n")
	for _, c := range caches {
		fmt.Fprintf(&b, "  p=\"$h/%s\"; [ -d \"$p\" ] && [ \"$(readlink -f \"$p\")\" = \"$p\" ] && [ \"$(stat -c %%d \"$p\")\" = \"$root_dev\" ] && echo \"home|%s|$p|$(du -skx \"$p\" 2>/dev/null | cut -f1)\"\n", c.rel, c.what)
	}
	b.WriteString("done\n")
	// Docker: what the build cache holds, and every image with the space only
	// it holds and the containers using it. Sizes as Docker prints them.
	// Docker Desktop's WSL integration puts its own docker command here: its
	// images live on Docker Desktop's disk, not this one, so they are not
	// offered (and nothing here stops its containers).
	b.WriteString(`if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1 && docker info --format '{{.OperatingSystem}}' 2>/dev/null | grep -q 'Docker Desktop'; then
  echo "docker|desktop"
elif command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
  echo "docker|on"
  echo "containers|$(docker ps -q | wc -l)"
  docker system df --format '{{.Type}}|{{.Reclaimable}}' 2>/dev/null | sed 's/^/df|/'
  # Per image: the space only it holds (shared layers stay with the other
  # images) and how many containers use it.
  docker image ls --filter dangling=true --quiet --no-trunc 2>/dev/null | sed 's/^/dangling|/'
  docker system df -v --format '{{range .Images}}{{.ID}}|{{.Repository}}:{{.Tag}}|{{.UniqueSize}}|{{.CreatedSince}}|{{.Containers}}{{println}}{{end}}' 2>/dev/null | sed -n 's/^\(.\)/img|\1/p'
fi
`)
	return b.String()
}

// Limits for the work inside a distribution: a Docker that takes the
// connection and never answers must not hold the screen or a run forever.
const (
	surveyLimit = 3 * time.Minute
	cleanLimit  = 30 * time.Minute
)

// Run surveys a running distribution.
func Run(ctx context.Context, sys wsl.System, distro string) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, surveyLimit)
	defer cancel()
	out, err := sys.Run(ctx, distro, "root", script())
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return Result{}, fmt.Errorf("the look inside took over %s: %w", surveyLimit, ctx.Err())
	}
	if err != nil {
		return Result{}, err
	}
	return parse(out), nil
}

// parse reads the script's lines.
func parse(out string) Result {
	var r Result
	// Images `docker image prune` removes: unnamed and not the parent of
	// another. Unnamed parents are listed too, but prune leaves them.
	dangling := map[string]bool{}
	seenImage := map[string]bool{}
	for line := range strings.SplitSeq(strings.ReplaceAll(out, "\r", ""), "\n") {
		f := strings.Split(strings.TrimSpace(line), "|")
		switch {
		case f[0] == "fs" && len(f) == 4:
			r.Size, r.Used, r.Avail = kib(f[1]), kib(f[2]), kib(f[3])
		case f[0] == "opts" && len(f) == 2:
			r.Options = f[1]
		case f[0] == "sys" && len(f) == 4:
			r.add(system(f[1], f[2], kib(f[3])))
		case f[0] == "home" && len(f) == 4:
			r.add(home(f[1], f[2], kib(f[3])))
		case f[0] == "docker" && len(f) == 2 && f[1] == "desktop":
			r.DockerDesktop = true
		case f[0] == "docker":
			r.Docker = true
		case f[0] == "containers" && len(f) == 2:
			r.Containers, _ = strconv.Atoi(strings.TrimSpace(f[1]))
		case f[0] == "df" && len(f) == 3 && f[1] == "Build Cache" && len(strings.Fields(f[2])) > 0:
			// "1.2GB" or "1.2GB (100%)": the reclaimable part of the cache.
			n := dockerSize(strings.Fields(f[2])[0])
			r.add(Item{ID: "docker-build-cache", What: "docker-build-cache", Label: "Docker build cache", Bytes: n,
				Kind: KindCache, Command: "docker builder prune --all --force"})
		case f[0] == "img" && len(f) == 6:
			// One item per image: a Docker that lists an image once per tag
			// would otherwise count and offer it twice.
			if it := image(f[1:]); !seenImage[it.ID] {
				seenImage[it.ID] = true
				r.add(it)
			}
		case f[0] == "dangling" && len(f) == 2:
			dangling[strings.TrimPrefix(strings.TrimSpace(f[1]), "sha256:")[:min(12, len(strings.TrimPrefix(strings.TrimSpace(f[1]), "sha256:")))]] = true
		}
	}
	// Unnamed images go as one item, counting only those prune removes;
	// unnamed parents of other images are neither offered nor counted.
	var unnamed int64
	kept := r.Items[:0]
	for _, it := range r.Items {
		if it.What == "image" && strings.HasPrefix(it.Label, "<none>") {
			if !it.InUse && dangling[strings.TrimPrefix(it.ID, "image:")] {
				unnamed += it.Bytes
			}
			continue
		}
		kept = append(kept, it)
	}
	r.Items = kept
	if unnamed > 0 {
		r.add(Item{ID: "docker-dangling", What: "docker-dangling", Label: "Docker images with no name", Bytes: unnamed,
			Kind: KindImage, Command: "docker image prune --force"})
	}
	sort.SliceStable(r.Items, func(i, j int) bool { return r.Items[i].Bytes > r.Items[j].Bytes })
	return r
}

// add keeps an item worth showing: an empty cache frees nothing.
func (r *Result) add(it Item) {
	if it.Bytes > 0 && it.Command != "" {
		r.Items = append(r.Items, it)
	}
}

// system builds the item for a system-wide finding.
func system(what, path string, n int64) Item {
	it := Item{ID: what, What: what, Label: path, Bytes: n}
	switch what {
	case "apt":
		// apt-get clean empties the downloaded package archive only.
		it.Kind, it.Command = KindCache, "apt-get clean"
	case "journal":
		// Keep the last 100 MB of the journal: recent boots stay readable.
		// What goes is the rest.
		it.Kind, it.Command = KindLog, "journalctl --vacuum-size=100M"
		it.Bytes = max(0, n-100<<20)
	}
	return it
}

// home builds the item for one cache in one home directory. The path comes
// from the script, which found it under a home directory; it is checked
// against the known list again before it goes into a command.
func home(what, path string, n int64) Item {
	for _, c := range caches {
		if c.what == what && strings.HasSuffix(path, "/"+c.rel) && safePath(path) {
			return Item{ID: what + ":" + path, What: what, Label: path, Bytes: n, Kind: c.kind,
				Command: removeContents(path)}
		}
	}
	return Item{}
}

// image builds the item for one Docker image: id, name, the space only it
// holds, its age, and how many containers use it.
func image(f []string) Item {
	// Docker prints full ids here; the short form is what people know.
	id := strings.TrimPrefix(f[0], "sha256:")
	if len(id) > 12 {
		id = id[:12]
	}
	name, size, since, inuse := f[1], f[2], f[3], strings.TrimSpace(f[4]) != "0"
	it := Item{ID: "image:" + id, What: "image", Label: name, Bytes: dockerSize(size), Kind: KindImage, InUse: inuse, Note: since}
	// An image a container uses is shown but cannot be selected: Docker
	// would refuse, and the container needs it. Docker lists an image once
	// even when it has several tags, and removing it by id fails then, so
	// every tag of that id is removed, which removes the image.
	// An image known only by its digest has no tag to remove: "name:<none>"
	// names nothing, so its id is removed instead.
	if !inuse && validImageID(id) {
		it.Command = "docker image ls --no-trunc --format '{{.Repository}}:{{.Tag}} {{.ID}}' | " +
			"awk '$2 ~ /^sha256:" + id + "/ { if ($1 ~ /:<none>$/) none = $2; else { print $1; named = 1 } } END { if (!named && none != \"\") print none }' | " +
			"xargs -r docker image rm"
	}
	return it
}

// removeContents is the command that empties a cache folder. It checks the
// folder again where it runs, since the survey: still no link on its path
// and still on the root filesystem, or it removes nothing. find -xdev stays
// on that filesystem; a folder with anything mounted inside it, a bind
// mount of the same filesystem too, which -xdev would not stop at, is left
// whole. The folder itself stays for its tool.
func removeContents(path string) string {
	q := quote(path)
	// It goes into the folder first and checks where it is from inside, so
	// a link swapped in on the way after the check cannot move the removal.
	return "p=" + q + "; if cd -P -- \"$p\" 2>/dev/null && [ \"$(pwd -P)\" = \"$p\" ] && [ \"$(stat -c %d .)\" = \"$(stat -c %d /)\" ]; then " +
		"if awk -v p=\"$p/\" 'index($5, p) == 1 { found = 1 } END { exit !found }' /proc/self/mountinfo; " +
		"then echo \"skipped $p: something is mounted inside\"; false; " +
		"else find . -xdev -mindepth 1 -delete; fi; " +
		"else echo \"skipped $p: moved or linked\"; false; fi"
}

// safePath accepts only absolute paths under /root or /home made of plain
// characters: no "..", no quotes or shell syntax.
func safePath(p string) bool {
	if !strings.HasPrefix(p, "/root/") && !strings.HasPrefix(p, "/home/") || strings.Contains(p, "..") {
		return false
	}
	for _, c := range p {
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("/._-+@", c)
		if !ok {
			return false
		}
	}
	return true
}

// validImageID accepts the short hex ids `docker image ls` prints.
func validImageID(id string) bool {
	if len(id) < 12 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// quote wraps a checked path in single quotes for sh.
func quote(p string) string { return "'" + p + "'" }

// kib parses a size in KiB into bytes.
func kib(s string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n * 1024
}

// dockerSize parses Docker's sizes ("1.23GB", "512MB", "0B"), which use
// powers of 1000.
func dockerSize(s string) int64 {
	s = strings.TrimSpace(s)
	units := []struct {
		suffix string
		mult   float64
	}{{"TB", 1e12}, {"GB", 1e9}, {"MB", 1e6}, {"kB", 1e3}, {"KB", 1e3}, {"B", 1}}
	for _, u := range units {
		if num, ok := strings.CutSuffix(s, u.suffix); ok {
			v, err := strconv.ParseFloat(num, 64)
			if err != nil {
				return 0
			}
			return int64(v * u.mult)
		}
	}
	return 0
}

// Clean removes the chosen items in one run as root and returns what each
// command printed. Only commands built in this package run.
func Clean(ctx context.Context, sys wsl.System, distro string, items []Item) (string, error) {
	var b strings.Builder
	for _, it := range items {
		if it.Command == "" {
			continue
		}
		// One line per item, each in a subshell of its own (a command may
		// change directory); a failure is reported and the next goes on.
		fmt.Fprintf(&b, "echo '== %s'; ( %s ) 2>&1 || echo 'failed: %s'\n", it.ID, it.Command, it.What)
	}
	if b.Len() == 0 {
		return "", nil
	}
	ctx, cancel := context.WithTimeout(ctx, cleanLimit)
	defer cancel()
	out, err := sys.Run(ctx, distro, "root", b.String())
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return out, fmt.Errorf("removing took over %s: %w", cleanLimit, ctx.Err())
	}
	return out, err
}
