package units

import "testing"

func TestBytes(t *testing.T) {
	// File Explorer's way: powers of 1024, one decimal below 100.
	for n, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1536: "1.5 KB", 5 << 20: "5.0 MB",
		612 << 30: "612 GB", 8_916_000_000: "8.3 GB", 3 << 40: "3.0 TB"} {
		if got := Bytes(n); got != want {
			t.Errorf("%d: got %q, want %q", n, got, want)
		}
	}
}

func TestBytesNeverShowsFourDigits(t *testing.T) {
	// From 999.5 of a unit up, the number would print as 1000 to 1024: the
	// next unit says it in fewer digits, as File Explorer does.
	for n, want := range map[int64]string{1000 << 20: "1.0 GB", 1021 << 20: "1.0 GB", 1023<<20 + 1<<19: "1.0 GB",
		999 << 20: "999 MB", 1000 << 10: "1.0 MB", 1000 << 30: "1.0 TB",
		// 99.97 GB rounds to 100 at one decimal: it shows as a number past
		// 100, never "100.0 GB"; 99.949 GB keeps its decimal.
		102_370 << 20: "100 GB", 102_348 << 20: "99.9 GB"} {
		if got := Bytes(n); got != want {
			t.Errorf("%d: got %q, want %q", n, got, want)
		}
	}
}
