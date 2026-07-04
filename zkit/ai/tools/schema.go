package tools

import (
	"maps"
	"reflect"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

const (
	schemaTypeArray   = "array"
	schemaTypeBoolean = "boolean"
	schemaTypeInteger = "integer"
	schemaTypeNumber  = "number"
	schemaTypeObject  = "object"
	schemaTypeString  = "string"
)

// SchemaFor reflects over T and returns the tool's parameter schema as a typed
// llm.Schema (the shape tools.ToolSpec.Parameters expects) so a tool author
// never has to hand-write a schema tree. Conventions are the standard json-tag
// set plus two ergonomic additions:
//
//	`json:"name"`            field name (drop with `json:"-"`)
//	`json:",omitempty"`      field is optional
//	pointer / interface type field is optional
//	`doc:"..."`              human-readable description shown to the LLM
//	`description:"..."`      alias for doc
//	`enum:"a,b,c"`           restrict the value to a fixed set
//
// Required fields are everything that isn't a pointer and doesn't carry
// omitempty. Order is preserved from the struct declaration. Unsupported types
// (channels, funcs, complex numbers, etc.) produce the zero Schema.
//
// SchemaFor is the lazy escape hatch. Tool authors who need finer control
// (oneOf, allOf, format constraints, custom $ref) build the llm.Schema by hand,
// putting those keys in its Extra field.
func SchemaFor[T any]() llm.Schema {
	var zero T
	return schemaFromType(reflect.TypeOf(zero))
}

// schemaFromType is the recursive worker. For pointer / interface the elem is
// used; pointer-ness only affects the parent's required list.
func schemaFromType(t reflect.Type) llm.Schema {
	if t == nil {
		return llm.Schema{}
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	switch t.Kind() {
	case reflect.String:
		return llm.Schema{Type: schemaTypeString}
	case reflect.Bool:
		return llm.Schema{Type: schemaTypeBoolean}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return llm.Schema{Type: schemaTypeInteger}
	case reflect.Float32, reflect.Float64:
		return llm.Schema{Type: schemaTypeNumber}
	case reflect.Slice, reflect.Array:
		item := schemaFromType(t.Elem())
		return llm.Schema{Type: schemaTypeArray, Items: &item}
	case reflect.Map:
		// map[string]X → object with additionalProperties describing the value.
		if t.Key().Kind() != reflect.String {
			return llm.Schema{} // non-string keys have no clean JSON shape
		}
		return llm.Schema{
			Type:                 schemaTypeObject,
			AdditionalProperties: schemaFromType(t.Elem()),
		}
	case reflect.Struct:
		return structSchema(t)
	case reflect.Interface:
		// any / interface{} — leave open; the LLM gets no shape but the schema
		// doesn't lie about the type.
		return llm.Schema{}
	}
	return llm.Schema{}
}

// structSchema builds an object schema from a struct's fields, honouring
// json/doc/enum tags and the optional/required convention. Properties is a
// non-nil map even when empty so a no-arg tool still emits "properties":{},
// matching additionalProperties:false to forbid undeclared args.
func structSchema(t reflect.Type) llm.Schema {
	s := llm.Schema{
		Type:                 schemaTypeObject,
		Properties:           map[string]llm.Schema{},
		AdditionalProperties: false,
	}

	for f := range t.Fields() {
		if !f.IsExported() {
			continue
		}
		// Embedded structs flatten their fields into the parent.
		if f.Anonymous && f.Type.Kind() == reflect.Struct {
			child := structSchema(f.Type)
			maps.Copy(s.Properties, child.Properties)
			s.Required = append(s.Required, child.Required...)
			continue
		}

		name, optional, skip := jsonFieldMeta(f)
		if skip {
			continue
		}

		fieldSchema := schemaFromType(f.Type)
		if doc := fieldDoc(f); doc != "" {
			fieldSchema.Description = doc
		}
		if enum := fieldEnum(f); len(enum) > 0 {
			fieldSchema.Enum = enum
		}

		s.Properties[name] = fieldSchema
		if !optional && f.Type.Kind() != reflect.Pointer {
			s.Required = append(s.Required, name)
		}
	}

	return s
}

// jsonFieldMeta extracts the field's JSON name and whether it's optional.
// Returns skip=true if the field is excluded with json:"-".
func jsonFieldMeta(f reflect.StructField) (string, bool, bool) {
	tag := f.Tag.Get("json")
	if tag == "-" {
		return "", false, true
	}
	parts := strings.Split(tag, ",")
	var n string
	if len(parts) > 0 && parts[0] != "" {
		n = parts[0]
	} else {
		n = f.Name
	}
	var opt bool
	for _, o := range parts[1:] {
		if o == "omitempty" {
			opt = true
		}
	}
	return n, opt, false
}

// fieldDoc returns the human-readable description for a field — from
// `doc:"..."` (preferred) or `description:"..."` (alias).
func fieldDoc(f reflect.StructField) string {
	if d := f.Tag.Get("doc"); d != "" {
		return d
	}
	return f.Tag.Get("description")
}

// fieldEnum returns the `enum:"a,b,c"` values as []any (the Schema.Enum shape),
// whitespace-trimmed. Empty when the tag is absent.
func fieldEnum(f reflect.StructField) []any {
	raw := f.Tag.Get("enum")
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]any, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}
