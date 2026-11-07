package units

import "testing"

func TestBytes(t *testing.T) {
	// File Explorer's way: the real disk file of 565,480,259,584 bytes.
	for n, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1536: "1.5 KB", 5 << 20: "5.0 MB",
		565480259584: "527 GB", 8_916_000_000: "8.3 GB", 3 << 40: "3.0 TB"} {
		if got := Bytes(n); got != want {
			t.Errorf("%d: got %q, want %q", n, got, want)
		}
	}
}
