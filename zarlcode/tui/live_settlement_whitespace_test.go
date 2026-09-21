package tui_test

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestLiveTurnNativeTextWhitespaceSettlement(t *testing.T) {
	for _, provider := range []string{"anthropic", "openai-codex"} {
		t.Run(provider, func(t *testing.T) {
			f := newBeforeFixture(t)
			const text = "\n  answer\n\n"
			item := llm.ContinuationItem{
				Provider: provider, Format: "responses.output_item", Kind: "message", ID: "msg_1", OutputIndex: llm.OutputPosition(0),
				Data: []byte(fmt.Sprintf(`{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":%q}]}`, text)),
			}
			switch provider {
			case "anthropic":
				item.Format, item.Kind, item.ID = "content_block.v1", "text", ""
				item.Data = []byte(fmt.Sprintf(`{"type":"text","text":%q}`, text))
			case "openai-codex":
				item.Format = "responses.reasoning.v1"
			}
			f.live.SetProviderSpec(nativeSettlementProvider{name: provider, chunk: llm.CompletionChunk{
				Content: text, ContentOutputIndex: llm.OutputPosition(0), CompletedItems: []llm.ContinuationItem{item},
			}}, engine.ProviderSpec{Name: provider, Model: "gpt-5.6"})
			for _, prompt := range []string{"first", "second", "third"} {
				settleRewindTurn(t, f, prompt)
				stored, err := f.store.GetSessionResumeState(t.Context(), f.ui.SessionIdentity())
				if err != nil {
					t.Fatal(err)
				}
				resume, err := rewind.DecodeResume(stored.Session.ContextJSON)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(resume.Context, f.live.ContextSnapshot()) {
					t.Fatalf("native text turn not saved: %s", f.ui.ToastText())
				}
				if got := resume.Context[len(resume.Context)-1].Content; got != text {
					t.Fatalf("history text = %q, want %q", got, text)
				}
			}
		})
	}
}
