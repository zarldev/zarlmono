package guardrails

import (
	"fmt"
	"slices"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

const (
	schemaTypeStr   = "string"
	schemaObj       = "object"
	jsonTypeInteger = "integer"
	jsonTypeNumber  = "number"

	// Used by verdictResponseFormat's hand-built llm.ResponseFormat.Schema
	// (a structured-output schema map, distinct from tool ToolSpec.Parameters).
	schemaKeyType     = "type"
	schemaProps       = "properties"
	schemaKeyEnum     = "enum"
	schemaKeyRequired = "required"
)

// validateJSONSchema checks value against the subset of JSON Schema our tools
// actually declare. Supports:
//
//   - Type: string | integer | number | boolean | array | object | null
//   - Required: [field, ...]
//   - Properties: { field: schema }   (recursive)
//   - Items: schema                   (per-element type check)
//   - Enum: [allowed, ...]
//   - AdditionalProperties: false     (rejects unknown keys)
//
// Returns nil when value satisfies schema. Error messages name the dotted
// field path so the LLM can fix the specific argument.
//
// Numbers from JSON arrive as float64; whole numbers are treated as integers
// so an "integer" schema accepts 42.0 from a JSON-decoded stream as well as
// int(42) from a Go caller.
func validateJSONSchema(schema llm.Schema, value any) error {
	return validateAt(schema, value, "")
}

func validateAt(schema llm.Schema, value any, path string) error {
	if schema.Type != "" {
		if err := checkType(schema.Type, value, path); err != nil {
			return err
		}
	}
	// Enum is one Go type now (the typed Schema canonicalises it), so there's
	// no []string-vs-[]any reconciliation here any more.
	if len(schema.Enum) > 0 {
		if !slices.ContainsFunc(schema.Enum, func(e any) bool { return e == value }) {
			return fmt.Errorf("%s: value %v not in enum %v", fieldPath(path), value, schema.Enum)
		}
	}
	if obj, ok := value.(map[string]any); ok {
		if err := validateObject(schema, obj, path); err != nil {
			return err
		}
	}
	if arr, ok := value.([]any); ok && schema.Items != nil {
		for i, v := range arr {
			if err := validateAt(*schema.Items, v, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateObject(schema llm.Schema, obj map[string]any, path string) error {
	for _, k := range schema.Required {
		if _, present := obj[k]; !present {
			return fmt.Errorf("missing required field %q", joinPath(path, k))
		}
	}
	for name, sub := range schema.Properties {
		v, present := obj[name]
		if !present {
			continue
		}
		if err := validateAt(sub, v, joinPath(path, name)); err != nil {
			return err
		}
	}
	if allow, ok := schema.AdditionalProperties.(bool); ok && !allow && schema.Properties != nil {
		for name := range obj {
			if _, declared := schema.Properties[name]; !declared {
				return fmt.Errorf("unexpected field %q", joinPath(path, name))
			}
		}
	}
	return nil
}

func checkType(want string, value any, path string) error {
	// null is permitted unless explicitly disallowed — matches LLM
	// behavior of omitting optional fields with null-ish values.
	if value == nil {
		return nil
	}
	actual := jsonType(value)
	if !typeMatches(want, actual) {
		return fmt.Errorf("%s: expected %s, got %s", fieldPath(path), want, actual)
	}
	return nil
}

// jsonType reports the JSON-schema type name for value. Whole-number
// float64s (the shape JSON unmarshaling produces) classify as integer
// so an "integer" schema accepts JSON's 42 → float64(42).
func jsonType(value any) string {
	switch v := value.(type) {
	case string:
		return schemaTypeStr
	case bool:
		return "boolean"
	case float64:
		if v == float64(int64(v)) {
			return jsonTypeInteger
		}
		return jsonTypeNumber
	case float32:
		if float64(v) == float64(int64(v)) {
			return jsonTypeInteger
		}
		return jsonTypeNumber
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return jsonTypeInteger
	case map[string]any:
		return schemaObj
	case []any:
		return "array"
	case nil:
		return "null"
	default:
		return fmt.Sprintf("%T", value)
	}
}

func typeMatches(want, actual string) bool {
	if want == actual {
		return true
	}
	// integer satisfies number — JSON-schema convention.
	if want == jsonTypeNumber && actual == jsonTypeInteger {
		return true
	}
	return false
}

func fieldPath(p string) string {
	if p == "" {
		return "field"
	}
	return p
}

func joinPath(parent, name string) string {
	if parent == "" {
		return name
	}
	return parent + "." + name
}
