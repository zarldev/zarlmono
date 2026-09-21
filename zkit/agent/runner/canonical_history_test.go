package runner_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

type canonicalSink struct {
	mu        sync.Mutex
	records   []runner.ReplayMessage
	requests  [][]byte
	onRequest func(context.Context) error
}

func (s *canonicalSink) Append(_ context.Context, records []runner.ReplayMessage) error {
	owned := make([]runner.ReplayMessage, len(records))
	for i, record := range records {
		data, err := runner.MarshalReplayMessage(record)
		if err != nil {
			return err
		}
		owned[i], err = runner.UnmarshalReplayMessage(data)
		if err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, owned...)
	return nil
}
func (s *canonicalSink) Request(ctx context.Context, req llm.CompletionRequest) error {
	data, err := runner.MarshalHistoryRequest(req)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.requests = append(s.requests, data)
	s.mu.Unlock()
	if s.onRequest != nil {
		return s.onRequest(ctx)
	}
	return nil
}

type captureClient struct {
	client runner.Client
	sink   *canonicalSink
	t      *testing.T
}

func (c captureClient) Complete(ctx context.Context, req llm.CompletionRequest) llm.CompletionStream {
	data, err := runner.MarshalHistoryRequest(req)
	if err != nil {
		c.t.Fatal(err)
	}
	c.sink.mu.Lock()
	if len(c.sink.requests) == 0 || !bytes.Equal(data, c.sink.requests[len(c.sink.requests)-1]) {
		c.t.Error("captured request differs from actual Complete input")
	}
	c.sink.mu.Unlock()
	return c.client.Complete(ctx, req)
}

type canonicalTrim struct{}

func (canonicalTrim) Truncate(string, string) string { return "TRUNCATED" }

type canonicalCompact struct{ calls int }

func (c *canonicalCompact) Compact(_ context.Context, messages []llm.Message, _ int) (compact.Result, error) {
	c.calls++
	for i := range messages {
		if messages[i].Role != llm.RoleSystem {
			messages[i].Content = "COMPACTED"
		}
	}
	return compact.Result{History: messages, BytesTrimmed: 1}, nil
}

func TestCanonicalHistoryFidelityAndCompaction(t *testing.T) {
	const args = "{ \"x\":  1 }\n"
	output := strings.Repeat("full result\n", 3000)
	registry := tools.NewRegistry()
	registry.Register(runnertest.Tool{Name: "echo", Result: output})
	native := []byte("{ \"type\":\"reasoning\",\"encrypted_content\":\"native\" }\n")
	chunks := [][]llm.CompletionChunk{
		{runnertest.ChunkToolCall("duplicate", "echo", args)},
		{runnertest.ChunkToolCall("duplicate", "echo", args)},
		{{Content: "  final\n", Thinking: "raw thought", CompletedItems: []llm.ContinuationItem{{Provider: "openai-codex", Format: "responses.reasoning.v1", Kind: "reasoning", Data: native}}}},
	}
	sink := &canonicalSink{}
	compactor := &canonicalCompact{}
	client := runnertest.NewClient(chunks)
	r := runner.New(captureClient{client: client, sink: sink, t: t}, runner.WithHistorySink(sink), runner.WithTools(registry),
		runner.WithResultTruncator(canonicalTrim{}), runner.WithCompactor(compactor), runner.WithCompactKeepRecent(1),
		runner.WithMaxIterations(3), runner.WithFinalizeWarn(runner.FinalizeWarn{RemainingThreshold: 3, Message: "REQUEST-ONLY-NUDGE"}))
	parts := []llm.ContentPart{llm.ImagePartFromDataURI("data:image/png;base64,eA==", "image/png"), llm.TextPart("attachment bytes")}
	result := r.Run(t.Context(), runner.TaskSpec{Prompt: "original prompt", Attachments: parts})
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if compactor.calls == 0 {
		t.Fatal("compaction did not run")
	}
	if len(sink.records) != 6 {
		t.Fatalf("occurrences = %d, want 6", len(sink.records))
	}
	parts[0].Image.DataURI = "mutated"
	native[0] = '!'
	if sink.records[0].Message.Content != "original prompt" || len(sink.records[0].Message.Parts) != 3 || sink.records[0].Message.Parts[1].Image == nil || sink.records[0].Message.Parts[1].Image.DataURI != "data:image/png;base64,eA==" {
		t.Fatal("canonical input mutated or compacted")
	}
	for _, i := range []int{1, 3} {
		if sink.records[i].RawToolCalls[0].Function.Arguments != args {
			t.Fatal("raw arguments normalized")
		}
		out := sink.records[i+1].Tool
		if out == nil || out.Output != output || sink.records[i+1].Message.Content != output {
			t.Fatal("canonical tool result truncated")
		}
	}
	if sink.records[2].Tool.ExecutionID == sink.records[4].Tool.ExecutionID {
		t.Fatal("duplicate provider IDs merged executions")
	}
	last := sink.records[5].Message
	if last.Content != "  final\n" || last.ReasoningContent != "raw thought" || last.ContinuationItems[0].Data[0] != '{' {
		t.Fatal("native response bytes changed")
	}
	for _, record := range sink.records {
		if strings.Contains(record.Message.Content, "REQUEST-ONLY-NUDGE") || record.Message.Content == "COMPACTED" {
			t.Fatal("prepared-only context contaminated history")
		}
	}
	if !bytes.Contains(sink.requests[0], []byte("REQUEST-ONLY-NUDGE")) {
		t.Fatal("prepared request missed nudge")
	}
}

