package llm_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// jsonEqual compares two JSON byte slices for semantic (key-order-independent)
// equality.
func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		t.Fatalf("unmarshal a: %v (%s)", err, a)
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		t.Fatalf("unmarshal b: %v (%s)", err, b)
	}
	return reflect.DeepEqual(av, bv)
}

// TestSchema_OpenWorldRoundTrip is the load-bearing guarantee: a schema an MCP
// server sends (with keys the struct doesn't model) must survive map -> Schema
// -> JSON byte-equivalently, or we'd send the model a corrupted schema.
func TestSchema_OpenWorldRoundTrip(t *testing.T) {
	t.Parallel()
	raw := []byte(`{
		"type": "object",
		"description": "rich",
		"properties": {
			"mode":  {"type": "string", "enum": ["fast", "safe"]},
			"count": {"type": "integer", "minimum": 1, "maximum": 10},
			"tags":  {"type": "array", "items": {"type": "string"}},
			"either": {"oneOf": [{"type": "string"}, {"type": "number"}]},
			"ref":   {"$ref": "#/$defs/Thing"},
			"pat":   {"type": "object", "patternProperties": {"^x": {"type": "string"}}}
		},
		"required": ["mode", "count"],
		"additionalProperties": false,
		"$defs": {"Thing": {"type": "string", "format": "uuid"}}
	}`)

	var s llm.Schema
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonEqual(t, raw, out) {
		t.Fatalf("open-world round-trip changed the schema:\n in: %s\nout: %s", raw, out)
	}

	// Spot-check that the modelled fields are typed, not buried in Extra.
	if s.Type != "object" || len(s.Required) != 2 || s.Required[0] != "mode" {
		t.Fatalf("required not typed: %#v", s.Required)
	}
	if _, ok := s.AdditionalProperties.(bool); !ok {
		t.Fatalf("additionalProperties not bool: %T", s.AdditionalProperties)
	}
	if _, ok := s.Extra["$defs"]; !ok {
		t.Fatalf("open-world $defs dropped from Extra: %#v", s.Extra)
	}
	mode := s.Properties["mode"]
	if len(mode.Enum) != 2 || mode.Enum[0] != "fast" {
		t.Fatalf("nested enum lost: %#v", mode.Enum)
	}
	count := s.Properties["count"]
	if count.Extra["minimum"] == nil {
		t.Fatalf("nested open-world minimum lost: %#v", count.Extra)
	}
}

// TestSchema_BothIngestShapes proves required/enum ingest from both the
// []string form (in-process literals) and the []any form (post-json.Unmarshal).
func TestSchema_BothIngestShapes(t *testing.T) {
	t.Parallel()
	fromStrings := llm.SchemaFromMap(map[string]any{
		"type":     "object",
		"required": []string{"a", "b"},
		"properties": map[string]any{
			"a": map[string]any{"type": "string", "enum": []string{"x", "y"}},
		},
	})
	fromAny := llm.SchemaFromMap(map[string]any{
		"type":     "object",
		"required": []any{"a", "b"},
		"properties": map[string]any{
			"a": map[string]any{"type": "string", "enum": []any{"x", "y"}},
		},
	})
	if !reflect.DeepEqual(fromStrings.Required, []string{"a", "b"}) {
		t.Fatalf("[]string required = %#v", fromStrings.Required)
	}
	if !reflect.DeepEqual(fromAny.Required, []string{"a", "b"}) {
		t.Fatalf("[]any required = %#v", fromAny.Required)
	}
	// Canonical output must be identical regardless of ingest shape.
	a, _ := json.Marshal(fromStrings)
	b, _ := json.Marshal(fromAny)
	if !jsonEqual(t, a, b) {
		t.Fatalf("ingest shape leaked into output:\n[]string: %s\n[]any:    %s", a, b)
	}
}

// TestSchema_ByteEquivalentWithLegacyMap proves a Schema marshals to the same
// JSON the old hand-built map[string]any produced, so providers that just
// marshal the schema send identical bytes to the model.
func TestSchema_ByteEquivalentWithLegacyMap(t *testing.T) {
	t.Parallel()
	legacy := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":    map[string]any{"type": "string", "description": "file path"},
			"mode":    map[string]any{"type": "string", "enum": []string{"read", "write"}},
			"recurse": map[string]any{"type": "boolean"},
		},
		"required":             []string{"path"},
		"additionalProperties": false,
	}
	legacyJSON, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy: %v", err)
	}

	typed := llm.Schema{
		Type: "object",
		Properties: map[string]llm.Schema{
			"path":    {Type: "string", Description: "file path"},
			"mode":    {Type: "string", Enum: []any{"read", "write"}},
			"recurse": {Type: "boolean"},
		},
		Required:             []string{"path"},
		AdditionalProperties: false,
	}
	typedJSON, err := json.Marshal(typed)
	if err != nil {
		t.Fatalf("marshal typed: %v", err)
	}
	if !jsonEqual(t, legacyJSON, typedJSON) {
		t.Fatalf("typed schema not byte-equivalent to legacy map:\nlegacy: %s\ntyped:  %s", legacyJSON, typedJSON)
	}
}

// TestSchema_AdditionalPropertiesSchemaForm covers the schema-valued
// additionalProperties JSON Schema permits (not just bool).
func TestSchema_AdditionalPropertiesSchemaForm(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"type":"object","additionalProperties":{"type":"string"}}`)
	var s llm.Schema
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	sub, ok := s.AdditionalProperties.(llm.Schema)
	if !ok {
		t.Fatalf("additionalProperties not Schema: %T", s.AdditionalProperties)
	}
	if sub.Type != "string" {
		t.Fatalf("additionalProperties subschema wrong: %#v", sub)
	}
	out, _ := json.Marshal(s)
	if !jsonEqual(t, raw, out) {
		t.Fatalf("additionalProperties schema-form round-trip failed: %s", out)
	}
}

// TestSchema_NilAndEmpty covers the absent / no-args edges.
func TestSchema_NilAndEmpty(t *testing.T) {
	t.Parallel()
	if got := llm.SchemaFromMap(nil); !got.IsZero() {
		t.Fatalf("SchemaFromMap(nil) = %#v, want zero", got)
	}
	var s llm.Schema
	if err := json.Unmarshal([]byte(`null`), &s); err != nil {
		t.Fatalf("unmarshal null: %v", err)
	}
	out, err := json.Marshal(llm.Schema{})
	if err != nil {
		t.Fatalf("marshal empty: %v", err)
	}
	if string(out) != "{}" {
		t.Fatalf("empty schema = %s, want {}", out)
	}
}
