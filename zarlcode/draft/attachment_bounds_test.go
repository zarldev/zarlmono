package draft_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/draft"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestDurableAttachmentBounds(t *testing.T) {
	for _, parts := range [][]llm.ContentPart{
		make([]llm.ContentPart, draft.MaxAttachments+1),
		{llm.TextPart(strings.Repeat("\\", draft.MaxAttachmentBytes/2))},
	} {
		if err := draft.ValidateAttachments(parts); !errors.Is(err, draft.ErrTooLarge) {
			t.Fatalf("attachment bound: %v", err)
		}
		if _, err := draft.EncodeWithAttachments("prompt", parts); !errors.Is(err, draft.ErrTooLarge) {
			t.Fatalf("encoded oversized attachments: %v", err)
		}
	}
}
