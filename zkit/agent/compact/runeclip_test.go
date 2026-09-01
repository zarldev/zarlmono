package compact_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestStructural_TruncationPreservesUTF8(t *testing.T) {
	t.Parallel()

	// Structural keeps at least 64 bytes of an assistant message. Put the first
	// byte of a multi-byte rune at that boundary to exercise truncation through
	// the public compactor behavior.
	content := strings.Repeat("a", 63) + "é" + strings.Repeat("z", 200)
	c := compact.NewStructural()
	c.AssistantTrimAt = 100
	result, err := c.Compact(t.Context(), []llm.Message{
		{Role: llm.RoleAssistant, Content: content},
		{Role: llm.RoleUser, Content: "keep recent"},
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	got := result.History[0].Content
	if !utf8.ValidString(got) {
		t.Fatalf("compacted content %q is not valid UTF-8", got)
	}
	if !strings.HasPrefix(got, strings.Repeat("a", 63)) {
		t.Fatalf("compacted content lost preserved prefix: %q", got)
	}
}
