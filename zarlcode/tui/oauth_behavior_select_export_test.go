package tui

// SelectProvider moves the providers cursor to name for footer behavior tests.
func (h *OAuthBehaviorHarness) SelectProvider(name string) {
	for i, definition := range h.providers.defs {
		if definition.Name == name {
			h.providers.cursor = i
			return
		}
	}
}
