package tools

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/mcp"
)

// An MCP tool's discovered schema must reach the model verbatim via
// ToolSpec.Parameters — rich JSON Schema features preserved, only cosmetic
// keys stripped. Guards both the schema-reaches-the-model fix and the
// single-field collapse (there is no RawSchema to fall back to).
func TestNewRemoteTool_SchemaLandsInParameters(t *testing.T) {
	def := mcp.ToolDef{
		Name:        "search",
		Description: "search the web",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "the query", // cosmetic — should be stripped
				},
				"limit": map[string]any{
					"anyOf": []any{
						map[string]any{"type": "integer", "minimum": float64(1)},
						map[string]any{"type": "null"},
					},
				},
			},
			"required": []any{"query"},
		},
	}

	spec := NewRemoteTool(nil, def).Definition()

	if spec.Parameters.IsZero() {
		t.Fatal("MCP schema must land in ToolSpec.Parameters, got nil")
	}
	props, ok := spec.Parameters.Map()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties missing from preserved schema: %#v", spec.Parameters)
	}

	// Rich feature (anyOf with minimum) must survive verbatim.
	limit, _ := props["limit"].(map[string]any)
	anyOf, ok := limit["anyOf"].([]any)
	if !ok || len(anyOf) != 2 {
		t.Errorf("anyOf must survive verbatim, got %#v", limit)
	} else if first, _ := anyOf[0].(map[string]any); first["minimum"] != float64(1) {
		t.Errorf("nested minimum must survive, got %#v", first)
	}

	// Type survives; cosmetic description is stripped.
	query, _ := props["query"].(map[string]any)
	if query["type"] != "string" {
		t.Errorf("type must survive, got %#v", query)
	}
	if _, hasDesc := query["description"]; hasDesc {
		t.Errorf("cosmetic 'description' should be stripped, got %#v", query)
	}
}

// A remote MCP tool carries no trustworthy read-only/destructive
// annotation, so it must default to Mutates:true — otherwise it reads as
// non-mutating and slips spawn's read-only explore/verify gates.
func TestNewRemoteTool_DefaultsMutating(t *testing.T) {
	spec := NewRemoteTool(nil, mcp.ToolDef{Name: "send_email", Description: "send an email"}).Definition()
	if !spec.Mutates {
		t.Error("remote MCP tool must default Mutates:true (conservative; no trusted read-only annotation)")
	}
}
