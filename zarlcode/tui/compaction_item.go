package tui

import "strings"

// compactionItem is a standalone transcript record for a user-triggered
// compaction. It follows the same collapsed [+] convention as tools, plans,
// skills, and sub-agents; expanding it exposes the full compaction metadata.
type compactionItem struct {
	versioned
	summary  string
	details  []string
	expanded bool
}

func newCompactionItem(summary string) *compactionItem {
	return &compactionItem{
		summary:  summary,
		details:  compactionDetails(summary),
		expanded: false,
	}
}

func (c *compactionItem) finished() bool { return true }

func (c *compactionItem) toggle() {
	c.expanded = !c.expanded
	c.bump()
}

func (c *compactionItem) togglerAt(_, line int) toggler {
	if line == 0 {
		return c
	}
	return nil
}

func (c *compactionItem) render(width int) []string {
	glyph := "+"
	if c.expanded {
		glyph = "-"
	}
	lines := []string{
		palette.Subtle.On("[") + palette.Primary.On(glyph) + palette.Subtle.On("] ") + palette.Muted.On(c.summary),
	}
	if c.expanded {
		for _, detail := range c.details {
			lines = append(lines, renderContentBlock(width, contentBlock{
				kind:       contentPlain,
				text:       detail,
				bodyPrefix: "  ",
				style:      palette.Muted.On,
			})...)
		}
	}
	return lines
}

func compactionDetails(summary string) []string {
	parts := strings.Split(summary, " · ")
	if len(parts) == 0 {
		return nil
	}
	details := []string{"manual conversation compaction"}
	for _, part := range parts {
		if part != "" {
			details = append(details, part)
		}
	}
	return details
}
