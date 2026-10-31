package chrome

// Connection renders one small persistent connection indicator using the family
// glyph vocabulary and semantic palette. Demo is distinct from a real connection.
// Applications without a meaningful connected state need not display one.
func (s Style) Connection(connected, demo bool) string {
	glyph, color := "○", s.Theme.Subtle
	if demo {
		glyph = "◇"
	} else if connected {
		glyph, color = "●", s.Theme.Success
	}
	if s.ASCII {
		switch glyph {
		case "●":
			glyph = "*"
		case "○":
			glyph = "o"
		default:
			glyph = "~"
		}
	}
	return s.paint(color, glyph)
}
