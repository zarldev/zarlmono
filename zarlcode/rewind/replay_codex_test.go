package rewind_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestCodexNativeTextValidation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mutate func(*llm.Message)
		err    error
	}{
		{"matching", func(*llm.Message) {}, nil},
		{"native only", func(m *llm.Message) { m.Content = ""; m.ContentOutputIndex = nil }, nil},
		{"contradictory text", func(m *llm.Message) { m.Content = "lost" }, rewind.ErrInvalid},
		{"contradictory index", func(m *llm.Message) { m.ContentOutputIndex = llm.OutputPosition(1) }, rewind.ErrInvalid},
		{"missing native index", func(m *llm.Message) { m.ContinuationItems[0].OutputIndex = nil }, rewind.ErrInvalid},
		{"duplicate index", func(m *llm.Message) { m.ContinuationItems = append(m.ContinuationItems, m.ContinuationItems[0]) }, rewind.ErrInvalid},
		{"foreign provider", func(m *llm.Message) { m.ContinuationItems[0].Provider = "openai" }, rewind.ErrInvalid},
		{"unknown format", func(m *llm.Message) { m.ContinuationItems[0].Format = "future" }, rewind.ErrInvalid},
		{"wrong kind", func(m *llm.Message) { m.ContinuationItems[0].Kind = "reasoning" }, rewind.ErrInvalid},
		{"wrong identity", func(m *llm.Message) { m.ContinuationItems[0].ID = "other" }, rewind.ErrInvalid},
		{"wrong role", func(m *llm.Message) {
			m.ContinuationItems[0].Data = []byte(`{"type":"message","id":"msg","role":"user","content":[{"type":"output_text","text":"answer"}]}`)
		}, rewind.ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			message := llm.Message{Role: llm.RoleAssistant, Content: "answer", ContentOutputIndex: llm.OutputPosition(3), ContinuationItems: []llm.ContinuationItem{{
				Provider: "openai-codex", Format: "responses.reasoning.v1", Kind: "message", ID: "msg", OutputIndex: llm.OutputPosition(3),
				Data: []byte(`{"type":"message","id":"msg","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"answer"}]}`),
			}}}
			tc.mutate(&message)
			messages := []llm.Message{message}
			encoded, err := rewind.EncodeResume(1, messages, rewind.Target{Provider: "openai-codex", Model: "gpt-5.6"}, "turn", 1, rewind.InitialContinuation{})
			if !errors.Is(err, tc.err) {
				t.Fatalf("EncodeResume=%v, want %v", err, tc.err)
			}
			if err != nil {
				return
			}
			decoded, err := rewind.DecodeResume(encoded)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(decoded.Context, messages) {
				t.Fatal("native text changed during save/resume")
			}
		})
	}
}

func TestCodexReasoningParagraphsAcrossItems(t *testing.T) {
	t.Parallel()
	message := llm.Message{Role: llm.RoleAssistant, ReasoningContent: "First\n\nSecond\n\nThird", ContinuationItems: []llm.ContinuationItem{
		{Provider: "openai-codex", Format: "responses.reasoning.v1", Kind: "reasoning", OutputIndex: llm.OutputPosition(2), Data: []byte(`{"type":"reasoning","id":"rs_2","encrypted_content":"opaque","summary":[{"text":"Third"}]}`)},
		{Provider: "openai-codex", Format: "responses.reasoning.v1", Kind: "reasoning", OutputIndex: llm.OutputPosition(0), Data: []byte(`{"type":"reasoning","id":"rs_1","encrypted_content":"opaque","summary":[{"text":"First"},{"text":"Second"}]}`)},
	}}
	if err := rewind.ValidateContext([]llm.Message{message}, "openai-codex"); err != nil {
		t.Fatal(err)
	}
	message.ReasoningContent = "Raw reasoning differs from the summary"
	encoded, err := rewind.EncodeResume(1, []llm.Message{message}, rewind.Target{Provider: "openai-codex", Model: "gpt-5.6"}, "turn", 1, rewind.InitialContinuation{})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := rewind.DecodeResume(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded.Context, []llm.Message{message}) {
		t.Fatal("save/resume changed independent display reasoning or native items")
	}
}
