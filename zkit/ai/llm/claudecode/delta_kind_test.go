package claudecode_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestRepeatedAndPrefixShapedDeltasAreNotSnapshots(t *testing.T) {
	stream := strings.Join([]string{
		textEvent("ha"), textEvent("ha"), textEvent("hah"),
		`{"type":"stream_event","event":{"delta":{"type":"thinking_delta","thinking":"think"}}}`,
		`{"type":"stream_event","event":{"delta":{"type":"thinking_delta","thinking":"think"}}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"hahahah"},{"type":"thinking","thinking":"thinkthink"}]}}`,
	}, "\n")
	chunks, _ := complete(t, stream, llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "go"}}})
	text, thinking, _ := collect(chunks)
	if text != "hahahah" || thinking != "thinkthink" {
		t.Fatalf("incremental bytes lost or snapshot duplicated: text=%q thinking=%q", text, thinking)
	}
}
