package theme_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func TestColor_FG(t *testing.T) {
	cases := map[theme.Color]string{
		"#ff8800": "\x1b[38;2;255;136;0m",
		"#abc":    "\x1b[38;2;170;187;204m", // 3-digit expands
		"":        "",
		"nothex":  "",
	}
	for c, want := range cases {
		if got := c.FG(); got != want {
			t.Errorf("Color(%q).FG() = %q, want %q", c, got, want)
		}
	}
}

func TestColor_On(t *testing.T) {
	got := theme.Color("#000000").On("x")
	if !strings.HasPrefix(got, "\x1b[38;2;0;0;0m") || !strings.HasSuffix(got, "\x1b[0m") {
		t.Errorf("On wrapping wrong: %q", got)
	}
	if theme.Color("").On("x") != "x" {
		t.Error("empty colour must pass text through unchanged")
	}
}

func TestDecode_ResolvesDark(t *testing.T) {
	th, err := theme.Decode([]byte(`{"name":"t","fg":{"light":"#fff","dark":"#111"},"primary":"#22d3ee","warning":"#e6db74"}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if th.Name != "t" || th.Fg != "#111" || th.Primary != "#22d3ee" {
		t.Errorf("decoded wrong: %+v", th)
	}
	if th.PlanMode != th.Warning {
		t.Errorf("PlanMode should inherit Warning, got %q", th.PlanMode)
	}
}

func TestBuiltins(t *testing.T) {
	if len(theme.Builtins()) == 0 {
		t.Fatal("no builtin themes embedded")
	}
	if theme.DarkDefault().Name == "" {
		t.Fatal("no dark default resolved")
	}
	if _, ok := theme.ByName("nord"); !ok {
		t.Error("expected builtin theme \"nord\"")
	}
	if theme.DarkDefault().Fg == "" {
		t.Error("dark default has no fg colour")
	}
}
