package runner_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestReplayJSONPreservesStringBytes(t *testing.T) {
	const raw = "raw\xff\x00\xfe"
	const escaped = "~zarl-history-bytes:literal"
	want := runner.ReplayMessage{
		Interrupted: true,
		Message: llm.Message{Role: llm.RoleAssistant, Content: raw, ReasoningContent: escaped,
			Parts:             []llm.ContentPart{llm.TextPart(raw), {Type: llm.ContentTypeImage, Image: &llm.ImageData{URL: raw, Detail: escaped}}},
			ContinuationItems: []llm.ContinuationItem{{Provider: "openai", Format: escaped, Data: []byte(raw)}},
		},
		RawToolCalls: []llm.ToolCall{{ID: raw, Function: llm.ToolCallFunction{Name: escaped, Arguments: raw}}},
		Tool:         &runner.ToolOutput{Args: raw, Output: raw, Error: escaped, Parameters: tools.ToolParameters{raw: []any{raw, escaped, nil, map[string]any{escaped: raw}}}},
	}
	data, err := runner.MarshalReplayMessage(want)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(data) {
		t.Fatal("storage is not valid JSON")
	}
	got, err := runner.UnmarshalReplayMessage(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("byte round trip changed occurrence: got %#v want %#v", got, want)
	}
	got.Message.Parts[1].Image.URL = "changed"
	got.Message.ContinuationItems[0].Data[0] = '!'
	got.Tool.Parameters[raw].([]any)[0] = "changed"
	if want.Message.Parts[1].Image.URL != raw || string(want.Message.ContinuationItems[0].Data) != raw || want.Tool.Parameters[raw].([]any)[0] != raw {
		t.Fatal("codec mutated or aliased borrowed input")
	}
	again, err := runner.MarshalReplayMessage(want)
	if err != nil || !bytes.Equal(data, again) {
		t.Fatalf("serialization changed borrowed input: %v", err)
	}
}

func TestHistoryRequestJSONPreservesSchemaBytes(t *testing.T) {
	const raw = "field\xff"
	const escaped = "~zarl-history-bytes:literal"
	schema := llm.Schema{Type: "object", Description: raw,
		Properties:    map[string]llm.Schema{raw: {Type: "string", Enum: []any{raw, escaped}}},
		PropertyOrder: []string{raw}, Required: []string{raw}, Extra: map[string]any{"examples": []any{map[string]any{raw: escaped}}},
	}
	want := llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: raw}}, ResponseFormat: llm.JSONSchemaResponseFormat(escaped, schema, true),
		Tools: []llm.Tool{{Type: "function", Function: llm.ToolFunction{Name: "ordered", Parameters: llm.Schema{
			Type: "object", Properties: map[string]llm.Schema{"nested": schema}, PropertyOrder: []string{"nested"},
			Items: &schema, AdditionalProperties: schema,
			Extra: map[string]any{"~zarl-history-bytes:schema-order": []any{escaped}},
		}}}},
	}
	data, err := runner.MarshalHistoryRequest(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := runner.UnmarshalHistoryRequest(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("prepared schema bytes changed: got %#v want %#v", got.ResponseFormat.Schema, want.ResponseFormat.Schema)
	}
	got.ResponseFormat.Schema.Properties[raw] = llm.Schema{}
	if want.ResponseFormat.Schema.Properties[raw].Type != "string" {
		t.Fatal("decoded schema aliases input")
	}
}

func TestReplayJSONLegacyAndStrictDecoding(t *testing.T) {
	legacy, err := runner.UnmarshalReplayMessage([]byte(`{"message":{"role":"user","content":"~zarl-history-bytes:literal"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if legacy.Message.Content != "~zarl-history-bytes:literal" {
		t.Fatal("legacy content interpreted as an escape")
	}
	for _, data := range []string{
		`{"encoding":"byte-strings.v1","value":{"message":{"content":"~zarl-history-bytes:!"}}}`,
		`{"encoding":"byte-strings.v1","value":{"message":{}},"unknown":true}`,
		`{"encoding":"byte-strings.v1","value":{"message":{},"unknown":true}}`,
		`{"message":{},"unknown":true}`,
		`{"encoding":"future","value":{}}`,
		`{"message":{}} {}`,
	} {
		t.Run(data, func(t *testing.T) {
			if _, err := runner.UnmarshalReplayMessage([]byte(data)); err == nil {
				t.Fatal("accepted invalid storage encoding")
			}
		})
	}
}

func TestReplayJSONObservationRoundTripAndValidation(t *testing.T) {
	want := runner.ReplayMessage{Message: llm.Message{Role: llm.RoleUser, Content: "reported evidence", Observation: llm.ObservationProvenance{Version: 1, ID: "child:1"}}}
	data, err := runner.MarshalReplayMessage(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := runner.UnmarshalReplayMessage(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("observation round trip: got %#v want %#v", got, want)
	}
	for _, invalid := range []string{
		`{"message":{"role":"assistant","content":"evidence","observation":{"version":1,"id":"child:1"}}}`,
		`{"message":{"role":"user","content":"evidence","observation":{"version":1}}}`,
		`{"message":{"role":"user","content":"evidence","observation":{"version":2,"id":"child:1"}}}`,
	} {
		if _, err := runner.UnmarshalReplayMessage([]byte(invalid)); err == nil {
			t.Fatalf("accepted invalid observation: %s", invalid)
		}
	}
}
