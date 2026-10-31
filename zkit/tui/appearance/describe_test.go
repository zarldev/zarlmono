package appearance_test

import (
	"os"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/tui/appearance"
)

func TestDescriptionShowsSessionWithoutPersistingIt(t *testing.T) {
	s, _ := store(t)
	if err := s.Set(t.Context(), appearance.Global(), "terminal-green"); err != nil {
		t.Fatal(err)
	}
	lines, err := s.Describe(t.Context(), "app", "workspace", "synthwave")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(lines, "\n")
	for _, want := range []string{"effective: synthwave (source: session)", "global: terminal-green", "app: inherit", "workspace: inherit"} {
		if !strings.Contains(text, want) {
			t.Fatalf("description omitted %q: %s", want, text)
		}
	}
	choice(t, s, "app", "workspace", "terminal-green", "global")
}

func TestDescribingMissingConfigurationDoesNotCreateIt(t *testing.T) {
	s, path := store(t)
	if _, err := s.Describe(t.Context(), "app", "workspace", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("inspection created configuration: %v", err)
	}
}
