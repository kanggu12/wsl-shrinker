package cli

import (
	"io"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// writeTable prints rows as columns two spaces apart. right[i] lines column
// i up on the right, for numbers; the others line up on the left, for words.
// Widths are counted in terminal cells, so a name in Korean lines up too.
func writeTable(w io.Writer, right []bool, rows [][]string) error {
	// The widest cell of each column sets its width.
	var widths []int
	for _, r := range rows {
		for i, c := range r {
			if i >= len(widths) {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], ansi.StringWidth(c))
		}
	}
	var b strings.Builder
	for _, r := range rows {
		var line strings.Builder
		for i, c := range r {
			// Two spaces between columns.
			if i > 0 {
				line.WriteString("  ")
			}
			pad := strings.Repeat(" ", widths[i]-ansi.StringWidth(c))
			if i < len(right) && right[i] {
				line.WriteString(pad + c)
			} else {
				line.WriteString(c + pad)
			}
		}
		// A left column at the end leaves spaces nobody sees: drop them.
		b.WriteString(strings.TrimRight(line.String(), " ") + "\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}