func TestCanonicalHistoryExcludesConcurrentRecursiveRuns(t *testing.T) {
	sink := &canonicalSink{}
	client := runnertest.NewClient([][]llm.CompletionChunk{{runnertest.ChunkText("answer")}, {runnertest.ChunkText("child")}, {runnertest.ChunkText("child")}})
	r := runner.New(client, runner.WithHistorySink(sink))
	var wg sync.WaitGroup
	for _, depth := range []int{0, 1, 2} {
		wg.Go(func() {
			result := r.Run(t.Context(), runner.TaskSpec{Prompt: "input", Depth: depth})
			if result.Err != nil {
				t.Error(result.Err)
			}
		})
	}
	wg.Wait()
	if len(sink.records) != 2 || len(sink.requests) != 1 {
		t.Fatalf("recursive contamination: %d occurrences, %d requests", len(sink.records), len(sink.requests))
	}
}

type interruptedCanonicalClient struct{ cancel context.CancelFunc }

func (c interruptedCanonicalClient) Complete(_ context.Context, _ llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		if !yield(llm.CompletionChunk{Content: " partial\n", CompletedItems: []llm.ContinuationItem{{Provider: "openai-codex", Format: "responses.reasoning.v1", Kind: "reasoning", Data: []byte(`{"native":true}`)}}}, nil) {
			return
		}
		if !yield(runnertest.ChunkToolCall("partial", "never_run", "{ \"partial\":"), nil) {
			return
		}
		c.cancel()
		yield(llm.CompletionChunk{}, context.Canceled)
	}
}
func TestCanonicalHistoryInterruptedObservations(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sink := &canonicalSink{}
	r := runner.New(interruptedCanonicalClient{cancel: cancel}, runner.WithHistorySink(sink))
	result := r.Run(ctx, runner.TaskSpec{Prompt: "input"})
	if result.Reason != runner.TerminalCancelled || !errors.Is(result.Err, context.Canceled) {
		t.Fatalf("cancellation changed: %+v", result)
	}
	if len(sink.records) != 3 {
		t.Fatalf("lost interrupted occurrences: %d", len(sink.records))
	}
	partial := sink.records[1]
	if !partial.Interrupted || partial.Message.Content != " partial\n" || len(partial.Message.ContinuationItems) != 1 || partial.RawToolCalls[0].Function.Arguments != "{ \"partial\":" {
		t.Fatal("partial provider data lost")
	}
	out := sink.records[2]
	if !out.Interrupted || out.Tool == nil || out.Tool.Success || out.Tool.Error == "" || out.Tool.ExecutionID == "" {
		t.Fatal("undispatched failure missing")
	}
	if len(result.Messages) != 1 {
		t.Fatal("interrupted attempt changed working retry context")
	}
}

func TestCanonicalRequestCancellationPreservesReason(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sink := &canonicalSink{onRequest: func(context.Context) error { cancel(); return context.Canceled }}
	client := runnertest.NewClient(nil)
	result := runner.New(client, runner.WithHistorySink(sink)).Run(ctx, runner.TaskSpec{Prompt: "input"})
	if result.Reason != runner.TerminalCancelled || !errors.Is(result.Err, runner.ErrReplayHistory) || !errors.Is(result.Err, context.Canceled) || client.CallCount() != 0 {
		t.Fatalf("capture cancellation: %+v", result)
	}
}
