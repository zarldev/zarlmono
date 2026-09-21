package rewind_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestNativeTextHistoricalTrimmedProjection(t *testing.T) {
	t.Parallel()
	for _, provider := range []string{"anthropic", "openai", "openai-codex"} {
		t.Run(provider, func(t *testing.T) {
			t.Parallel()
			for _, tc := range []struct {
				name, native, projection string
				err                      error
			}{
				{name: "exact", native: "\n answer\n", projection: "\n answer\n"},
				{name: "historical trim", native: "\n answer\n", projection: "answer"},
				{name: "historical whitespace only", native: "\t\n ", projection: ""},
				{name: "changed text", native: "\n answer\n", projection: "different", err: rewind.ErrInvalid},
				{name: "changed internal whitespace", native: "\n first\nsecond\n", projection: "first second", err: rewind.ErrInvalid},
				{name: "added whitespace", native: "answer", projection: " answer ", err: rewind.ErrInvalid},
			} {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()
					item := llm.ContinuationItem{
						Provider: provider, Format: "responses.output_item", Kind: "message", ID: "msg", OutputIndex: llm.OutputPosition(3),
						Data: []byte(fmt.Sprintf(`{"type":"message","id":"msg","role":"assistant","content":[{"type":"output_text","text":%q}]}`, tc.native)),
					}
					switch provider {
					case "anthropic":
						item.Format, item.Kind, item.ID = "content_block.v1", "text", ""
						item.Data = []byte(fmt.Sprintf(`{"type":"text","text":%q}`, tc.native))
					case "openai-codex":
						item.Format = "responses.reasoning.v1"
					}
					messages := []llm.Message{{Role: llm.RoleAssistant, Content: tc.projection, ContentOutputIndex: llm.OutputPosition(3), ContinuationItems: []llm.ContinuationItem{item}}}
					encoded, err := rewind.EncodeResume(1, messages, rewind.Target{Provider: provider, Model: "gpt-5.6"}, "turn", 1, rewind.InitialContinuation{})
					if !errors.Is(err, tc.err) {
						t.Fatalf("EncodeResume = %v, want %v", err, tc.err)
					}
					if err != nil {
						return
					}
					decoded, err := rewind.DecodeResume(encoded)
					if err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(decoded.Context, messages) {
						t.Fatal("save/resume changed native bytes or historical projection")
					}
				})
			}
		})
	}
}
