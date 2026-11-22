package google_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

type toolSchemaRequest struct {
	Tools []struct {
		FunctionDeclarations []struct {
			Name                 string          `json:"name"`
			Description          string          `json:"description"`
			Parameters           json.RawMessage `json:"parameters"`
			ParametersJSONSchema json.RawMessage `json:"parametersJsonSchema"`
		} `json:"functionDeclarations"`
	} `json:"tools"`
}

func TestProviderPreservesToolJSONSchema(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		schema string
	}{
		{
			name: "nested references and definitions",
			schema: `{
				"type":"object",
				"$defs":{"entry":{
					"type":"object",
					"properties":{"label":{"$ref":"#/$defs/label"}},
					"required":["label"],
					"additionalProperties":false
				},"label":{"type":"string","minLength":1}},
				"properties":{"entries":{"type":"array","items":{"$ref":"#/$defs/entry"}}},
				"required":["entries"],
				"additionalProperties":false
			}`,
		},
		{
			name: "composition bounds and additional properties",
			schema: `{
				"type":"object",
				"properties":{
					"choice":{"anyOf":[{"type":"string","pattern":"^[a-z]+$"},{"type":"integer","minimum":0,"maximum":10}]},
					"labels":{"type":"array","minItems":1,"maxItems":3,"items":{"type":"string","minLength":1,"maxLength":20}},
					"counts":{"type":"object","additionalProperties":{"type":"integer","minimum":0}}
				},
				"required":["choice","labels"],
				"additionalProperties":false
			}`,
		},
		{
			name: "numeric enums and supported fields",
			schema: `{
				"type":"object",
				"description":"Choose a level and mode",
				"properties":{
					"level":{"type":"integer","enum":[0,1,2]},
					"ratio":{"type":"number","enum":[0.25,0.5,1]},
					"mode":{"type":"string","description":"Operating mode","enum":["fast","slow"]},
					"enabled":{"type":"boolean"}
				},
				"required":["level","mode"]
			}`,
		},
	}
	for _, route := range googleRoutes {
		t.Run(route.name, func(t *testing.T) {
			t.Parallel()
			for _, test := range tests {
				t.Run(test.name, func(t *testing.T) {
					t.Parallel()
					var schema llm.Schema
					if err := json.Unmarshal([]byte(test.schema), &schema); err != nil {
						t.Fatalf("decode schema fixture: %v", err)
					}
					tools := []llm.Tool{
						{Type: "function", Function: llm.ToolFunction{Name: "z_schema", Description: "Use the supplied schema", Parameters: schema}},
						{Type: "function", Function: llm.ToolFunction{Name: "a_no_args"}},
						{Type: "function", Function: llm.ToolFunction{Name: "m_empty_object", Parameters: llm.Schema{Type: "object", Properties: map[string]llm.Schema{}}}},
					}
					before, err := json.Marshal(tools)
					if err != nil {
						t.Fatalf("snapshot tools: %v", err)
					}
					requests := make(chan toolSchemaRequest, 1)
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						var request toolSchemaRequest
						if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
							t.Errorf("decode request: %v", err)
						}
						requests <- request
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = w.Write([]byte("data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n"))
					}))
					t.Cleanup(server.Close)

					provider := newGoogleRouteProvider(t, route, server.URL)
					for _, err := range provider.Complete(t.Context(), llm.CompletionRequest{
						Stream:   true,
						Messages: []llm.Message{{Role: llm.RoleUser, Content: "Use a tool"}},
						Tools:    tools,
					}) {
						if err != nil {
							t.Fatalf("Complete: %v", err)
						}
					}
					after, err := json.Marshal(tools)
					if err != nil {
						t.Fatalf("marshal tools after completion: %v", err)
					}
					if !bytes.Equal(before, after) {
						t.Fatalf("completion mutated input tools:\nbefore: %s\nafter: %s", before, after)
					}

					var request toolSchemaRequest
					select {
					case request = <-requests:
					default:
						t.Fatal("completion did not dispatch a request")
					}
					if len(request.Tools) != 1 || len(request.Tools[0].FunctionDeclarations) != len(tools) {
						t.Fatalf("tools = %#v, want one tool containing %d declarations", request.Tools, len(tools))
					}
					decls := request.Tools[0].FunctionDeclarations
					for i, tool := range tools {
						if decls[i].Name != tool.Function.Name || decls[i].Description != tool.Function.Description {
							t.Errorf("declaration %d = %q (%q), want %q (%q)", i, decls[i].Name, decls[i].Description, tool.Function.Name, tool.Function.Description)
						}
						if len(decls[i].Parameters) != 0 {
							t.Errorf("declaration %d has mutually exclusive parameters: %s", i, decls[i].Parameters)
						}
					}
					assertToolSchemaJSON(t, decls[0].ParametersJSONSchema, test.schema)
					if len(decls[1].ParametersJSONSchema) != 0 {
						t.Errorf("no-argument tool has parametersJsonSchema: %s", decls[1].ParametersJSONSchema)
					}
					assertToolSchemaJSON(t, decls[2].ParametersJSONSchema, `{"type":"object","properties":{}}`)
				})
			}
		})
	}
}

func assertToolSchemaJSON(t *testing.T, got json.RawMessage, want string) {
	t.Helper()
	var gotSchema, wantSchema any
	if err := json.Unmarshal(got, &gotSchema); err != nil {
		t.Fatalf("decode wire schema %s: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &wantSchema); err != nil {
		t.Fatalf("decode expected schema: %v", err)
	}
	if !reflect.DeepEqual(gotSchema, wantSchema) {
		t.Errorf("parametersJsonSchema = %s, want %s", got, want)
	}
}
