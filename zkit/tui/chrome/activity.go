package chrome

// MarkerFor combines the common thin selection pointer with a domain-specific
// one-cell activity glyph (for example playback's play triangle). An empty glyph
// means inactive. Callers supply a known single-cell symbol; ASCII uses '*'.
func (s Style) MarkerFor(selected bool, activity string) string {
	if activity == "" {
		return s.Marker(selected, false)
	}
	if s.ASCII {
		return s.Marker(selected, true)
	}
	if selected {
		return "▸" + activity
	}
	return activity + " "
}
