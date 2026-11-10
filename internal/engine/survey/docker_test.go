package survey

import (
	"slices"
	"strings"
	"testing"

	"github.com/kanggu12/wsl-shrinker/internal/testutil/shtest"
)

// The made-up images' ids, in full as Docker prints them.
const (
	idPostgres = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	idNode     = "sha256:ba9876543210fedcba9876543210fedcba9876543210fedcba9876543210fedc"
)

// engine is a made-up Docker of a distribution's own: an image a container
// uses, one nothing uses, two with no name, the unnamed parent of another,
// and a build cache. One of its lines holds a backslash, which has to come
// through as it is.
func engine() shtest.Docker {
	a, b, c := "sha256:"+strings.Repeat("a", 64), "sha256:"+strings.Repeat("b", 64), "sha256:"+strings.Repeat("c", 64)
	return shtest.Docker{
		OS:       "Ubuntu 24.04.1 LTS",
		Running:  []string{"0123456789ab", "fedcba987654"},
		DF:       []string{"Images|5.2GB (60%)", `Containers|12.5kB\t(0%)`, "Local Volumes|1.1GB (40%)", "Build Cache|3.4GB"},
		Dangling: []string{a, b},
		Images: []string{
			idPostgres + "|postgres:16|440MB|3 weeks ago|1",
			idNode + "|node:20|1.1GB|2 months ago|0",
			a + "|<none>:<none>|200MB|5 days ago|0",
			b + "|<none>:<none>|300MB|5 days ago|0",
			c + "|<none>:<none>|900MB|5 days ago|0",
		},
		Tags: []string{
			"postgres:16 " + idPostgres,
			"node:20 " + idNode,
			"<none>:<none> " + a,
			"<none>:<none> " + b,
			"<none>:<none> " + c,
		},
	}
}

// besidesTheRoot are the lines the script printed other than the root
// filesystem's own, which are those of the machine the test runs on.
func besidesTheRoot(out string) []string {
	var lines []string
	for l := range strings.SplitSeq(strings.TrimRight(out, "\n"), "\n") {
		if !strings.HasPrefix(l, "fs|") && !strings.HasPrefix(l, "opts|") {
			lines = append(lines, l)
		}
	}
	return lines
}

func TestDockersAnswersAreReadByTheShellAlone(t *testing.T) {
	// Nothing but docker is on the PATH: no grep, sed, wc or tr, which a
	// distribution made from a small image may not have. Every line Docker
	// prints still arrives, each under its own name.
	e := engine()
	dir := shtest.Tools(t)
	asked := e.Put(t, dir)
	out, err := shtest.Run(t, dir, scriptAt(t.TempDir()))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want := []string{"docker|on", "containers|2"}
	for _, l := range e.DF {
		want = append(want, "df|"+l)
	}
	for _, l := range e.Dangling {
		want = append(want, "dangling|"+l)
	}
	for _, l := range e.Images {
		want = append(want, "img|"+l)
	}
	if got := besidesTheRoot(out); !slices.Equal(got, want) {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// And they read as what that Docker holds.
	r := parse(out)
	if !r.Docker || r.DockerDesktop || r.Containers != 2 {
		t.Fatalf("%+v", r)
	}
	found := map[string]int64{}
	for _, it := range r.Items {
		found[it.ID] = it.Bytes
	}
	// The image in use is not offered, nor the unnamed parent.
	if len(found) != 3 || found["docker-build-cache"] != 3_400_000_000 || found["image:ba9876543210"] != 1_100_000_000 || found["docker-dangling"] != 500_000_000 {
		t.Fatalf("%v", found)
	}
	// Looking changes nothing.
	if a := shtest.Asked(t, asked); a != nil {
		t.Fatalf("the look inside asked docker for %q", a)
	}
}

func TestDockerDesktopsImagesAreNotListed(t *testing.T) {
	// The docker command here is Docker Desktop's: the same images and
	// containers, which are on its disk and are its own. The script says
	// whose Docker it is and nothing more, with no grep to tell it.
	e := engine()
	e.OS = "Docker Desktop"
	dir := shtest.Tools(t)
	asked := e.Put(t, dir)
	out, err := shtest.Run(t, dir, scriptAt(t.TempDir()))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := besidesTheRoot(out); !slices.Equal(got, []string{"docker|desktop"}) {
		t.Fatalf("%q", got)
	}
	if r := parse(out); !r.DockerDesktop || r.Docker || len(r.Items) != 0 {
		t.Fatalf("%+v", r)
	}
	if a := shtest.Asked(t, asked); a != nil {
		t.Fatalf("the look inside asked docker for %q", a)
	}
	// With no docker at all, and with one whose engine does not answer,
	// nothing is said of Docker.
	none := shtest.Tools(t)
	if out, err := shtest.Run(t, none, scriptAt(t.TempDir())); err != nil || besidesTheRoot(out) != nil {
		t.Fatalf("%v %q", err, out)
	}
	shtest.Stub(t, none, "docker", "exit 1")
	if out, err := shtest.Run(t, none, scriptAt(t.TempDir())); err != nil || besidesTheRoot(out) != nil {
		t.Fatalf("%v %q", err, out)
	}
}

func TestCleaningAsksDockerOnlyForWhatWasChosen(t *testing.T) {
	// The whole way with a plain shell: what the look inside found is
	// cleaned by the commands it built, and docker is asked for exactly
	// those removals.
	dir := shtest.Tools(t)
	asked := engine().Put(t, dir)
	out, err := shtest.Run(t, dir, scriptAt(t.TempDir()))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	items := parse(out).Items
	// One that fails, in the middle: the ones after it still run.
	items = slices.Insert(items, 1, Item{ID: "broken", What: "broken", Command: "exit 3"})
	out, err = shtest.Run(t, dir, cleanScript(items))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	// Largest first, as listed; the image goes by its name.
	want := []string{"builder prune --all --force", "image rm node:20", "image prune --force"}
	if got := shtest.Asked(t, asked); !slices.Equal(got, want) {
		t.Fatalf("docker was asked for %q, want %q", got, want)
	}
	if out != "== docker-build-cache\n== broken\nfailed: broken\n== image:ba9876543210\n== docker-dangling\n" {
		t.Fatalf("%q", out)
	}
}

func TestNoScriptNamesATextTool(t *testing.T) {
	// The tests above run the scripts with those tools missing; this one
	// reads every command the package can build, the ones no test runs
	// too, for the name of one.
	texts := []string{script(), RootOptions, removeContents("/home/u/.cache/pip")}
	for _, it := range parse(sample).Items {
		texts = append(texts, it.Command)
	}
	for _, f := range systemFolders {
		texts = append(texts, f.when, f.command)
	}
	for _, s := range texts {
		if tool := shtest.TextTool(s); tool != "" {
			t.Errorf("%s is needed by:\n%s", tool, s)
		}
	}
	// The check itself finds one where there is one.
	if shtest.TextTool("docker ps -q | wc -l") != "wc" || shtest.TextTool("docker image ls --no-trunc") != "" {
		t.Fatal("the check does not tell a tool from an option")
	}
}
