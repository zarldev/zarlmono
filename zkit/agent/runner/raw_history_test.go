package runner_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

type rawHistorySink struct {
	runner.NopSink
	starts   []runner.ToolStarted
	failures []runner.ToolFailed
}

func (s *rawHistorySink) OnToolStarted(ctx context.Context, e runner.ToolStarted) {
	s.starts = append(s.starts, e)
}

func (s *rawHistorySink) OnToolFailed(ctx context.Context, e runner.ToolFailed) {
	s.failures = append(s.failures, e)
}

func TestRawToolHistoryPreservesArguments(t *testing.T) {
	for _, tc := range []struct {
		name, args string
		deny       bool
	}{
		{"success", "{ \"auth_token\":\"CANARY-token\", \"env\":{\"KEY\":\"CANARY-env\"}}", false},
		{"denied", `{"command":"CANARY-command"}`, true},
		{"malformed", `{"auth_token": CANARY-broken`, false},
		{"repaired", "{\"command\":\"CANARY\ncommand\"}", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := &rawHistorySink{}
			outputs := &recordingToolOutputSink{}
			calls := 0
			tool := tools.New(tools.ToolSpec{Name: "capture", Description: "capture"}, func(_ context.Context, parameters map[string]any) (string, error) {
				calls++
				if tc.name == "repaired" {
					parameters["command"] = "producer-mutated"
				}
				return "CANARY-output", nil
			})
			client := runnertest.NewClient([][]llm.CompletionChunk{{runnertest.ChunkToolCall("raw-call", "capture", tc.args)}, {runnertest.ChunkText("done")}})
			r := runner.New(client, runner.WithTools(tools.NewRegistry(tool)), runner.WithSink(events), runner.WithToolOutputSink(outputs))
			ctx := t.Context()
			if tc.deny {
				ctx = runner.WithToolGate(ctx, func(tools.ToolSpec) bool { return false })
			}
			_ = r.Run(ctx, runner.TaskSpec{Prompt: "capture", MaxIterations: 2})
			if len(events.starts) != 1 || events.starts[0].RawArguments != tc.args {
				t.Fatalf("original arguments missing from events: %+v", events.starts)
			}
			if len(outputs.records) != 1 || outputs.records[0].Args != tc.args {
				t.Fatalf("original arguments missing from output history: %+v", outputs.records)
			}
			record := outputs.records[0]
			switch tc.name {
			case "success", "repaired":
				if !record.Success || record.Kind != tools.Kinds.UNKNOWN || record.Error != "" {
					t.Fatalf("success classification: %+v", record)
				}
			case "denied", "malformed":
				if record.Success || record.Kind != tools.Kinds.VALIDATION || record.Error == "" {
					t.Fatalf("failure classification: %+v", record)
				}
			}
			if tc.name == "repaired" && record.Parameters.String("command", "") != "CANARY\ncommand" {
				t.Fatalf("decoded repaired parameters: %+v", record.Parameters)
			}
			if tc.name == "malformed" && record.Parameters != nil {
				t.Fatalf("malformed call gained canonical parameters: %+v", record.Parameters)
			}
			if tc.deny && calls != 0 {
				t.Fatal("denied call executed")
			}
		})
	}
}

type interruptedHistoryClient struct {
	args  string
	cause error
}

func (c interruptedHistoryClient) Complete(context.Context, llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		if !yield(runnertest.ChunkToolCall("partial", "capture", c.args), nil) {
			return
		}
		yield(llm.CompletionChunk{}, c.cause)
	}
}

func TestRawHistoryPreservesUndispatchedAttempts(t *testing.T) {
	for _, cause := range []error{errors.New("stream interrupted"), context.Canceled} {
		t.Run(cause.Error(), func(t *testing.T) {
			args := `{"auth_token":"CANARY-partial`
			events := &rawHistorySink{}
			outputs := &recordingToolOutputSink{}
			calls := 0
			tool := tools.New(tools.ToolSpec{Name: "capture", Description: "capture"}, func(context.Context, map[string]any) (string, error) { calls++; return "", nil })
			r := runner.New(interruptedHistoryClient{args: args, cause: cause}, runner.WithTools(tools.NewRegistry(tool)), runner.WithSink(events), runner.WithToolOutputSink(outputs))
			result := r.Run(t.Context(), runner.TaskSpec{Prompt: "capture", MaxIterations: 1})
			if result.Err == nil || calls != 0 {
				t.Fatalf("result=%v executed=%d", result.Err, calls)
			}
			if len(events.starts) != 1 || events.starts[0].RawArguments != args {
				t.Fatalf("starts: %+v", events.starts)
			}
			if len(outputs.records) != 1 || outputs.records[0].Args != args {
				t.Fatalf("history: %+v", outputs.records)
			}
		})
	}
}

