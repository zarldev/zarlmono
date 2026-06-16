package openai

import (
	"encoding/json"
	"testing"

	"github.com/openai/openai-go/v2"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestDecodeReasoningField(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, raw, want string
	}{
		{"empty", "", ""},
		{"null literal", "null", ""},
		{"plain string", `"hello"`, "hello"},
		{"whitespace string", `" thinking process: "`, " thinking process: "},
		{"escape sequences", `"line1\nline2"`, "line1\nline2"},
		{"unicode", `"🤔"`, "🤔"},
		{"malformed", `{not json`, ""},
		{"non-string", `42`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := decodeReasoningField(c.raw); got != c.want {
				t.Errorf("decodeReasoningField(%q) = %q, want %q", c.raw, got, c.want)
			}
		})
	}
}

// Regression: extractReasoningDelta only knew the reasoning_content
// field. Non-API GPT-5/5.5 endpoints (aggregators, reverse proxies)
// emit reasoning under .reasoning instead — without this fix it
// landed verbatim in the visible content channel and the user saw
// "I need to explain why..." leak into the assistant body.
func TestProbeReasoningFromRaw(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, raw, want string
	}{
		{"empty input", "", ""},
		{"none of the candidates", `{"content":"hello"}`, ""},
		{"reasoning_content (llama.cpp / vLLM)", `{"reasoning_content":"thinking..."}`, "thinking..."},
		{"reasoning (aggregators)", `{"reasoning":"meta-narrative"}`, "meta-narrative"},
		{"reasoning_summary (some proxies)", `{"reasoning_summary":"summary"}`, "summary"},
		{"thinking", `{"thinking":"thought block"}`, "thought block"},
		{"thought", `{"thought":"single thought"}`, "single thought"},
		{"empty string still falls through", `{"reasoning":""}`, ""},
		{"malformed json", `not json`, ""},
		{"unicode preserved", `{"reasoning":"🤔 thinking"}`, "🤔 thinking"},
		{"escape sequences preserved", `{"reasoning":"line1\nline2"}`, "line1\nline2"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := probeReasoningFromRaw(c.raw); got != c.want {
				t.Errorf("probeReasoningFromRaw(%q) = %q, want %q", c.raw, got, c.want)
			}
		})
	}
}

// assistantMessageParam is the per-message reasoning serializer. The
// runner stores assistant turns with clean Content + separate
// ReasoningContent; these cases pin how each mode reshapes that pair
// for the wire: Inline re-wraps reasoning into `<think>…</think>`,
// Field forwards it via reasoning_content, Strip drops it.
func TestAssistantMessageParam_Modes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		msg           llm.Message
		mode          llm.ReasoningHistory
		wantContent   string
		wantReasoning string // "" means the field must be absent
	}{
		{
			name:          "inline rewraps reasoning into think tags",
			msg:           llm.Message{Role: "assistant", Content: "visible", ReasoningContent: "hidden"},
			mode:          llm.ReasoningHistories.INLINE,
			wantContent:   "<think>hidden</think>visible",
			wantReasoning: "",
		},
		{
			name:          "inline passes content through when no reasoning",
			msg:           llm.Message{Role: "assistant", Content: "visible"},
			mode:          llm.ReasoningHistories.INLINE,
			wantContent:   "visible",
			wantReasoning: "",
		},
		{
			name:          "field forwards reasoning_content extra field",
			msg:           llm.Message{Role: "assistant", Content: "visible", ReasoningContent: "hidden chain"},
			mode:          llm.ReasoningHistories.FIELD,
			wantContent:   "visible",
			wantReasoning: "hidden chain",
		},
		{
			name:          "strip drops reasoning entirely",
			msg:           llm.Message{Role: "assistant", Content: "visible", ReasoningContent: "hidden chain"},
			mode:          llm.ReasoningHistories.STRIP,
			wantContent:   "visible",
			wantReasoning: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := marshalAssistant(t, c.msg, c.mode)
			if got["content"] != c.wantContent {
				t.Errorf("content = %#v, want %q; full=%v", got["content"], c.wantContent, got)
			}
			if c.wantReasoning == "" {
				if _, ok := got["reasoning_content"]; ok {
					t.Errorf("reasoning_content present (%#v), want absent; full=%v", got["reasoning_content"], got)
				}
				return
			}
			if got["reasoning_content"] != c.wantReasoning {
				t.Errorf("reasoning_content = %#v, want %q; full=%v", got["reasoning_content"], c.wantReasoning, got)
			}
		})
	}
}

