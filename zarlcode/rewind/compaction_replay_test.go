package rewind_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestTieredCompactionPreservesCheckpointAdmission(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		text      string
		tool      bool
		phase3    float64
		wantPhase string
		wantCount int
	}{
		{name: "phase two native text", text: strings.Repeat("answer ", 200), phase3: 100, wantPhase: "phase 2", wantCount: 2},
		{name: "phase three reasoning and tools", tool: true, phase3: .9, wantPhase: "phase 3", wantCount: 3},
		{name: "phase three native text and tools", text: strings.Repeat("answer ", 200), tool: true, phase3: .3, wantPhase: "phase 3", wantCount: 4},
		{name: "phase three reasoning only", phase3: .9, wantPhase: "phase 3", wantCount: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			message := llm.Message{Role: llm.RoleAssistant, Content: tc.text, ReasoningContent: "display reasoning", ContinuationItems: []llm.ContinuationItem{{
				Provider: "openai-codex", Format: "responses.reasoning.v1", Kind: "reasoning", OutputIndex: llm.OutputPosition(0),
				Data: []byte(`{"type":"reasoning","id":"rs","encrypted_content":"` + strings.Repeat("x", 1500) + `"}`),
			}}}
			if tc.text != "" {
				text, err := json.Marshal(tc.text)
				if err != nil {
					t.Fatal(err)
				}
				message.ContentOutputIndex = llm.OutputPosition(1)
				message.ContinuationItems = append(message.ContinuationItems, llm.ContinuationItem{
					Provider: "openai-codex", Format: "responses.reasoning.v1", Kind: "message", ID: "msg", OutputIndex: llm.OutputPosition(1),
					Data: []byte(`{"type":"message","id":"msg","role":"assistant","content":[{"type":"output_text","text":` + string(text) + `}]}`),
				})
			}
			if tc.tool {
				position := 1
				if tc.text != "" {
					position = 2
				}
				message.ToolCalls = []llm.ToolCall{{ID: "call", Type: "function", OutputIndex: llm.OutputPosition(position), Function: llm.ToolCallFunction{Name: "read", Arguments: `{}`}}}
			}
			history := []llm.Message{message}
			if tc.tool {
				history = append(history, llm.Message{Role: llm.RoleTool, ToolCallID: "call", Content: "result"})
			}
			history = append(history, llm.Message{Role: llm.RoleUser, Content: "continue"})
			original := llm.CloneMessages(history)
			if err := rewind.ValidateContext(history, "openai-codex"); err != nil {
				t.Fatalf("input: %v", err)
			}
			compactor := compact.Tiered{TargetBytes: 1000, Phase1Threshold: .1, Phase2Threshold: .2, Phase3Threshold: tc.phase3}
			predicted := compactor.WouldReduceBytes(history, 1)
			result, err := compactor.Compact(t.Context(), history, 1)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(result.Warning, tc.wantPhase) || len(result.History) != tc.wantCount {
				t.Fatalf("compaction: %s, messages=%d", result.Warning, len(result.History))
			}
			if predicted != result.BytesTrimmed || predicted <= 0 {
				t.Fatalf("probe=%d, saved=%d", predicted, result.BytesTrimmed)
			}
			if !reflect.DeepEqual(history, original) || !reflect.DeepEqual(result.History[len(result.History)-1], original[len(original)-1]) {
				t.Fatal("compaction changed borrowed history or the protected recent message")
			}
			if tc.text != "" {
				excerpt := result.History[0]
				if excerpt.Role != llm.RoleUser || excerpt.Observation.Version != 1 || excerpt.Observation.ID == "" {
					t.Fatal("compacted prose is not attributed as host input")
				}
				if tc.tool && (result.History[1].Role != llm.RoleAssistant || result.History[1].Content != "" || result.History[2].ToolCallID != "call") {
					t.Fatal("host excerpt interrupted the native tool-call shell and result")
				}
			}
			encoded, err := rewind.EncodeResume(1, result.History, rewind.Target{Provider: "openai-codex", Model: "gpt-6-astra"}, "turn", 1, rewind.InitialContinuation{})
			if err != nil {
				t.Fatalf("save compacted context: %v", err)
			}
			decoded, err := rewind.DecodeResume(encoded)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(decoded.Context, result.History) {
				t.Fatal("save/resume changed compacted context")
			}
		})
	}
}
