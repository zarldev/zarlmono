package appearance

// SetEffective supplies an application-resolved opening choice, such as a
// temporary session override or a compatible legacy preference. It changes only
// displayed metadata and the choice retained on save errors; it never persists
// anything or replaces the original palette used for cancellation.
func (p *Picker) SetEffective(choice Choice) { p.effective = choice }
