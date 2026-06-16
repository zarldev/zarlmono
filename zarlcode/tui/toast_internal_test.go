package tui

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func TestStatusToastUsesThemeSurfaceAndSemanticForeground(t *testing.T) {
	old := palette
	UseTheme(theme.Theme{
		Fg:        "#ffffff",
		Subtle:    "#aaaaaa",
		Highlight: "#222222",
		Success:   "#00ff00",
		Error:     "#ff0000",
	})
	t.Cleanup(func() { UseTheme(old) })

	s := NewSession("~", "/tmp", "")
	p := newStatusPane(s)

	s.SetSuccessToast("saved settings")
	got := p.statusToast()
	if want := theme.Color("#222222").BG(); !strings.Contains(got, want) {
		t.Fatalf("success toast surface missing %q in %q", want, got)
	}
	if want := theme.Color("#00ff00").FG(); !strings.Contains(got, want) {
		t.Fatalf("success toast foreground missing %q in %q", want, got)
	}

	s.SetErrorToast("boom")
	got = p.statusToast()
	if want := theme.Color("#222222").BG(); !strings.Contains(got, want) {
		t.Fatalf("error toast surface missing %q in %q", want, got)
	}
	if want := theme.Color("#ff0000").FG(); !strings.Contains(got, want) {
		t.Fatalf("error toast foreground missing %q in %q", want, got)
	}

	s.SetToast("working…")
	got = p.statusToast()
	if want := theme.Color("#222222").BG(); !strings.Contains(got, want) {
		t.Fatalf("info toast surface missing %q in %q", want, got)
	}
	if want := theme.Color("#aaaaaa").FG(); !strings.Contains(got, want) {
		t.Fatalf("info toast foreground missing %q in %q", want, got)
	}
}

func TestIsErrorStatusCatchesActionFailures(t *testing.T) {
	for _, status := range []string{
		"name required",
		"transport must be 'stdio' or 'http'",
		"add: duplicate provider",
		"save key: permission denied",
		"fetch models: timeout",
		"delete: not found",
	} {
		if !isErrorStatus(status) {
			t.Fatalf("isErrorStatus(%q) = false, want true", status)
		}
	}
}
