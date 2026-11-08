package version

import "testing"

func TestPseudoVersionReadsAsDev(t *testing.T) {
	for in, want := range map[string]string{
		// What go build stamps on a commit with no tag: the hash is printed beside it already.
		"v0.0.0-20261107192537-1d1924b3cf34":        "dev",
		"v0.0.0-20261107192537-1d1924b3cf34+dirty":  "dev",
		"v0.2.1-0.20261107192537-1d1924b3cf34":      "dev",
		"v0.3.0-rc.1.0.20261107192537-1d1924b3cf34": "dev",
		// A tag stays as it is.
		"v0.2.0":       "v0.2.0",
		"v0.2.0+dirty": "v0.2.0+dirty",
		"v0.3.0-rc.1":  "v0.3.0-rc.1",
	} {
		if got := moduleVersion(in); got != want {
			t.Errorf("%s: got %q, want %q", in, got, want)
		}
	}
}
