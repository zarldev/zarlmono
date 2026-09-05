package llm_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestContinuationItemsCloneAndJSONRoundTrip(t *testing.T) {
	t.Parallel()

	payload := []byte(`{"type":"reasoning","encrypted":"opaque"}`)
	message := llm.Message{
		Role: llm.RoleAssistant,
		ContinuationItems: []llm.ContinuationItem{{
			OutputIndex: llm.OutputPosition(2),
			Provider:    "example",
			Format:      "response-item-v1",
			Kind:        "reasoning",
			ID:          "item-1",
			Data:        payload,
		}},
	}

	clone := message.Clone()
	payload[0] = 'X'
	message.ContinuationItems[0].Data[1] = 'X'
	if got := string(clone.ContinuationItems[0].Data); got != `{"type":"reasoning","encrypted":"opaque"}` {
		t.Fatalf("clone data = %q", got)
	}

	encoded, err := json.Marshal(clone)
	if err != nil {
		t.Fatal(err)
	}
	var decoded llm.Message
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, clone) {
		t.Fatalf("round trip = %#v, want %#v", decoded, clone)
	}

	legacy := []byte(`{"role":"assistant","content":"visible","reasoning_content":"displayable"}`)
	decoded = llm.Message{}
	if err := json.Unmarshal(legacy, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Content != "visible" || decoded.ReasoningContent != "displayable" || decoded.ContinuationItems != nil {
		t.Fatalf("legacy decode = %#v", decoded)
	}
}

func TestCompletionChunkCloneOwnsCompletedItems(t *testing.T) {
	t.Parallel()

	data := []byte("opaque")
	chunk := llm.CompletionChunk{CompletedItems: []llm.ContinuationItem{{Provider: "p", Format: "f", Data: data}}}
	clone := chunk.Clone()
	data[0] = 'X'
	chunk.CompletedItems[0].Data[1] = 'X'
	if got := string(clone.CompletedItems[0].Data); got != "opaque" {
		t.Fatalf("clone data = %q", got)
	}
}
