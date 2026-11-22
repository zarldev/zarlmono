package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

type numericHistoryClient struct {
	raw      string
	requests [][]llm.Message
}

func (c *numericHistoryClient) Complete(_ context.Context, req llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		c.requests = append(c.requests, llm.CloneMessages(req.Messages))
		if len(c.requests) == 1 {
			yield(runnertest.ChunkToolCall("numeric-call", "exact", c.raw), nil)
			return
		}
		yield(runnertest.ChunkText("done"), nil)
	}
}

type numericCaptureTool struct {
	inner tools.Tool
	calls []tools.ToolCall
}

func (c *numericCaptureTool) Definition() tools.ToolSpec { return c.inner.Definition() }
func (c *numericCaptureTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	captured := call
	captured.Arguments = tools.CloneParameters(call.Arguments)
	c.calls = append(c.calls, captured)
	return c.inner.Execute(ctx, call)
}

func TestStructuredToolArgumentsExactExecutionAndHistory(t *testing.T) {
	t.Parallel()
	for _, number := range []string{"9007199254740992", "9007199254740993", "9223372036854775807"} {
		t.Run(number, func(t *testing.T) {
			t.Parallel()
			type nested struct {
				Huge     json.Number `json:"huge"`
				Fraction json.Number `json:"fraction"`
			}
			type args struct {
				N      int64    `json:"n"`
				Nested []nested `json:"nested"`
			}
			raw := " \n{ \"n\":1, \"n\":" + number + ", \"nested\":[{\"huge\":1e9999999999999999999999999999,\"fraction\":0.12345678901234567890123456789}]} \t"
			want, err := json.Number(number).Int64()
			if err != nil {
				t.Fatal(err)
			}
			var executed int64
			tool := &numericCaptureTool{inner: tools.New(tools.ToolSpec{Name: "exact", Parameters: tools.SchemaFor[args]()}, func(_ context.Context, a args) (string, error) {
				executed = a.N
				if a.Nested[0].Huge != "1e9999999999999999999999999999" || a.Nested[0].Fraction != "0.12345678901234567890123456789" {
					t.Errorf("typed exact leaves=%+v", a)
				}
				return "executed:" + number, nil
			})}
			client := &numericHistoryClient{raw: raw}
			events := &rawHistorySink{}
			outputs := &recordingToolOutputSink{}
			r := runner.New(client, runner.WithTools(tools.NewRegistry(tool)), runner.WithSink(events), runner.WithToolOutputSink(outputs), runner.WithMaxIterations(3))
			result := r.Run(t.Context(), runner.TaskSpec{Prompt: "start"})
			if result.Err != nil || result.FinalContent != "done" || len(client.requests) != 2 || executed != want || len(tool.calls) != 1 {
				t.Fatalf("result=%+v requests=%d executed=%d calls=%d", result, len(client.requests), executed, len(tool.calls))
			}
			if got := client.requests[1][1].ToolCalls[0].Function.Arguments; got != raw {
				t.Errorf("next request arguments changed: %q want %q", got, raw)
			}
			if result.Messages[1].ToolCalls[0].Function.Arguments != raw || tool.calls[0].RawArguments != raw || len(events.starts) != 1 || events.starts[0].RawArguments != raw || len(outputs.records) != 1 || outputs.records[0].Args != raw {
				t.Fatal("original raw arguments lost from history/audit/events")
			}
			call := tool.calls[0]
			for _, params := range []tools.ToolParameters{call.Arguments, tools.ToolParameters(events.starts[0].Parameters), outputs.records[0].Parameters} {
				if params["n"] != json.Number(number) {
					t.Errorf("exact dispatch/event/audit n=%#v", params["n"])
				}
				leaf := params["nested"].([]any)[0].(map[string]any)
				if leaf["huge"] != json.Number("1e9999999999999999999999999999") || leaf["fraction"] != json.Number("0.12345678901234567890123456789") {
					t.Errorf("recursive Number leaves=%#v", leaf)
				}
				encoded, err := json.Marshal(params)
				if err != nil || !strings.Contains(string(encoded), `"n":`+number) || !strings.Contains(string(encoded), `"huge":1e9999999999999999999999999999`) {
					t.Errorf("map re-encode=%s err=%v", encoded, err)
				}
			}
			adjacent := call
			adjacent.Arguments = tools.CloneParameters(call.Arguments)
			adjacent.Arguments["n"] = json.Number("9007199254740994")
			if tools.CallSignature(call) == tools.CallSignature(adjacent) {
				t.Fatal("dispatch signature lost precision")
			}
		})
	}
}

