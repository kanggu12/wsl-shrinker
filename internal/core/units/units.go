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
	for v >= 1024 && unit < 5 {
		v /= 1024
		unit++
	}
	// One decimal below 100, none above: "8.3 GB", "471 GB".
	name := []string{"B", "KB", "MB", "GB", "TB", "PB"}[unit]
	if v >= 100 {
		return fmt.Sprintf("%.0f %s", v, name)
	}
	return fmt.Sprintf("%.1f %s", v, name)
}
