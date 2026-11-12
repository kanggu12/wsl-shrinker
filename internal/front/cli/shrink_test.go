package cli

import (
	"regexp"
	"strings"
	"testing"

	"github.com/kanggu12/wsl-shrinker/internal/core/units"
	"github.com/kanggu12/wsl-shrinker/internal/machine/wsl"
)

// withAPT makes the look inside Dev find APT's package files, 1 GiB of
// them, which --clean Dev=apt removes.
func withAPT(fake *wsl.Fake) {
	inner := fake.Script
	fake.Script = func(d, u, s string) (string, error) {
		if strings.Contains(s, "df -Pk /") {
			// The survey's script: its line for the folder, in KiB.
			return "sys|apt|/var/cache/apt/archives|1048576\n", nil
		}
		return inner(d, u, s)
	}
}

func TestTheQuestionCountsWhatCleanRemoves(t *testing.T) {
	// The items --clean names come off the disk too: the question says what
	// comes back with them, the amount the check step then says, as the
	// screen's question does.
	a, fake := flowApp(t)
	withAPT(fake)
	answering(t, "y\n")
	code, out, errOut := shrinkWith(a, "shrink", "Dev", "--clean", "Dev=apt")
	asked := regexp.MustCompile(`Dev: about (\S+ \S+) back to Windows`).FindStringSubmatch(errOut)
	checked := regexp.MustCompile(`check +done +about (\S+ \S+) to give back`).FindStringSubmatch(out)
	if code != exitOK || asked == nil || checked == nil || asked[1] != checked[1] {
		t.Fatalf("exit %d: the question %q and the check %q disagree:\n%s\n%s", code, asked, checked, errOut, out)
	}
	// At least the GiB chosen: the disk alone gives back a few MB.
	if !strings.HasSuffix(asked[1], "GB") {
		t.Fatalf("asked about %s, without the 1 GiB chosen:\n%s", asked[1], errOut)
	}
}

func TestADiskNamedTwiceIsShrunkOnce(t *testing.T) {
	// WSL's names ignore case: dev is Dev again, and runs once.
	a, fake := flowApp(t)
	code, out, errOut := shrinkWith(a, "shrink", "Dev", "dev", "--yes")
	if code != exitOK {
		t.Fatalf("exit %d: %s\n%s", code, errOut, out)
	}
	n := 0
	for _, c := range fake.CallList() {
		if c == "compact:Dev" {
			n++
		}
	}
	if n != 1 || strings.Count(out, "Dev\n") != 1 {
		t.Fatalf("compacted %d times:\n%s", n, out)
	}
	// The question names it once, with what it gives back once.
	a, _ = flowApp(t)
	answering(t, "n\n")
	_, _, errOut = shrinkWith(a, "shrink", "Dev", "DEV")
	if strings.Count(errOut, "Dev: about ") != 1 {
		t.Fatalf("asked:\n%s", errOut)
	}
	// Its --clean is the one disk's, under either name.
	a, fake = flowApp(t)
	withAPT(fake)
	code, out, errOut = shrinkWith(a, "shrink", "Dev", "dev", "--clean", "dev=apt", "--yes", "--json")
	if code != exitOK || strings.Count(out, `"disk": "Dev"`) != 1 {
		t.Fatalf("exit %d: %s\n%s", code, errOut, out)
	}
	// The item is removed, once.
	if cleaned := regexp.MustCompile(`"detail": "1 items?, about `+regexp.QuoteMeta(units.Bytes(1<<30))+`"`).FindAllString(out, -1); len(cleaned) != 1 {
		t.Fatalf("cleaned %d times:\n%s", len(cleaned), out)
	}
}
