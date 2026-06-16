package service_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zarlai/service"
)

func TestGet(t *testing.T) {
	tests := []struct {
		name string
		args service.Arguments
		key  string
		want any
		fn   func(service.Arguments, string) any
	}{
		{
			name: "string hit",
			args: service.Arguments{"name": "living_room"},
			key:  "name",
			want: "living_room",
			fn:   func(a service.Arguments, k string) any { return service.Get[string](a, k) },
		},
		{
			name: "string miss",
			args: service.Arguments{},
			key:  "name",
			want: "",
			fn:   func(a service.Arguments, k string) any { return service.Get[string](a, k) },
		},
		{
			name: "string wrong type",
			args: service.Arguments{"name": 42},
			key:  "name",
			want: "",
			fn:   func(a service.Arguments, k string) any { return service.Get[string](a, k) },
		},
		{
			name: "float64 hit",
			args: service.Arguments{"brightness": 0.75},
			key:  "brightness",
			want: 0.75,
			fn:   func(a service.Arguments, k string) any { return service.Get[float64](a, k) },
		},
		{
			name: "bool hit",
			args: service.Arguments{"on": true},
			key:  "on",
			want: true,
			fn:   func(a service.Arguments, k string) any { return service.Get[bool](a, k) },
		},
		{
			name: "bool miss returns false",
			args: service.Arguments{},
			key:  "on",
			want: false,
			fn:   func(a service.Arguments, k string) any { return service.Get[bool](a, k) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.fn(tt.args, tt.key)
			if got != tt.want {
				t.Errorf("Get() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParametersToJSONSchema(t *testing.T) {
	params := service.Parameters{
		{Name: "entity_id", Type: service.ParamString, Description: "The entity ID", Required: true},
		{Name: "brightness", Type: service.ParamNumber, Description: "Brightness level", Required: false},
		{Name: "mode", Type: service.ParamString, Description: "Operating mode", Required: false, Enum: []string{"heat", "cool", "auto"}},
	}

	schema := params.ToJSONSchema().Map()

	// Check type
	if schema["type"] != "object" {
		t.Fatalf("type = %v, want object", schema["type"])
	}

	// Check properties
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("properties not a map")
	}
	if len(props) != 3 {
		t.Fatalf("len(properties) = %d, want 3", len(props))
	}

	entityProp, ok := props["entity_id"].(map[string]any)
	if !ok {
		t.Fatal("entity_id property not a map")
	}
	if entityProp["type"] != "string" {
		t.Errorf("entity_id type = %v, want string", entityProp["type"])
	}
	if entityProp["description"] != "The entity ID" {
		t.Errorf("entity_id description = %v", entityProp["description"])
	}

	modeProp, ok := props["mode"].(map[string]any)
	if !ok {
		t.Fatal("mode property not a map")
	}
	enumVal, ok := modeProp["enum"].([]any)
	if !ok {
		t.Fatal("mode enum not []any")
	}
	if len(enumVal) != 3 {
		t.Fatalf("len(enum) = %d, want 3", len(enumVal))
	}

	// Check required
	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatal("required not []string")
	}
	if len(required) != 1 || required[0] != "entity_id" {
		t.Errorf("required = %v, want [entity_id]", required)
	}
}
