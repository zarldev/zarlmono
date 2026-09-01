package llm_test

import (
	"errors"
	"reflect"
	"testing"
	"unsafe"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestCompletionStreamIsDirectlyRangeable(t *testing.T) {
	t.Parallel()

	terminalErr := errors.New("terminal")
	stream := llm.CompletionStream(func(yield func(llm.CompletionChunk, error) bool) {
		if !yield(llm.CompletionChunk{Content: "first"}, nil) {
			return
		}
		yield(llm.CompletionChunk{}, terminalErr)
	})

	var chunks []llm.CompletionChunk
	var errs []error
	for chunk, err := range stream {
		chunks = append(chunks, chunk)
		errs = append(errs, err)
	}

	if got := len(chunks); got != 2 {
		t.Fatalf("yield count = %d, want 2", got)
	}
	if chunks[0].Content != "first" || !reflect.ValueOf(chunks[1]).IsZero() {
		t.Fatalf("chunks = %#v, want content then zero terminal chunk", chunks)
	}
	if errs[0] != nil || !errors.Is(errs[1], terminalErr) {
		t.Fatalf("errors = %v, want nil then terminal error", errs)
	}
}

func TestCompletionStreamMetadataDoesNotSignalCompletion(t *testing.T) {
	t.Parallel()

	stream := llm.CompletionStream(func(yield func(llm.CompletionChunk, error) bool) {
		if !yield(llm.CompletionChunk{
			FinishReason:  llm.FinishReasons.STOP,
			Usage:         llm.Usage{},
			UsageReported: true,
		}, nil) {
			return
		}
		yield(llm.CompletionChunk{Content: "after metadata"}, nil)
	})

	var chunks []llm.CompletionChunk
	for chunk, err := range stream {
		if err != nil {
			t.Fatalf("unexpected stream error: %v", err)
		}
		chunks = append(chunks, chunk)
	}

	if len(chunks) != 2 {
		t.Fatalf("yield count = %d, want 2", len(chunks))
	}
	if chunks[0].FinishReason != llm.FinishReasons.STOP || !chunks[0].UsageReported {
		t.Fatalf("metadata chunk = %#v", chunks[0])
	}
	if chunks[1].Content != "after metadata" {
		t.Fatalf("chunk after metadata = %#v", chunks[1])
	}
}

func TestCompletionStreamWithOrdersMiddlewareAndPropagatesFalseSynchronously(t *testing.T) {
	t.Parallel()

	var trace []string
	base := llm.CompletionStream(func(yield func(llm.CompletionChunk, error) bool) {
		trace = append(trace, "enter base")
		if !yield(llm.CompletionChunk{Content: "first"}, nil) {
			trace = append(trace, "base saw false")
			trace = append(trace, "exit base")
			return
		}
		yield(llm.CompletionChunk{Content: "must not be yielded"}, nil)
		trace = append(trace, "exit base")
	})

	stream := base.With(
		tracingMiddleware{name: "A", trace: &trace},
		tracingMiddleware{name: "B", trace: &trace},
	)

	for range stream {
		trace = append(trace, "consumer")
		break
	}

	want := []string{
		"enter A", "enter B", "enter base",
		"event B", "event A", "consumer",
		"result A false", "result B false", "base saw false",
		"exit base", "exit B", "exit A",
	}
	if !reflect.DeepEqual(trace, want) {
		t.Fatalf("trace = %q, want %q", trace, want)
	}
}

func TestCompletionChunkCloneOwnsReferenceBackedFields(t *testing.T) {
	t.Parallel()

	content := []byte("content")
	thinking := []byte("thinking")
	id := []byte("call-id")
	typ := []byte("function")
	name := []byte("tool-name")
	arguments := []byte(`{"key":"value"}`)
	chunk := llm.CompletionChunk{
		Content:  unsafe.String(unsafe.SliceData(content), len(content)),
		Thinking: unsafe.String(unsafe.SliceData(thinking), len(thinking)),
		ToolCalls: []llm.ToolCall{{
			ID:   unsafe.String(unsafe.SliceData(id), len(id)),
			Type: unsafe.String(unsafe.SliceData(typ), len(typ)),
			Function: llm.ToolCallFunction{
				Name:      unsafe.String(unsafe.SliceData(name), len(name)),
				Arguments: unsafe.String(unsafe.SliceData(arguments), len(arguments)),
			},
		}},
		FinishReason:  llm.FinishReasons.TOOLCALLS,
		Usage:         llm.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3},
		UsageReported: true,
	}

	clone := chunk.Clone()
	content[0], thinking[0], id[0], typ[0], name[0], arguments[0] = 'X', 'X', 'X', 'X', 'X', 'X'
	chunk.ToolCalls[0] = llm.ToolCall{}

	if clone.Content != "content" || clone.Thinking != "thinking" {
		t.Fatalf("cloned text = %q/%q, want content/thinking", clone.Content, clone.Thinking)
	}
	wantCall := llm.ToolCall{
		ID:   "call-id",
		Type: "function",
		Function: llm.ToolCallFunction{
			Name:      "tool-name",
			Arguments: `{"key":"value"}`,
		},
	}
	if !reflect.DeepEqual(clone.ToolCalls, []llm.ToolCall{wantCall}) {
		t.Fatalf("cloned tool calls = %#v, want %#v", clone.ToolCalls, []llm.ToolCall{wantCall})
	}
	if clone.FinishReason != llm.FinishReasons.TOOLCALLS || !clone.UsageReported || clone.Usage.TotalTokens != 3 {
		t.Fatalf("cloned metadata = %#v", clone)
	}
}

func TestFinishReasonZeroValueIsUnknown(t *testing.T) {
	t.Parallel()

	var zero llm.FinishReason
	if zero != llm.FinishReasons.UNKNOWN || zero.String() != "unknown" {
		t.Fatalf("zero FinishReason = %v, want semantic unknown", zero)
	}
}

type tracingMiddleware struct {
	name  string
	trace *[]string
}

func (m tracingMiddleware) Wrap(upstream llm.CompletionStream) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		*m.trace = append(*m.trace, "enter "+m.name)
		upstream(func(chunk llm.CompletionChunk, err error) bool {
			*m.trace = append(*m.trace, "event "+m.name)
			accepted := yield(chunk, err)
			if accepted {
				*m.trace = append(*m.trace, "result "+m.name+" true")
			} else {
				*m.trace = append(*m.trace, "result "+m.name+" false")
			}
			return accepted
		})
		*m.trace = append(*m.trace, "exit "+m.name)
	}
}
