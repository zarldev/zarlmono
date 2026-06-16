package tui

// Braille spinner frames used for live LLM activity. These are all single-cell
// glyphs in the Unicode Braille Patterns block, so the title width stays stable
// while the indicator animates.
var brailleActivityFrames = [...]string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func runActivityGlyph(frame int, running bool) string {
	if !running {
		return "⠄"
	}
	return brailleActivityFrames[frame%len(brailleActivityFrames)]
}
