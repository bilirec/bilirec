package stdoutbox

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"

	xwidth "golang.org/x/text/width"
)

// Print writes a bordered box to stdout with the given title and body lines.
func Print(title string, lines ...string) {
	PrintTo(os.Stdout, title, lines...)
}

// PrintTo writes a bordered box to w.
func PrintTo(w io.Writer, title string, lines ...string) {
	const minWidth = 55
	const leftPadding = "  "

	width := minWidth
	if l := displayWidth(leftPadding+title) + 1; l > width {
		width = l
	}
	for _, line := range lines {
		if l := displayWidth(leftPadding+line) + 1; l > width {
			width = l
		}
	}

	edge := "+" + strings.Repeat("-", width) + "+"
	emptyLine := "|" + strings.Repeat(" ", width) + "|"

	fmt.Fprintln(w)
	fmt.Fprintln(w, edge)
	printLineTo(w, width, title)
	fmt.Fprintln(w, emptyLine)
	for _, line := range lines {
		printLineTo(w, width, line)
	}
	fmt.Fprintln(w, emptyLine)
	fmt.Fprintln(w, edge)
	fmt.Fprintln(w)
}

func printLineTo(w io.Writer, width int, content string) {
	const leftPadding = "  "
	inner := leftPadding + content
	padding := width - displayWidth(inner)
	if padding < 0 {
		padding = 0
	}
	fmt.Fprintf(w, "|%s%s|\n", inner, strings.Repeat(" ", padding))
}

func displayWidth(s string) int {
	total := 0
	for _, r := range s {
		if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || r == '\u200d' {
			continue
		}

		switch xwidth.LookupRune(r).Kind() {
		case xwidth.EastAsianWide, xwidth.EastAsianFullwidth:
			total += 2
		default:
			total += 1
		}
	}
	return total
}
