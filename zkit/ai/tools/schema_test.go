package tools_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

type schemaArgs struct {
	Name  string            `json:"name" doc:"display name"`
	Mode  string            `json:"mode,omitempty" enum:"fast, safe"`
	Count int               `json:"count"`
	Flags map[string]string `json:"flags,omitempty"`
	Skip  string            `json:"-"`
}

func TestSchemaForStructTags(t *testing.T) {
	t.Parallel()

	schema := tools.SchemaFor[schemaArgs]().Map()
	if got := schema["type"]; got != "object" {
		t.Fatalf("type = %v, want object", got)
	}
	if got := schema["additionalProperties"]; got != false {
		t.Fatalf("additionalProperties = %v, want false", got)
	}

	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties has type %T, want map[string]any", schema["properties"])
	}
	name, ok := props["name"].(map[string]any)
	if !ok {
		t.Fatalf("name property has type %T, want map[string]any", props["name"])
	}
	if got := name["type"]; got != "string" {
		t.Fatalf("name.type = %v, want string", got)
	}
	if got := name["description"]; got != "display name" {
		t.Fatalf("name.description = %v, want display name", got)
	}

	mode, ok := props["mode"].(map[string]any)
	if !ok {
		t.Fatalf("mode property has type %T, want map[string]any", props["mode"])
	}
	gotEnum, ok := mode["enum"].([]any)
	if !ok {
		t.Fatalf("mode.enum has type %T, want []any", mode["enum"])
	}
	if len(gotEnum) != 2 || gotEnum[0] != "fast" || gotEnum[1] != "safe" {
		t.Fatalf("mode.enum = %#v, want fast/safe", gotEnum)
	}
	if _, ok := props["Skip"]; ok {
		t.Fatal("json:- field was included in schema")
	}

	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatalf("required has type %T, want []string", schema["required"])
	}
	if !sameStrings(required, []string{"name", "count"}) {
		t.Fatalf("required = %#v, want name/count", required)
	}
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
