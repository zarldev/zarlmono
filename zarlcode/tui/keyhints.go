package tui

import "strings"

// keyHint pairs a key binding with the action it performs. An empty key
// renders the label alone (an inline instruction like "type" or a status
// note); an empty label renders the key alone.
type keyHint struct{ key, label string }

// keyLegend styles a row of key hints for a footer, title strip, or help box:
// each key in the Primary accent with its label beside it in Muted, pairs
// glued by a dim separator — so the bindings read as "key → what it does" at a
// glance instead of one flat muted run. With the zero-value palette (tests) it
// degrades to the plain "key label · key label" text, so substring assertions
// stay colour-agnostic.
func keyLegend(hints ...keyHint) string {
	parts := make([]string, 0, len(hints))
	for _, h := range hints {
		var seg string
		switch {
		case h.key == "":
			seg = palette.Muted.On(h.label)
		case h.label == "":
			seg = palette.Primary.On(h.key)
		default:
			seg = palette.Primary.On(h.key) + " " + palette.Muted.On(h.label)
		}
		parts = append(parts, seg)
	}
	return strings.Join(parts, palette.Subtle.On(" · "))
}