// Regression: DeepSeek's API rejects assistant history where neither
// content nor tool_calls is present ("Invalid assistant message:
// content or tool_calls must be set"). A pure-thinking turn (empty
// Content + non-empty ReasoningContent) must still serialize an
// explicit empty content field in Field and Strip modes.
func TestAssistantMessageParam_PureThinkingHasEmptyContent(t *testing.T) {
	t.Parallel()
	for _, mode := range []llm.ReasoningHistory{llm.ReasoningHistories.FIELD, llm.ReasoningHistories.STRIP} {
		got := marshalAssistant(t, llm.Message{
			Role:             "assistant",
			Content:          "",
			ReasoningContent: "only thinking, no answer",
		}, mode)
		if c, ok := got["content"]; !ok || c != "" {
			t.Errorf("mode %d: content = %#v (present=%v), want empty string present; full=%v", mode, got["content"], ok, got)
		}
	}
}

func TestProviderConvertMessagesToOpenAI_InlinesReasoningByDefault(t *testing.T) {
	t.Parallel()
	p := &Provider{} // zero value → llm.ReasoningHistories.INLINE
	messages := p.convertMessagesToOpenAI([]llm.Message{{
		Role:             "assistant",
		Content:          "visible",
		ReasoningContent: "hidden",
	}})
	got := mustMarshalMessage(t, messages[0])
	if got["content"] != "<think>hidden</think>visible" {
		t.Fatalf("content = %#v, want reasoning re-inlined; full=%v", got["content"], got)
	}
	if _, ok := got["reasoning_content"]; ok {
		t.Fatalf("reasoning_content should be omitted in Inline mode; full=%v", got)
	}
}

// In Field mode without a keep-mask hook, every assistant message
// keeps its reasoning_content. The DeepSeek-V4 trim is opt-in via
// [WithReasoningKeepMask] — see the deepseek package for the mask
// implementation and its end-to-end test.
func TestConvertMessages_FieldKeepsAllReasoningByDefault(t *testing.T) {
	t.Parallel()
	msgs := []llm.Message{
		{Role: "user", Content: "do a thing"},
		{Role: "assistant", Content: "first answer", ReasoningContent: "plan A"},
		{Role: "user", Content: "another"},
		{Role: "assistant", Content: "second answer", ReasoningContent: "plan B"},
	}
	out := convertMessagesToOpenAIWithReasoning(msgs, llm.ReasoningHistories.FIELD, nil)
	for i, idx := range []int{1, 3} {
		got := mustMarshalMessage(t, out[idx])
		want := []string{"plan A", "plan B"}[i]
		if got["reasoning_content"] != want {
			t.Errorf("msg[%d] reasoning_content = %#v, want %q", idx, got["reasoning_content"], want)
		}
	}
}

// WithReasoningKeepMask lets a wrapping provider downgrade selected
// Field-mode messages to Strip semantics. The mask is consulted only
// in Field mode — Inline and Strip ignore it.
func TestConvertMessages_FieldKeepMaskHookDrives(t *testing.T) {
	t.Parallel()
	msgs := []llm.Message{
		{Role: "user", Content: "q1"},
		{Role: "assistant", Content: "a1", ReasoningContent: "keep"},
		{Role: "user", Content: "q2"},
		{Role: "assistant", Content: "a2", ReasoningContent: "drop"},
	}
	// Mask: keep index 1, drop index 3.
	mask := func(in []llm.Message) []bool {
		if len(in) != len(msgs) {
			t.Fatalf("mask received %d msgs, want %d", len(in), len(msgs))
		}
		return []bool{false, true, false, false}
	}

	out := convertMessagesToOpenAIWithReasoning(msgs, llm.ReasoningHistories.FIELD, mask)
	kept := mustMarshalMessage(t, out[1])
	if kept["reasoning_content"] != "keep" {
		t.Errorf("kept msg reasoning_content = %#v, want %q", kept["reasoning_content"], "keep")
	}
	dropped := mustMarshalMessage(t, out[3])
	if _, ok := dropped["reasoning_content"]; ok {
		t.Errorf("dropped msg reasoning_content present (%#v), want absent", dropped["reasoning_content"])
	}
}

func marshalAssistant(t *testing.T, msg llm.Message, mode llm.ReasoningHistory) map[string]any {
	t.Helper()
	a := assistantMessageParam(msg, mode)
	return mustMarshalMessage(t, openai.ChatCompletionMessageParamUnion{OfAssistant: &a})
}

func mustMarshalMessage(t *testing.T, msg any) map[string]any {
	t.Helper()
	b, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal message: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal message %s: %v", string(b), err)
	}
	return out
}
