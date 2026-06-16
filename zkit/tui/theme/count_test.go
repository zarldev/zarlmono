package theme_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func TestCountBuiltins(t *testing.T) {
	bs := theme.Builtins()
	t.Logf("Builtins count: %d", len(bs))
	for _, th := range bs {
		t.Logf("  %s", th.Name)
	}
}
