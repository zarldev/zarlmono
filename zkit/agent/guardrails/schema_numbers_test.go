package guardrails_test

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/guardrails"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestSchemaExactNumberTypes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		raw             string
		integer, number bool
	}{
		{"42", true, true}, {"42.0", true, true}, {"4.2e1", true, true}, {"9007199254740993", true, true},
		{"18446744073709551616000000000000000000000000000", true, true},
		{"1e99999999999999999999999999999999999", true, true},
		{"0e-99999999999999999999999999999999999", true, true}, {"-0.000e99999999999999999999999999999999999", true, true},
		{"1e-99999999999999999999999999999999999", false, true}, {"42.00000000000000000000000000001", false, true},
		{"-1.5", false, true}, {"0.000000001", false, true},
		{"", false, false}, {"01", false, false}, {"+1", false, false}, {"1 ", false, false}, {" 1", false, false},
		{"NaN", false, false}, {"Inf", false, false}, {"0x10", false, false}, {"1/2", false, false}, {"1e", false, false}, {"1.", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			t.Parallel()
			for _, kind := range []struct {
				name    string
				allowed bool
			}{{"integer", tt.integer}, {"number", tt.number}} {
				err := numberSchemaError(t, llm.Schema{Type: kind.name}, json.Number(tt.raw))
				if (err == nil) != kind.allowed {
					t.Errorf("%s(%q): %v want accepted=%t", kind.name, tt.raw, err, kind.allowed)
				}
			}
			if err := numberSchemaError(t, llm.Schema{Type: "string"}, json.Number(tt.raw)); err == nil {
				t.Error("Number accepted as string")
			}
		})
	}
}

func TestSchemaNumberEnumMathematicalEquality(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		a, b  any
		equal bool
	}{
		{"adjacent", json.Number("9007199254740993"), json.Number("9007199254740992"), false},
		{"large-int64", json.Number("9007199254740993"), int64(9007199254740993), true},
		{"large-float", json.Number("9007199254740993"), float64(9007199254740992), false},
		{"max-uint64", json.Number("18446744073709551615.0"), uint64(18446744073709551615), true},
		{"spelling", json.Number("42.000"), json.Number("4.2e1"), true},
		{"huge-exponents", json.Number("10e999999999999999999999999999999"), json.Number("1e1000000000000000000000000000000"), true},
		{"tiny-exponents", json.Number("10e-999999999999999999999999999999"), json.Number("1e-999999999999999999999999999998"), true},
		{"signed-zero", json.Number("-0e999999999999999999999999999999"), float64(0), true},
		{"float-decimal", json.Number("0.1"), float64(0.1), true},
		{"int", json.Number("42e0"), int(42), true}, {"int8", json.Number("42e0"), int8(42), true},
		{"int16", json.Number("42e0"), int16(42), true}, {"int32", json.Number("42e0"), int32(42), true},
		{"uint", json.Number("42e0"), uint(42), true}, {"uint8", json.Number("42e0"), uint8(42), true},
		{"uint16", json.Number("42e0"), uint16(42), true}, {"uint32", json.Number("42e0"), uint32(42), true},
		{"uintptr", json.Number("42e0"), uintptr(42), true}, {"float32", json.Number("42e0"), float32(42), true},
		{"float64", json.Number("42e0"), float64(42), true},
		{"no-string-coercion", json.Number("42"), "42", false}, {"invalid-not-string", json.Number("NaN"), "NaN", false},
		{"invalid-not-number", json.Number("NaN"), json.Number("NaN"), false},
		{"empty-not-zero", json.Number(""), 0, false}, {"nan", json.Number("1"), math.NaN(), false},
		{"inf", json.Number("1"), math.Inf(1), false},
		{"legacy-same-float", float64(42), float64(42), true},
		{"legacy-int-float", 42, float64(42), false},
		{"legacy-string", "value", "value", true},
		{"legacy-struct", struct{ N int }{42}, struct{ N int }{42}, true},
		{"legacy-array", [1]int{42}, [1]int{42}, true},
		{"map", map[string]any{"n": 42}, map[string]any{"n": 42}, false},
		{"slice", []any{42}, []any{42}, false},
		{"interface-struct", struct{ V any }{[]int{42}}, struct{ V any }{[]int{42}}, false},
		{"interface-array", [1]any{[]int{42}}, [1]any{[]int{42}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			for _, pair := range [][2]any{{tt.a, tt.b}, {tt.b, tt.a}} {
				err := numberSchemaError(t, llm.Schema{Enum: []any{pair[0]}}, pair[1])
				if (err == nil) != tt.equal {
					t.Errorf("enum=%#v value=%#v: %v want equal=%t", pair[0], pair[1], err, tt.equal)
				}
			}
		})
	}
}

func numberSchemaError(t *testing.T, field llm.Schema, value any) error {
	t.Helper()
	schema := llm.Schema{Type: "object", Properties: map[string]llm.Schema{"value": field}}
	guard := guardrails.NewSchemaGuardrail(tools.NewRegistry(schemaTool{schema: schema}))
	return guard.Before(t.Context(), tools.ToolCall{ToolName: "schema-test", Arguments: tools.ToolParameters{"value": value}})
}
