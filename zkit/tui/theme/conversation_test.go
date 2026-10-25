package theme_test

import (
	"testing"

	lg "charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func TestConversationBackgrounds(t *testing.T) {
	for _, tc := range []struct {
		name            string
		palette         theme.Theme
		user, assistant theme.Color
	}{
		{name: "zero"},
		{name: "legacy", palette: theme.Theme{Bg: "#abcdef", Subtle: "#333333"}, user: "#abcdef", assistant: "#abcdef"},
		{name: "override", palette: theme.Theme{Bg: "#abcdef", UserBg: "#eeeeee", AssistantBg: "#ffffff"}, user: "#eeeeee", assistant: "#ffffff"},
		{name: "dark default", palette: theme.DarkDefault(), user: "#21293a", assistant: "#0f172a"},
		{name: "custom dark", palette: theme.Theme{Bg: "#102030", Fg: "#d0e0f0"}, user: "#1f2f3f", assistant: "#102030"},
		{name: "custom light", palette: theme.Theme{Bg: "#f0e0d0", Fg: "#302010"}, user: "#e0d0c0", assistant: "#f0e0d0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u, a := tc.palette.ConversationBackgrounds()
			if u != tc.user || a != tc.assistant {
				t.Fatalf("backgrounds = %q, %q; want %q, %q", u, a, tc.user, tc.assistant)
			}
		})
	}
	light, ok := theme.ByName("parchment")
	if !ok {
		t.Fatal("missing light default")
	}
	u, a := light.ConversationBackgrounds()
	if a != light.Bg || u != "#ebe9d7" {
		t.Fatalf("light surfaces = %q, %q", u, a)
	}
}

func TestDecodeConversationBackgrounds(t *testing.T) {
	for _, tc := range []struct {
		input           string
		user, assistant theme.Color
	}{
		{`{"bg":"#abcdef"}`, "#abcdef", "#abcdef"},
		{`{"bg":"#abcdef","userBg":"#123456","assistantBg":{"light":"#ffffff","dark":"#101010"}}`, "#123456", "#101010"},
		{`{}`, "", ""},
	} {
		got, err := theme.Decode([]byte(tc.input))
		if err != nil {
			t.Fatal(err)
		}
		u, a := got.ConversationBackgrounds()
		if u != tc.user || a != tc.assistant {
			t.Fatalf("%s: backgrounds = %q, %q", tc.input, u, a)
		}
	}
}

func TestConversationSurfacesRemainDistinctInANSI256(t *testing.T) {
	for _, name := range []string{"midnight", "parchment"} {
		palette, _ := theme.ByName(name)
		user, assistant := palette.ConversationBackgrounds()
		u := colorprofile.ANSI256.Convert(lg.Color(string(user)))
		a := colorprofile.ANSI256.Convert(lg.Color(string(assistant)))
		ur, ug, ub, _ := u.RGBA()
		ar, ag, ab, _ := a.RGBA()
		if ur == ar && ug == ag && ub == ab {
			t.Fatalf("%s surfaces collapse to the same ANSI256 colour", name)
		}
	}
}
