package compact_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/anthropic"
)

func TestStructuralPreservesOpaqueItemsWithoutTruncationAndOwnsCopies(t *testing.T) {
	payload := make([]byte, 2048)
	for i := range payload {
		payload[i] = byte(i)
	}
	history := []llm.Message{
		{Role: llm.RoleAssistant, Content: "short", ContinuationItems: []llm.ContinuationItem{{Provider: "p", Format: "f", Data: payload}}},
		{Role: llm.RoleUser, Content: "recent"},
	}

	result, err := compact.NewStructural().Compact(t.Context(), history, 1)
	if err != nil {
		t.Fatal(err)
	}
	got := result.History[0].ContinuationItems[0].Data
	if len(got) != len(payload) {
		t.Fatalf("opaque payload len = %d, want %d", len(got), len(payload))
	}
	payload[0] = 99
	if got[0] == 99 {
		t.Fatal("compacted history aliases opaque input bytes")
	}
}

func TestTieredPhase3DropsWholeOldNonActionOpaqueItems(t *testing.T) {
	payload := make([]byte, 1200)
	history := []llm.Message{
		{Role: llm.RoleAssistant, Content: "projected answer", ReasoningContent: "display reasoning", ContinuationItems: []llm.ContinuationItem{{Provider: "p", Format: "f", Data: payload}}},
		{Role: llm.RoleUser, Content: "recent", ContinuationItems: []llm.ContinuationItem{{Provider: "p", Format: "f", Data: []byte("recent-native")}}},
	}
	tiered := &compact.Tiered{TargetBytes: 1000, Phase1Threshold: .60, Phase2Threshold: .75, Phase3Threshold: .90}

	if got := tiered.WouldReduceBytes(history, 1); got <= 0 {
		t.Fatalf("WouldReduceBytes = %d, want positive from opaque bytes", got)
	}
	result, err := tiered.Compact(t.Context(), history, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.History[0].ContinuationItems) != 0 || result.History[0].ReasoningContent != "" {
		t.Fatalf("old non-action native state retained: %#v", result.History[0])
	}
	if result.History[0].Content != "projected answer" {
		t.Fatalf("projected assistant content lost: %#v", result.History[0])
	}
	if got := string(result.History[1].ContinuationItems[0].Data); got != "recent-native" {
		t.Fatalf("recent continuation item = %q", got)
	}
	if result.BytesTrimmed < 1200 {
		t.Fatalf("BytesTrimmed = %d, want dominant native payload reduction", result.BytesTrimmed)
	}
}

func TestTieredPreservesNativeStateForRetainedToolCalls(t *testing.T) {
	for _, tc := range []struct {
		name   string
		phase3 float64
	}{
		{name: "phase 2", phase3: 100},
		{name: "phase 3", phase3: .5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := []byte(`{"type":"reasoning","id":"signed-item","signature":"exact-provider-signature"}`)
			history := []llm.Message{
				{
					Role: llm.RoleAssistant, Content: strings.Repeat("projected reasoning ", 100), ContentOutputIndex: llm.OutputPosition(2), ReasoningContent: "exact reasoning",
					Parts:             []llm.ContentPart{llm.TextPart("exact attachment")},
					ToolCalls:         []llm.ToolCall{{ID: "call-1", Type: "function", OutputIndex: llm.OutputPosition(3), Function: llm.ToolCallFunction{Name: "read", Arguments: `{}`}}},
					ContinuationItems: []llm.ContinuationItem{{OutputIndex: llm.OutputPosition(1), Provider: "signed-provider", Format: "native-v1", Data: payload}},
				},
				{Role: llm.RoleTool, ToolCallID: "call-1", Content: "result"},
				{Role: llm.RoleUser, Content: "recent"},
			}
			original := llm.CloneMessages(history)
			tiered := &compact.Tiered{TargetBytes: 1000, Phase1Threshold: .1, Phase2Threshold: .2, Phase3Threshold: tc.phase3}

			probe := tiered.WouldReduceBytes(history, 1)
			result, err := tiered.Compact(t.Context(), history, 1)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(history, original) {
				t.Fatal("Compact mutated borrowed history")
			}
			var retained *llm.Message
			for i := range result.History {
				if len(result.History[i].ToolCalls) > 0 {
					retained = &result.History[i]
					break
				}
			}
			if retained == nil {
				t.Fatal("retained tool call missing")
			}
			if !reflect.DeepEqual(*retained, original[0]) {
				t.Fatalf("retained native action changed: %#v", retained)
			}
			actual := retainedHistoryBytes(history) - retainedHistoryBytes(result.History)
			if probe != result.BytesTrimmed || probe != actual {
				t.Fatalf("untruthful savings: probe=%d reported=%d actual=%d", probe, result.BytesTrimmed, actual)
			}
			payload[0] = 'x'
			if retained.ContinuationItems[0].Data[0] == 'x' {
				t.Fatal("retained native state aliases borrowed input")
			}
		})
	}
}

