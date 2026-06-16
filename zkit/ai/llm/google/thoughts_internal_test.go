package google

import (
	"strings"
	"testing"

	"google.golang.org/genai"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// chunkCollector accumulates the chunks emitPart yields, joining the
// Content and Thinking streams. yield is the func threaded into emitPart;
// it always returns true (these tests never exercise early-break).
type chunkCollector struct {
	cb, tb strings.Builder
}

func (c *chunkCollector) yield(chunk llm.CompletionChunk, _ error) bool {
	c.cb.WriteString(chunk.Content)
	c.tb.WriteString(chunk.Thinking)
	return true
}

func (c *chunkCollector) strings() (string, string) {
	return c.cb.String(), c.tb.String()
}

func TestEmitPart_ThoughtRoutesToThinkingChannel(t *testing.T) {
	t.Parallel()
	var cc chunkCollector
	st := streamAttempt{calls: map[string]llm.ToolCall{}}

	emitPart(&genai.Part{Text: "let me reason", Thought: true}, &st, cc.yield)

	content, thinking := cc.strings()
	if thinking != "let me reason" {
		t.Errorf("thinking = %q, want %q", thinking, "let me reason")
	}
	if content != "" {
		t.Errorf("thought part leaked into Content: %q", content)
	}
}

func TestEmitPart_AnswerRoutesToContentChannel(t *testing.T) {
	t.Parallel()
	var cc chunkCollector
	st := streamAttempt{calls: map[string]llm.ToolCall{}}

	emitPart(&genai.Part{Text: "the answer is 42", Thought: false}, &st, cc.yield)

	content, thinking := cc.strings()
	if content != "the answer is 42" {
		t.Errorf("content = %q, want %q", content, "the answer is 42")
	}
	if thinking != "" {
		t.Errorf("answer leaked into Thinking: %q", thinking)
	}
}

func TestEmitPart_ChannelsStayDisjointAcrossMixedParts(t *testing.T) {
	t.Parallel()
	var cc chunkCollector
	st := streamAttempt{calls: map[string]llm.ToolCall{}}

	emitPart(&genai.Part{Text: "step 1, ", Thought: true}, &st, cc.yield)
	emitPart(&genai.Part{Text: "step 2", Thought: true}, &st, cc.yield)
	emitPart(&genai.Part{Text: "the answer is 42", Thought: false}, &st, cc.yield)

	content, thinking := cc.strings()
	if thinking != "step 1, step 2" {
		t.Errorf("thinking = %q, want %q", thinking, "step 1, step 2")
	}
	if content != "the answer is 42" {
		t.Errorf("content = %q, want %q", content, "the answer is 42")
	}
}

func TestEmitPart_ToolCallAccumulates(t *testing.T) {
	t.Parallel()
	var cc chunkCollector
	st := streamAttempt{calls: map[string]llm.ToolCall{}}

	emitPart(&genai.Part{Text: "i need to read foo.go", Thought: true}, &st, cc.yield)
	emitPart(&genai.Part{
		FunctionCall: &genai.FunctionCall{
			ID:   "call_1",
			Name: "read",
			Args: map[string]any{"path": "foo.go"},
		},
	}, &st, cc.yield)

	_, thinking := cc.strings()
	if thinking != "i need to read foo.go" {
		t.Errorf("thinking = %q, want %q", thinking, "i need to read foo.go")
	}
	if _, ok := st.calls["call_1"]; !ok {
		t.Fatal("pendingCalls missing the tool call entry")
	}
	if got, want := st.calls["call_1"].Function.Name, "read"; got != want {
		t.Errorf("tool name = %q, want %q", got, want)
	}
	if len(st.order) != 1 || st.order[0] != "call_1" {
		t.Errorf("pendingOrder = %v, want [call_1]", st.order)
	}
}

func TestEmitPart_PlainTextNeverSetsThinking(t *testing.T) {
	t.Parallel()
	var cc chunkCollector
	st := streamAttempt{calls: map[string]llm.ToolCall{}}

	emitPart(&genai.Part{Text: "hello world", Thought: false}, &st, cc.yield)
	content, thinking := cc.strings()
	if thinking != "" {
		t.Errorf("non-thought part set Thinking: %q", thinking)
	}
	if content != "hello world" {
		t.Errorf("plain text mangled: %q", content)
	}
}

func TestBuildConfig_EnablesIncludeThoughts(t *testing.T) {
	t.Parallel()
	p := &Provider{}
	cfg := p.buildConfig(llm.CompletionRequest{Temperature: 0.4})
	if cfg.ThinkingConfig == nil {
		t.Fatal("buildConfig left ThinkingConfig nil — Gemini won't emit thoughts")
	}
	if !cfg.ThinkingConfig.IncludeThoughts {
		t.Error("IncludeThoughts must be true so Gemini streams reasoning back")
	}
}