func TestStructuredToolArgumentRepairAndWholeDocument(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, raw, history string
		execute            bool
		want               int64
	}{
		{"malformed-closer", `{"n":9007199254740993`, `{"n":9007199254740993}`, true, 9007199254740993},
		{"malformed-comma", `{"n":9007199254740993,}`, `{"n":9007199254740993}`, true, 9007199254740993},
		{"empty", "", "", true, 0}, {"whitespace", " \t\n", "{}", true, 0},
		{"unrecoverable", "not JSON", "not JSON", false, 0},
		// Existing Repair extracts the first flat object from trailing prose.
		// Decoder.Decode must not simply accept arbitrary trailing bytes.
		{"trailing-object-cascade", `{"n":9007199254740993} prose`, `{"n":9007199254740993}`, true, 9007199254740993},
		{"trailing-array-no-extraction", `[1] prose`, `[1] prose`, false, 0},
		{"trailing-scalar-no-extraction", `1 prose`, `1 prose`, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			type args struct {
				N int64 `json:"n"`
			}
			var value int64
			tool := &numericCaptureTool{inner: tools.New(tools.ToolSpec{Name: "exact"}, func(_ context.Context, a args) (int64, error) { value = a.N; return value, nil })}
			client := &numericHistoryClient{raw: tt.raw}
			events := &rawHistorySink{}
			outputs := &recordingToolOutputSink{}
			r := runner.New(client, runner.WithTools(tools.NewRegistry(tool)), runner.WithSink(events), runner.WithToolOutputSink(outputs), runner.WithMaxIterations(3))
			result := r.Run(t.Context(), runner.TaskSpec{Prompt: "start"})
			if result.Err != nil || len(client.requests) != 2 || (len(tool.calls) == 1) != tt.execute || value != tt.want {
				t.Fatalf("result=%+v requests=%d calls=%d n=%d", result, len(client.requests), len(tool.calls), value)
			}
			if got := client.requests[1][1].ToolCalls[0].Function.Arguments; got != tt.history {
				t.Errorf("history=%q want %q", got, tt.history)
			}
			if len(events.starts) != 1 || events.starts[0].RawArguments != tt.raw || len(outputs.records) != 1 || outputs.records[0].Args != tt.raw {
				t.Fatal("repair changed audit input")
			}
			if !tt.execute && (outputs.records[0].Kind != tools.Kinds.VALIDATION || !strings.Contains(result.Messages[2].Content, "did not parse as JSON")) {
				t.Fatalf("failure classification=%+v", outputs.records[0])
			}
		})
	}
}

// drain's existing identity boundary rejects multiple complete JSON values
// before repair/dispatch. RNP1 must not weaken this earlier fail-closed check.
func TestStructuredToolArgumentsMultipleDocumentsRemainAmbiguous(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{`{"n":9007199254740993} {"n":17}`, `[1] [2]`, `1 2`} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			client := &numericHistoryClient{raw: raw}
			tool := &numericCaptureTool{inner: runnertest.Tool{Name: "exact"}}
			r := runner.New(client, runner.WithTools(tools.NewRegistry(tool)), runner.WithMaxIterations(3))
			result := r.Run(t.Context(), runner.TaskSpec{Prompt: "start"})
			if !errors.Is(result.Err, runner.ErrAmbiguousToolCalls) || len(tool.calls) != 0 || len(client.requests) != 1 {
				t.Fatalf("result=%+v calls=%d requests=%d", result, len(tool.calls), len(client.requests))
			}
		})
	}
}