func TestTieredNativeActionAnthropicWireReplayUnchanged(t *testing.T) {
	longText := strings.Repeat("native visible text ", 200)
	longThinking := strings.Repeat("native signed thinking ", 200)
	assistant := llm.Message{
		Role: llm.RoleAssistant, Content: longText, ContentOutputIndex: llm.OutputPosition(1), ReasoningContent: longThinking,
		ToolCalls: []llm.ToolCall{{
			ID: "toolu_1", Type: "function", OutputIndex: llm.OutputPosition(2),
			Function: llm.ToolCallFunction{Name: "echo", Arguments: `{"x":1}`},
		}},
		ContinuationItems: []llm.ContinuationItem{
			{OutputIndex: llm.OutputPosition(0), Provider: "anthropic", Format: "content_block.v1", Kind: "thinking", Data: []byte(`{"type":"thinking","thinking":` + quotedJSON(t, longThinking) + `,"signature":"signed-exact"}`)},
			{OutputIndex: llm.OutputPosition(1), Provider: "anthropic", Format: "content_block.v1", Kind: "text", Data: []byte(`{"type":"text","text":` + quotedJSON(t, longText) + `}`)},
		},
	}
	history := []llm.Message{
		{Role: llm.RoleUser, Content: "run the tool"},
		assistant,
		{Role: llm.RoleTool, ToolCallID: "toolu_1", Content: strings.Repeat("tool result ", 200)},
		{Role: llm.RoleUser, Content: "continue"},
	}
	compactor := compact.Tiered{TargetBytes: 1000, Phase1Threshold: .1, Phase2Threshold: .2, Phase3Threshold: 100}
	probe := compactor.WouldReduceBytes(history, 1)
	result, err := compactor.Compact(t.Context(), history, 1)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	actual := retainedHistoryBytes(history) - retainedHistoryBytes(result.History)
	if probe <= 0 || probe != result.BytesTrimmed || probe != actual {
		t.Fatalf("untruthful savings: probe=%d reported=%d actual=%d", probe, result.BytesTrimmed, actual)
	}
	if !reflect.DeepEqual(result.History[1], assistant) {
		t.Fatalf("native action changed during compaction: %#v", result.History[1])
	}
	for _, message := range result.History {
		if message.Observation.Version != 0 && strings.Contains(message.Content, longText) {
			t.Fatal("compaction added a host excerpt of native assistant text")
		}
	}

	requests := make(chan map[string]any, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		requests <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_fixture","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"claude-test","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	t.Cleanup(server.Close)
	provider := anthropic.NewProvider("test-key", anthropic.WithBaseURL(server.URL))
	send := func(messages []llm.Message) map[string]any {
		t.Helper()
		for _, completeErr := range provider.Complete(t.Context(), llm.CompletionRequest{Messages: messages}) {
			if completeErr != nil {
				t.Fatalf("Complete: %v", completeErr)
			}
		}
		return <-requests
	}
	uninterrupted := anthropicAssistantBlocks(t, send(history))
	compacted := anthropicAssistantBlocks(t, send(result.History))
	if !reflect.DeepEqual(compacted, uninterrupted) {
		t.Fatalf("compacted native replay differs\nuninterrupted: %#v\ncompacted: %#v", uninterrupted, compacted)
	}
}

func quotedJSON(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func anthropicAssistantBlocks(t *testing.T, body map[string]any) []any {
	t.Helper()
	for _, raw := range body["messages"].([]any) {
		message := raw.(map[string]any)
		if message["role"] == "assistant" {
			return message["content"].([]any)
		}
	}
	t.Fatal("assistant message missing from Anthropic request")
	return nil
}
