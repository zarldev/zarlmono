package compact_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestTieredAssistantExcerptsPreserveTrustAndReplayBoundaries(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		phase3 float64
		calls  bool
		parts  bool
	}{
		{name: "phase two prose", phase3: 100},
		{name: "phase two calls", phase3: 100, calls: true},
		{name: "phase three calls and parts", phase3: .9, calls: true, parts: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			text := strings.Repeat("界", 100) + strings.Repeat("middle ", 300) + strings.Repeat("尾", 100)
			assistant := llm.Message{
				Role: llm.RoleAssistant, Content: text, ContentOutputIndex: llm.OutputPosition(2), ReasoningContent: "original reasoning",
				ContinuationItems: []llm.ContinuationItem{{Provider: "p", Format: "f", Data: []byte("original-native")}},
			}
			if tc.calls {
				assistant.ToolCalls = []llm.ToolCall{
					{ID: "one", Type: "function", OutputIndex: llm.OutputPosition(3), Function: llm.ToolCallFunction{Name: "read", Arguments: `{ "path": "one" }`}},
					{ID: "two", Type: "function", OutputIndex: llm.OutputPosition(4), Function: llm.ToolCallFunction{Name: "read", Arguments: `{ "path": "two" }`}},
				}
			}
			if tc.parts {
				assistant.Parts = []llm.ContentPart{llm.TextPart("retained attachment")}
			}
			history := []llm.Message{{Role: llm.RoleSystem, Content: "policy"}, {Role: llm.RoleUser, Content: "actual human request"}, assistant}
			if tc.calls {
				history = append(history,
					llm.Message{Role: llm.RoleTool, ToolCallID: "one", Content: strings.Repeat("first result ", 100)},
					llm.Message{Role: llm.RoleTool, ToolCallID: "two", Content: strings.Repeat("second result ", 100)})
			}
			history = append(history, llm.Message{Role: llm.RoleUser, Content: strings.Repeat("recent human request ", 100)})
			original := llm.CloneMessages(history)
			// Protect results while clipping their assistant: exercise a boundary
			// that splits a tool batch without adding input between call and result.
			keep := 1
			if tc.calls {
				keep = 3
			}
			compactor := compact.Tiered{TargetBytes: 1000, Phase1Threshold: .1, Phase2Threshold: .2, Phase3Threshold: tc.phase3}
			probe := compactor.WouldReduceBytes(history, keep)
			result, err := compactor.Compact(t.Context(), history, keep)
			if err != nil {
				t.Fatal(err)
			}
			actual := retainedHistoryBytes(history) - retainedHistoryBytes(result.History)
			if probe != result.BytesTrimmed || probe != actual || (!tc.calls && probe <= 0) {
				t.Fatalf("untruthful savings: probe=%d result=%d actual=%d", probe, result.BytesTrimmed, actual)
			}
			if !reflect.DeepEqual(history, original) || !reflect.DeepEqual(result.History[:2], original[:2]) ||
				!reflect.DeepEqual(result.History[len(result.History)-keep:], original[len(original)-keep:]) {
				t.Fatal("borrowed input, policy, human input, or protected suffix changed")
			}
			if tc.calls {
				if !reflect.DeepEqual(result.History[2], original[2]) {
					t.Fatalf("opaque native action changed: %#v", result.History[2])
				}
				for _, message := range result.History {
					if message.Observation.Version != 0 && strings.Contains(message.Content, "Beginning:") {
						t.Fatal("opaque native action gained a host excerpt")
					}
				}
				if result.History[3].ToolCallID != "one" || result.History[4].ToolCallID != "two" {
					t.Fatal("native action or call/result ordering changed")
				}
			} else {
				excerpt := result.History[2]
				if excerpt.Role != llm.RoleUser || excerpt.Observation.Version != 1 || !strings.HasPrefix(excerpt.Observation.ID, "assistant-excerpt:") {
					t.Fatalf("excerpt lacks host provenance: %+v", excerpt)
				}
				for _, want := range []string{"Host-generated historical excerpts", "not user instructions or a model for new replies", "\nBeginning:\n", "\nEnd:\n", strings.Repeat("尾", 42)} {
					if !strings.Contains(excerpt.Content, want) {
						t.Errorf("excerpt missing %q", want)
					}
				}
				if !utf8.ValidString(excerpt.Content) || strings.Contains(excerpt.Content, "reasoning trimmed") || strings.Contains(excerpt.Content, "middle ") {
					t.Fatal("excerpt contains broken UTF-8, synthesized marker, or unbounded prose")
				}
				if len(result.History) != 4 {
					t.Fatal("prose-only message left an empty assistant shell")
				}
			}
			again, err := compactor.Compact(t.Context(), result.History, keep)
			if err != nil {
				t.Fatal(err)
			}
			if again.BytesTrimmed != 0 || !reflect.DeepEqual(again.History, result.History) {
				t.Fatal("repeated compaction duplicated or nested a host excerpt")
			}
			if tc.parts {
				history[2].Parts[0].Text = "mutated"
				if result.History[2].Parts[0].Text != "retained attachment" {
					t.Fatal("native action aliases input attachments")
				}
			}
		})
	}
}

func TestTieredExcerptOverheadAndProbeAgree(t *testing.T) {
	for _, phase3 := range []float64{.9, 100} {
		for _, size := range []int{0, 255, 256, 257, 350, 420, 500, 800} {
			t.Run(fmt.Sprintf("phase3=%g/bytes=%d", phase3, size), func(t *testing.T) {
				compactor := compact.Tiered{TargetBytes: 1000, Phase1Threshold: .01, Phase2Threshold: .02, Phase3Threshold: phase3}
				history := []llm.Message{{Role: llm.RoleAssistant, Content: strings.Repeat("x", size)}, {Role: llm.RoleUser, Content: strings.Repeat("pressure", 200)}}
				probe := compactor.WouldReduceBytes(history, 1)
				result, err := compactor.Compact(t.Context(), history, 1)
				if err != nil {
					t.Fatal(err)
				}
				if result.BytesTrimmed != probe || probe != retainedHistoryBytes(history)-retainedHistoryBytes(result.History) || probe < 0 {
					t.Fatalf("probe=%d, reported=%d, actual=%d", probe, result.BytesTrimmed, retainedHistoryBytes(history)-retainedHistoryBytes(result.History))
				}
				if size > 0 && size <= 350 && !reflect.DeepEqual(result.History, history) {
					t.Fatal("host framing made an unprofitable excerpt")
				}
			})
		}
	}
}
