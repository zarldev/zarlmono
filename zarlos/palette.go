package main

import (
	"fmt"
	"strings"

	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

// ansi16 is the Ghostty Night Instrument 16-color palette (indices 0-15).
var ansi16 = [16]string{
	"#1a202b", "#db858d", "#a1b99f", "#d7b878", "#8aadf4", "#b8a9d9", "#96abb6", "#ccd2dc",
	"#596579", "#e8a1a8", "#b7c9b3", "#e3cca0", "#aec3ed", "#cbbde8", "#b5c5cc", "#e2e3e7",
}

// cmdPalette prints the 16-color palette centered for the terminal width.
func cmdPalette() {
	pad := centeredPad(24, termCols()) // 8 blocks x 3 cells
	for row := 0; row < 2; row++ {
		fmt.Print(strings.Repeat(" ", pad))
		for i := 0; i < 8; i++ {
			fmt.Print(theme.Color(ansi16[row*8+i]).BG() + "   ")
		}
		fmt.Print("\x1b[0m\n")
	}
}
