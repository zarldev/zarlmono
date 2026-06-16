package runner

import (
	"strings"
	"testing"
)

type stringerData struct{}

func (stringerData) String() string { return "stringer-text" }

// TestFormatToolData covers the 4.6 fix: the shared formatter renders a
// nested map as JSON, not fmt.Sprint's Go-syntax (map[foo:bar]), and
// honours strings / fmt.Stringer.
func TestFormatToolData(t *testing.T) {
	if got := formatToolData(nil); got != "" {
		t.Errorf("nil = %q, want empty", got)
	}
	if got := formatToolData("hi"); got != "hi" {
		t.Errorf("string = %q, want hi", got)
	}
	if got := formatToolData(stringerData{}); got != "stringer-text" {
		t.Errorf("stringer = %q, want stringer-text", got)
	}
	got := formatToolData(map[string]string{"foo": "bar"})
	if strings.Contains(got, "map[") {
		t.Errorf("map rendered with Go syntax: %q", got)
	}
	if !strings.Contains(got, `"foo":"bar"`) {
		t.Errorf("map = %q, want JSON containing \"foo\":\"bar\"", got)
	}
}
