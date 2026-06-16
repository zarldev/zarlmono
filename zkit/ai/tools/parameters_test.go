package tools_test

import (
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestToolParameters_String(t *testing.T) {
	t.Parallel()
	p := tools.ToolParameters{"name": "alice", "n": 42}
	if got := p.String("name", ""); got != "alice" {
		t.Errorf("String(name) = %q", got)
	}
	if got := p.String("missing", "fallback"); got != "fallback" {
		t.Errorf("String(missing) = %q, want fallback", got)
	}
	// Wrong type — should fall back rather than coerce.
	if got := p.String("n", "fallback"); got != "fallback" {
		t.Errorf("String over int = %q, want fallback", got)
	}
}

func TestToolParameters_Int(t *testing.T) {
	t.Parallel()
	// JSON decodes numbers as float64 — make sure that path works.
	p := tools.ToolParameters{"a": float64(7), "b": 3, "c": "9", "bad": "nope"}
	cases := map[string]int{"a": 7, "b": 3, "c": 9}
	for k, want := range cases {
		if got := p.Int(k, 0); got != want {
			t.Errorf("Int(%s) = %d, want %d", k, got, want)
		}
	}
	if got := p.Int("bad", -1); got != -1 {
		t.Errorf("Int(bad) = %d, want -1 (fallback)", got)
	}
	if got := p.Int("missing", -1); got != -1 {
		t.Errorf("Int(missing) = %d, want -1", got)
	}
}

func TestToolParameters_Bool(t *testing.T) {
	t.Parallel()
	p := tools.ToolParameters{"on": true, "off": false, "weird": "true"}
	if !p.Bool("on", false) {
		t.Errorf("Bool(on) wrong")
	}
	if p.Bool("off", true) {
		t.Errorf("Bool(off) wrong")
	}
	if p.Bool("weird", false) {
		t.Errorf("Bool(weird=string) should fall back to false")
	}
	if !p.Bool("missing", true) {
		t.Errorf("Bool(missing) default true ignored")
	}
}

func TestToolParameters_Float(t *testing.T) {
	t.Parallel()
	p := tools.ToolParameters{
		"a": float64(1.5),
		"b": 2,
		"c": "3.25",
		"d": "not-a-number",
	}
	if got := p.Float("a", 0); got != 1.5 {
		t.Errorf("Float(a) = %v", got)
	}
	if got := p.Float("b", 0); got != 2 {
		t.Errorf("Float(b) = %v", got)
	}
	if got := p.Float("c", 0); got != 3.25 {
		t.Errorf("Float(c) = %v", got)
	}
	if got := p.Float("d", -1); got != -1 {
		t.Errorf("Float(d) = %v, want -1 fallback", got)
	}
	if got := p.Float("missing", -1); got != -1 {
		t.Errorf("Float(missing) = %v, want -1", got)
	}
}

func TestToolParameters_Slice(t *testing.T) {
	t.Parallel()

	// JSON-shaped: []any of strings.
	p := tools.ToolParameters{
		"args": []any{"one", "two", "three"},
	}
	if got := p.Slice("args"); !reflect.DeepEqual(got, []string{"one", "two", "three"}) {
		t.Errorf("Slice(args) = %v", got)
	}

	// Mixed types: non-strings get stringified via fmt.Sprint.
	p = tools.ToolParameters{"mix": []any{"alice", 42, true}}
	got := p.Slice("mix")
	want := []string{"alice", "42", "true"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Slice(mix) = %v, want %v", got, want)
	}

	// Already-typed []string passes through (callers building params
	// by hand instead of via JSON).
	p = tools.ToolParameters{"raw": []string{"a", "b"}}
	if got := p.Slice("raw"); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("Slice(raw) = %v", got)
	}

	// Missing → nil.
	empty := tools.ToolParameters{}
	if got := empty.Slice("nope"); got != nil {
		t.Errorf("Slice(missing) = %v, want nil", got)
	}

	// Wrong shape (e.g. a single string for an array key) → nil.
	p = tools.ToolParameters{"args": "not-an-array"}
	if got := p.Slice("args"); got != nil {
		t.Errorf("Slice over string = %v, want nil", got)
	}
}

func TestToolParameters_Map(t *testing.T) {
	t.Parallel()

	// JSON-shaped: map[string]any with string values.
	p := tools.ToolParameters{
		"env": map[string]any{
			"PATH": "/usr/bin",
			"FOO":  "bar",
		},
	}
	got := p.Map("env")
	want := map[string]string{"PATH": "/usr/bin", "FOO": "bar"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Map(env) = %v, want %v", got, want)
	}

	// Mixed value types stringify.
	p = tools.ToolParameters{"meta": map[string]any{"port": 8080, "tls": true}}
	got = p.Map("meta")
	if got["port"] != "8080" || got["tls"] != "true" {
		t.Errorf("Map stringify wrong: %v", got)
	}

	// Already-typed map[string]string passes through.
	p = tools.ToolParameters{"hand": map[string]string{"a": "b"}}
	if got := p.Map("hand"); !reflect.DeepEqual(got, map[string]string{"a": "b"}) {
		t.Errorf("Map(hand) = %v", got)
	}

	// Missing → nil.
	emptyMap := tools.ToolParameters{}
	if got := emptyMap.Map("nope"); got != nil {
		t.Errorf("Map(missing) = %v, want nil", got)
	}

	// Wrong shape (a string under an object key) → nil.
	p = tools.ToolParameters{"env": "PATH=/usr/bin"}
	if got := p.Map("env"); got != nil {
		t.Errorf("Map over string = %v, want nil", got)
	}
}
