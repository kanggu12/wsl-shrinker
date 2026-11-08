// Package units formats sizes the way Windows shows them.
package units

import "fmt"

// Bytes formats a size as File Explorer does: powers of 1024 named KB, MB,
// GB, so the numbers on screen match what Windows reports for the same file.
func Bytes(n int64) string {
	// Below a kilobyte the exact count reads best.
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	// Find the largest unit the size reaches.
	v, unit := float64(n), 0
	// A value that would print as 1000 or more (999.5 rounds up) moves to
	// the next unit too, so no size shows four digits: "1.0 GB", not "1021 MB".
	for v >= 999.5 && unit < 5 {
		v /= 1024
		unit++
	}
	// One decimal below 100, none above: "8.3 GB", "471 GB". A value that
	// rounds to 100 at one decimal (99.95 and up) is past it: "100 GB".
	name := []string{"B", "KB", "MB", "GB", "TB", "PB"}[unit]
	if v >= 99.95 {
		return fmt.Sprintf("%.0f %s", v, name)
	}
	return fmt.Sprintf("%.1f %s", v, name)
}
