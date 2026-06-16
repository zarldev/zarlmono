package guardrails

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// validateJSONSchema is package-private; this is a white-box test in
// the runner package, not the _test package.

func TestValidateJSONSchema_Types(t *testing.T) {
	schema := map[string]any{"type": "string"}
	if err := validateJSONSchema(llm.SchemaFromMap(schema), "hello"); err != nil {
		t.Errorf("string value: %v", err)
	}
	if err := validateJSONSchema(llm.SchemaFromMap(schema), 42); err == nil {
		t.Errorf("int value against string schema: want error")
	}
}

func TestValidateJSONSchema_IntegerFromFloat(t *testing.T) {
	schema := map[string]any{"type": "integer"}
	// JSON unmarshals "42" to float64(42); the validator should accept it.
	if err := validateJSONSchema(llm.SchemaFromMap(schema), float64(42)); err != nil {
		t.Errorf("float64(42) against integer schema: %v", err)
	}
	// A fractional float64 should fail.
	if err := validateJSONSchema(llm.SchemaFromMap(schema), 42.5); err == nil {
		t.Errorf("42.5 against integer schema: want error")
	}
}

func TestValidateJSONSchema_IntegerSatisfiesNumber(t *testing.T) {
	schema := map[string]any{"type": "number"}
	if err := validateJSONSchema(llm.SchemaFromMap(schema), float64(42)); err != nil {
		t.Errorf("integer against number schema: %v", err)
	}
}

func TestValidateJSONSchema_RequiredFields(t *testing.T) {
	schema := map[string]any{
		"type":            "object",
		schemaKeyRequired: []any{"command"},
		"properties": map[string]any{
			"command": map[string]any{"type": "string"},
		},
	}
	good := map[string]any{"command": "ls"}
	if err := validateJSONSchema(llm.SchemaFromMap(schema), good); err != nil {
		t.Errorf("good value: %v", err)
	}
	bad := map[string]any{}
	if err := validateJSONSchema(llm.SchemaFromMap(schema), bad); err == nil {
		t.Errorf("missing required: want error")
	}
}

func TestValidateJSONSchema_NestedProperty(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"opts": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"timeout": map[string]any{"type": "integer"},
				},
			},
		},
	}
	good := map[string]any{"opts": map[string]any{"timeout": float64(30)}}
	if err := validateJSONSchema(llm.SchemaFromMap(schema), good); err != nil {
		t.Errorf("nested good: %v", err)
	}
	bad := map[string]any{"opts": map[string]any{"timeout": "not an int"}}
	if err := validateJSONSchema(llm.SchemaFromMap(schema), bad); err == nil {
		t.Errorf("nested bad: want error")
	}
}

func TestValidateJSONSchema_Enum(t *testing.T) {
	schema := map[string]any{schemaKeyEnum: []any{"a", "b", "c"}}
	if err := validateJSONSchema(llm.SchemaFromMap(schema), "a"); err != nil {
		t.Errorf("enum match: %v", err)
	}
	if err := validateJSONSchema(llm.SchemaFromMap(schema), "d"); err == nil {
		t.Errorf("enum miss: want error")
	}
}

// tools.SchemaFor / service.Parameters build enum as []string (no JSON
// round-trip). The validator must enforce that shape too, else every
// in-process tool's enum constraint is silently un-checked.
func TestValidateJSONSchema_EnumStringSlice(t *testing.T) {
	schema := map[string]any{schemaKeyEnum: []string{"a", "b", "c"}}
	if err := validateJSONSchema(llm.SchemaFromMap(schema), "a"); err != nil {
		t.Errorf("[]string enum match: %v", err)
	}
	if err := validateJSONSchema(llm.SchemaFromMap(schema), "d"); err == nil {
		t.Errorf("[]string enum miss: want error")
	}
}

// required as []string (the SchemaFor / service.Parameters shape) must be
// honoured alongside the []any (MCP/dynamic) shape.
func TestValidateJSONSchema_RequiredStringSlice(t *testing.T) {
	schema := map[string]any{
		"type":            "object",
		schemaKeyRequired: []string{"command"},
		"properties": map[string]any{
			"command": map[string]any{"type": "string"},
		},
	}
	if err := validateJSONSchema(llm.SchemaFromMap(schema), map[string]any{"command": "ls"}); err != nil {
		t.Errorf("[]string required good: %v", err)
	}
	if err := validateJSONSchema(llm.SchemaFromMap(schema), map[string]any{}); err == nil {
		t.Errorf("[]string required missing: want error")
	}
}

func TestValidateJSONSchema_AdditionalPropertiesFalse(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{"type": "string"},
		},
		"additionalProperties": false,
	}
	good := map[string]any{"command": "ls"}
	if err := validateJSONSchema(llm.SchemaFromMap(schema), good); err != nil {
		t.Errorf("declared field: %v", err)
	}
	bad := map[string]any{"command": "ls", "unknown": "x"}
	if err := validateJSONSchema(llm.SchemaFromMap(schema), bad); err == nil {
		t.Errorf("undeclared field: want error")
	}
}

func TestValidateJSONSchema_NullPasses(t *testing.T) {
	// Optional fields commonly arrive nil from JSON. The validator
	// shouldn't trip on absent values — required does that job.
	schema := map[string]any{"type": "string"}
	if err := validateJSONSchema(llm.SchemaFromMap(schema), nil); err != nil {
		t.Errorf("nil value: %v", err)
	}
}

func TestValidateJSONSchema_ArrayItems(t *testing.T) {
	schema := map[string]any{
		"type":  "array",
		"items": map[string]any{"type": "string"},
	}
	good := []any{"a", "b", "c"}
	if err := validateJSONSchema(llm.SchemaFromMap(schema), good); err != nil {
		t.Errorf("string array: %v", err)
	}
	bad := []any{"a", 42, "c"}
	if err := validateJSONSchema(llm.SchemaFromMap(schema), bad); err == nil {
		t.Errorf("mixed array against string-items: want error")
	}
}
