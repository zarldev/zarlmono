package tools_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/mcp"
)

// An MCP tool's discovered schema must reach the model verbatim via
// ToolSpec.Parameters: rich JSON Schema features are preserved while cosmetic
// keys are stripped.
func TestNewRemoteToolSchemaLandsInParameters(t *testing.T) {
	t.Parallel()
	def := mcp.ToolDef{
		Name:        "search",
		Description: "search the web",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "the query",
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

	spec := tools.NewRemoteTool(nil, def).Definition()
	if spec.Parameters.IsZero() {
		t.Fatal("MCP schema must land in ToolSpec.Parameters, got nil")
	}
	props, ok := spec.Parameters.Map()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties missing from preserved schema: %#v", spec.Parameters)
	}
	limit, _ := props["limit"].(map[string]any)
	anyOf, ok := limit["anyOf"].([]any)
	if !ok || len(anyOf) != 2 {
		t.Errorf("anyOf must survive verbatim, got %#v", limit)
	} else if first, _ := anyOf[0].(map[string]any); first["minimum"] != float64(1) {
		t.Errorf("nested minimum must survive, got %#v", first)
	}
	query, _ := props["query"].(map[string]any)
	if query["type"] != "string" {
		t.Errorf("type must survive, got %#v", query)
	}
	if _, hasDesc := query["description"]; hasDesc {
		t.Errorf("cosmetic description should be stripped, got %#v", query)
	}
}

func TestNewRemoteToolDefaultsMutating(t *testing.T) {
	t.Parallel()
	spec := tools.NewRemoteTool(nil, mcp.ToolDef{Name: "send_email", Description: "send an email"}).Definition()
	if !spec.Mutates {
		t.Error("remote MCP tool must default Mutates:true")
	}
}