func TestNestedHistoryUsesTerminalClassification(t *testing.T) {
	outputs := &recordingToolOutputSink{}
	events := &rawHistorySink{}
	client := runnertest.NewClient([][]llm.CompletionChunk{
		{runnertest.ChunkToolCall("parent", "nested_classifier", `{}`)},
		{runnertest.ChunkText("done")},
	})
	r := runner.New(client, runner.WithTools(tools.NewRegistry(nestedClassificationTool{})), runner.WithSink(events), runner.WithToolOutputSink(outputs))
	if result := r.Run(t.Context(), runner.TaskSpec{Prompt: "classify", MaxIterations: 2}); result.Err != nil {
		t.Fatal(result.Err)
	}
	if len(outputs.records) != 5 {
		t.Fatalf("history length = %d, want 5", len(outputs.records))
	}
	checks := []struct {
		id, errorText string
		kind          tools.Kind
	}{
		{id: "nested-err", errorText: "CANARY-denied", kind: tools.Kinds.PERMISSION},
		{id: "nested-error", errorText: "CANARY-explicit", kind: tools.Kinds.BUDGET},
		{id: "nested-result", errorText: "CANARY-invalid", kind: tools.Kinds.VALIDATION},
		{id: "nested-cancel", errorText: "CANARY-cancelled", kind: tools.Kinds.TRANSIENT},
	}
	for i, check := range checks {
		got := outputs.records[i]
		if got.ToolCallID != check.id || got.Success || got.Kind != check.kind || !strings.Contains(got.Error, check.errorText) {
			t.Fatalf("nested classification %d: %+v", i, got)
		}
	}
	if len(events.failures) != 4 {
		t.Fatalf("failure events = %d, want 4", len(events.failures))
	}
	cancelled := events.failures[3]
	if cancelled.Kind != tools.Kinds.TRANSIENT || !errors.Is(cancelled.Err, context.Canceled) ||
		!strings.Contains(cancelled.Error, "CANARY-cancelled") || cancelled.RawOutput != "" {
		t.Fatalf("nested cancellation event: %+v", cancelled)
	}
	if got := outputs.records[3]; got.Output != "" {
		t.Fatalf("nested cancellation raw output changed: %+v", got)
	}
}

type nestedClassificationTool struct{}

func (nestedClassificationTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{Name: "nested_classifier", Description: "emits nested classifications"}
}

func (nestedClassificationTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	observer := tools.NestedToolObserverFromContext(ctx)
	cases := []tools.NestedToolResult{
		{Err: tools.Permission("child", "CANARY-denied")},
		{Result: tools.Success("nested-error", "CANARY-raw"), Error: "CANARY-explicit", Kind: tools.Kinds.BUDGET},
		{Result: tools.Failure("nested-result", tools.Validation("child", "CANARY-invalid"))},
		{
			Result: tools.Failure("nested-cancel", tools.Validation("child", "CANARY-late-validation")),
			Err:    tools.Transient("child", fmt.Errorf("CANARY-cancelled: %w", context.Canceled)),
			Kind:   tools.Kinds.TRANSIENT,
		},
	}
	ids := []tools.ToolCallID{"nested-err", "nested-error", "nested-result", "nested-cancel"}
	for i := range cases {
		nestedCall := tools.NestedToolCall{
			ParentID: call.ID,
			ChildID:  ids[i],
			Sequence: i,
			Call:     tools.ToolCall{ID: ids[i], ToolName: "child", RawArguments: `{"canary":true}`},
		}
		cases[i].NestedToolCall = nestedCall
		observer.OnNestedToolStarted(ctx, nestedCall)
		observer.OnNestedToolFinished(ctx, cases[i])
	}
	return tools.Success(call.ID, "done"), nil
}
