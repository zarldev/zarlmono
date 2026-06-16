package tui

import (
	"fmt"
	"strings"
)

// skillsItem shows which skills were loaded during the assistant turn.
// It is always created at turn start but renders as zero lines until at
// least one skill is loaded via load_skill. When collapsed it shows a
// one-line summary; expanded it lists each skill.
//
// Positioned between the assistant headline and thinking block, so the
// user sees "here's what I loaded to help" before the reasoning.
type skillsItem struct {
	versioned
	nested   bool
	skills   []skillRef
	expanded bool
	closed   bool
}

type skillRef struct {
	name string
}

func (s *skillsItem) finished() bool { return s.closed }

func (s *skillsItem) toggle() {
	s.expanded = !s.expanded
	s.bump()
}

func (s *skillsItem) togglerAt(_, ln int) toggler {
	if ln == 0 {
		return s
	}
	return nil
}

func (s *skillsItem) add(name string) {
	// Deduplicate — same skill loaded twice is still one entry.
	for _, sk := range s.skills {
		if sk.name == name {
			return
		}
	}
	s.skills = append(s.skills, skillRef{name: name})
	s.bump()
}

func (s *skillsItem) render(width int) []string {
	if len(s.skills) == 0 {
		return nil // invisible until populated
	}
	toggle := palette.Subtle.On("[") + palette.Primary.On("+") + palette.Subtle.On("]")
	if s.expanded {
		toggle = palette.Subtle.On("[") + palette.Primary.On("-") + palette.Subtle.On("]")
	}
	names := make([]string, len(s.skills))
	for i, sk := range s.skills {
		names[i] = sk.name
	}
	summary := fmt.Sprintf("skills (%d): %s", len(s.skills), strings.Join(names, ", "))
	lines := []string{toggle + " " + palette.Muted.On(summary)}
	if s.expanded {
		for _, sk := range s.skills {
			lines = append(lines, renderContentBlock(width, contentBlock{
				kind:       contentPlain,
				text:       "• " + sk.name,
				bodyPrefix: "  ",
				style:      palette.Muted.On,
			})...)
		}
	}
	if s.nested {
		lines = prefixLines(lines, nestPad)
	}
	return lines
}
