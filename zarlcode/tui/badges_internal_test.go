package tui

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

// providerBadges replaces the old ★active/key●/key○ glyph soup with a small,
// consistent word vocabulary. With a colourless palette the output is plain
// text, so substring assertions are stable.
func TestProviderBadges_Vocabulary(t *testing.T) {
	UseTheme(theme.Theme{})
	defer UseTheme(theme.Theme{})

	cases := []struct {
		name                                   string
		active, custom, oauth, hasKey, usesKey bool
		want                                   []string
		absent                                 []string
	}{
		{name: "active local server", active: true, want: []string{"local", badgeActive}},
		{name: "key set", hasKey: true, usesKey: true, want: []string{"key set"}, absent: []string{badgeActive}},
		{name: "no key", usesKey: true, want: []string{"no key"}},
		{name: "oauth signed in", oauth: true, hasKey: true, usesKey: true, want: []string{"signed in"}},
		{name: "custom with key", custom: true, hasKey: true, usesKey: true, want: []string{"custom", "key set"}},
	}
	for _, c := range cases {
		got := providerBadges(c.active, c.custom, c.oauth, c.hasKey, c.usesKey)
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: %q missing %q", c.name, got, w)
			}
		}
		for _, a := range c.absent {
			if strings.Contains(got, a) {
				t.Errorf("%s: %q should not contain %q", c.name, got, a)
			}
		}
	}
}

// joinBadges drops empty parts so an unset slot leaves no dangling separator.
func TestJoinBadges_SkipsEmpty(t *testing.T) {
	UseTheme(theme.Theme{})
	defer UseTheme(theme.Theme{})

	if got := joinBadges("", "key set", ""); got != "key set" {
		t.Errorf("joinBadges = %q, want %q", got, "key set")
	}
	if got := joinBadges("", ""); got != "" {
		t.Errorf("joinBadges of all-empty = %q, want empty", got)
	}
}
