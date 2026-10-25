package claudecode_test

import (
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestNativeSchemaCannotSucceedWithoutStructuredResult(t *testing.T) {
	for _, terminal := range []string{`{"type":"result","subtype":"success"}`, `{"type":"result","structured_output":null}`, `{"type":"result","is_error":true,"subtype":"error_max_turns"}`} {
		provider, _ := newFixtureProvider(t, textEvent("unconstrained answer")+"\n"+terminal)
		request := llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "go"}}, ResponseFormat: llm.ResponseFormat{Type: llm.ResponseFormatJSONSchema, Schema: llm.Schema{Type: "object"}}}
		failures, successfulTerminals := 0, 0
		for chunk, err := range provider.Complete(t.Context(), request) {
			if err != nil {
				failures++
				if !reflect.DeepEqual(chunk, llm.CompletionChunk{}) {
					t.Fatal("terminal error has nonzero chunk")
				}
				continue
			}
			if chunk.FinishReason != llm.FinishReasons.UNKNOWN {
				successfulTerminals++
			}
			if chunk.Content != "" {
				t.Fatal("schema route emitted unconstrained fallback text")
			}
		}
		if failures != 1 || successfulTerminals != 0 {
			t.Fatalf("missing structured answer was accepted: failures=%d successes=%d", failures, successfulTerminals)
		}
	}
}

func TestCLIResultFailureCannotSucceedWithZeroProcessExit(t *testing.T) {
	provider, _ := newFixtureProvider(t, textEvent("partial")+"\n"+`{"type":"result","subtype":"error_during_execution","is_error":true}`)
	failures := 0
	for chunk, err := range provider.Complete(t.Context(), llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "go"}}}) {
		if err != nil {
			failures++
			if !reflect.DeepEqual(chunk, llm.CompletionChunk{}) {
				t.Fatal("error chunk is not zero")
			}
			continue
		}
		if chunk.FinishReason != llm.FinishReasons.UNKNOWN {
			t.Fatal("CLI failure reported successful finish")
		}
	}
	if failures != 1 {
		t.Fatalf("error count=%d", failures)
	}
}
