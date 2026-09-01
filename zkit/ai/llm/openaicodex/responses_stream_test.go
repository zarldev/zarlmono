package openaicodex_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openaicodex"
)

// collectChunks exercises the public Provider against a fixed SSE response.
func collectChunks(t *testing.T, payload string) ([]llm.CompletionChunk, error) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, payload)
	}))
	t.Cleanup(srv.Close)
	provider, err := openaicodex.NewProvider(
		openaicodex.StaticTokenSource{T: openaicodex.Token{Access: "access", AccountID: "account", Expires: time.Now().Add(time.Hour)}},
		openaicodex.WithBaseURL(srv.URL),
		openaicodex.WithNoRetry(),
	)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	var out []llm.CompletionChunk
	var yieldedErr error
	for chunk, streamErr := range provider.Complete(t.Context(), llm.CompletionRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "test"}},
	}) {
		if streamErr != nil {
			yieldedErr = streamErr
			continue
		}
		out = append(out, chunk)
	}
	return out, yieldedErr
}

func TestParseSSEStream_TextOnly(t *testing.T) {
	t.Parallel()
	// A minimal three-event response: two text deltas then completed.
	stream := strings.Join([]string{
		`event: response.output_text.delta`,
		`data: {"type":"response.output_text.delta","delta":"Hello "}`,
		``,
		`data: {"type":"response.output_text.delta","delta":"world"}`,
		``,
		`data: {"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12,"input_tokens_details":{"cached_tokens":4}}}}`,
		``,
	}, "\n")
	chunks, err := collectChunks(t, stream)
	if err != nil {
		t.Fatalf("parseSSEStream: %v", err)
	}
	if len(chunks) != 3 {
		t.Fatalf("got %d chunks, want 3: %#v", len(chunks), chunks)
	}
	if chunks[0].Content != "Hello " {
		t.Errorf("chunk0 = %q, want %q", chunks[0].Content, "Hello ")
	}
	if chunks[1].Content != "world" {
		t.Errorf("chunk1 = %q, want %q", chunks[1].Content, "world")
	}
	last := chunks[2]
	if last.FinishReason != llm.FinishReasons.STOP {
		t.Errorf("last finish reason = %q, want stop", last.FinishReason)
	}
	if !last.UsageReported || last.Usage.PromptTokens != 10 || last.Usage.CachedTokens != 4 {
		t.Errorf("usage = %+v reported=%v, want prompt=10 cached=4", last.Usage, last.UsageReported)
	}
}

func TestParseSSEStream_ReasoningRoutesToThinkingChannel(t *testing.T) {
	t.Parallel()
	// Reasoning summary deltas land on the out-of-band Thinking
	// channel; visible text stays on Content. The two channels must
	// stay disjoint — same bytes appearing on both would double in
	// the TUI's thinking pane.
	stream := strings.Join([]string{
		`data: {"type":"response.reasoning_summary_text.delta","delta":"Considering the question"}`,
		``,
		`data: {"type":"response.reasoning_summary_text.delta","delta":" carefully."}`,
		``,
		`data: {"type":"response.output_text.delta","delta":"42"}`,
		``,
		`data: {"type":"response.completed","response":{"usage":{}}}`,
		``,
	}, "\n")
	chunks, err := collectChunks(t, stream)
	if err != nil {
		t.Fatalf("parseSSEStream: %v", err)
	}
	var contents, thinking []string
	for _, c := range chunks {
		if c.Content != "" {
			contents = append(contents, c.Content)
		}
		if c.Thinking != "" {
			thinking = append(thinking, c.Thinking)
		}
	}
	wantThinking := []string{"Considering the question", " carefully."}
	if !reflect.DeepEqual(thinking, wantThinking) {
		t.Errorf("thinking = %v, want %v", thinking, wantThinking)
	}
	wantContent := []string{"42"}
	if !reflect.DeepEqual(contents, wantContent) {
		t.Errorf("content = %v, want %v", contents, wantContent)
	}
}

func TestParseSSEStream_ReasoningSummaryPartsSeparated(t *testing.T) {
	t.Parallel()
	// A multi-part summary: each part is bracketed by a *.part.added /
	// *.text.done pair and its deltas carry no leading separator. The
	// parser must inject a paragraph break at each part boundary so the
	// concatenated thinking reads "part one.\n\npart two." rather than
	// running together as "part one.part two."
	stream := strings.Join([]string{
		`data: {"type":"response.reasoning_summary_part.added","summary_index":0}`,
		``,
		`data: {"type":"response.reasoning_summary_text.delta","delta":"Checked CI."}`,
		``,
		`data: {"type":"response.reasoning_summary_text.done"}`,
		``,
		`data: {"type":"response.reasoning_summary_part.added","summary_index":1}`,
		``,
		`data: {"type":"response.reasoning_summary_text.delta","delta":"Now pushing the fix."}`,
		``,
		`data: {"type":"response.completed","response":{"usage":{}}}`,
		``,
	}, "\n")
	chunks, err := collectChunks(t, stream)
	if err != nil {
		t.Fatalf("parseSSEStream: %v", err)
	}
	var thinking strings.Builder
	for _, c := range chunks {
		thinking.WriteString(c.Thinking)
	}
	const want = "Checked CI.\n\nNow pushing the fix."
	if got := thinking.String(); got != want {
		t.Errorf("thinking = %q, want %q", got, want)
	}
}

func TestParseSSEStream_ToolCall(t *testing.T) {
	t.Parallel()
	stream := strings.Join([]string{
		`data: {"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_xyz","name":"search","arguments":""}}`,
		``,
		`data: {"type":"response.function_call_arguments.delta","output_index":1,"item_id":"fc_1","delta":"{\"q\":\""}`,
		``,
		`data: {"type":"response.function_call_arguments.delta","output_index":1,"item_id":"fc_1","delta":"foo\"}"}`,
		``,
		`data: {"type":"response.completed","response":{"usage":{}}}`,
		``,
	}, "\n")
	chunks, err := collectChunks(t, stream)
	if err != nil {
		t.Fatalf("parseSSEStream: %v", err)
	}
	var toolEvents []llm.ToolCall
	for _, c := range chunks {
		toolEvents = append(toolEvents, c.ToolCalls...)
	}
	if len(toolEvents) < 3 {
		t.Fatalf("expected at least 3 tool-call chunks (name + 2 arg deltas), got %d: %#v", len(toolEvents), toolEvents)
	}
	if toolEvents[0].Function.Name != "search" {
		t.Errorf("first event name = %q, want search", toolEvents[0].Function.Name)
	}
	if toolEvents[0].ID != "call_xyz" {
		t.Errorf("first event id = %q, want call_xyz", toolEvents[0].ID)
	}
	// All events should share the same ID so the runner can accumulate.
	for i, e := range toolEvents {
		if e.ID != "call_xyz" {
			t.Errorf("event %d id = %q, want call_xyz", i, e.ID)
		}
	}
	// Argument fragments concatenated should reconstruct the JSON.
	var args strings.Builder
	for _, e := range toolEvents {
		args.WriteString(e.Function.Arguments)
	}
	if args.String() != `{"q":"foo"}` {
		t.Errorf("reconstructed args = %q, want %q", args.String(), `{"q":"foo"}`)
	}
	last := chunks[len(chunks)-1]
	if last.FinishReason != llm.FinishReasons.TOOLCALLS {
		t.Errorf("finish reason = %q, want tool_calls", last.FinishReason)
	}
}

func TestParseSSEStream_ToolCallArgDelivery(t *testing.T) {
	t.Parallel()
	const completed = `data: {"type":"response.completed","response":{"usage":{}}}`
	added := func(args string) string {
		return `data: {"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_xyz","name":"search","arguments":` + jsonStr(args) + `}}`
	}
	delta := func(d string) string {
		return `data: {"type":"response.function_call_arguments.delta","output_index":1,"item_id":"fc_1","delta":` + jsonStr(d) + `}`
	}
	done := func(args string) string {
		return `data: {"type":"response.function_call_arguments.done","output_index":1,"item_id":"fc_1","arguments":` + jsonStr(args) + `}`
	}

	cases := []struct {
		name   string
		events []string
	}{
		// The regression: arguments arrive ONLY on the done event (no deltas).
		// Before the fix the call dispatched with empty args.
		{"done only", []string{added(""), done(`{"q":"foo"}`)}},
		// Arguments delivered complete on the added event, no deltas/done.
		{"added carries full args", []string{added(`{"q":"foo"}`)}},
		// Normal streaming, with a redundant done — must NOT double-count.
		{"deltas then redundant done", []string{added(""), delta(`{"q":"`), delta(`foo"}`), done(`{"q":"foo"}`)}},
		// Partial deltas, done supplies the remainder.
		{"partial deltas completed by done", []string{added(""), delta(`{"q":"`), done(`{"q":"foo"}`)}},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			stream := strings.Join(append(interleaveBlankLines(tt.events), completed, ""), "\n")
			chunks, err := collectChunks(t, stream)
			if err != nil {
				t.Fatalf("parseSSEStream: %v", err)
			}
			var name string
			var args strings.Builder
			for _, c := range chunks {
				for _, e := range c.ToolCalls {
					if e.ID != "call_xyz" {
						t.Errorf("tool-call id = %q, want call_xyz", e.ID)
					}
					if e.Function.Name != "" {
						name = e.Function.Name
					}
					args.WriteString(e.Function.Arguments)
				}
			}
			if name != "search" {
				t.Errorf("tool-call name = %q, want search", name)
			}
			if args.String() != `{"q":"foo"}` {
				t.Errorf("reconstructed args = %q, want %q", args.String(), `{"q":"foo"}`)
			}
		})
	}
}

// jsonStr renders s as a JSON string literal (with quotes/escapes) for
// embedding in a test SSE payload.
func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// interleaveBlankLines puts an empty line after each event so the SSE parser
// sees event boundaries.
func interleaveBlankLines(events []string) []string {
	out := make([]string, 0, len(events)*2)
	for _, e := range events {
		out = append(out, e, "")
	}
	return out
}

func TestParseSSEStream_Failure(t *testing.T) {
	t.Parallel()
	stream := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"partial"}`,
		``,
		`data: {"type":"response.failed","response":{"error":{"message":"rate limit","code":"rate_limited"}}}`,
		``,
	}, "\n")
	chunks, err := collectChunks(t, stream)
	if err == nil {
		t.Fatal("expected terminal stream error")
	}
	if len(chunks) != 1 || chunks[0].Content != "partial" {
		t.Fatalf("successful chunks = %#v, want partial content only", chunks)
	}
	if !strings.Contains(err.Error(), "rate limit") {
		t.Errorf("error %q should mention rate limit", err)
	}
}

func TestParseSSEStream_IncompleteIsTruncatedNotError(t *testing.T) {
	t.Parallel()
	// response.incomplete is a normal terminal status (the model stopped
	// before emitting a final turn), not a failure: it must yield a truncated
	// "length" metadata observation carrying partial usage and no error. Regression for
	// the "codex response response.incomplete" terminal failure on gpt-5.x-codex.
	stream := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"partial answer"}`,
		``,
		`data: {"type":"response.incomplete","response":{"status":"incomplete","usage":{"input_tokens":20,"output_tokens":4,"total_tokens":24,"input_tokens_details":{"cached_tokens":8}}}}`,
		``,
	}, "\n")
	chunks, err := collectChunks(t, stream)
	if err != nil {
		t.Fatalf("parseSSEStream: %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("expected text + finish metadata, got %d chunks", len(chunks))
	}
	last := chunks[len(chunks)-1]
	if last.FinishReason != llm.FinishReasons.LENGTH {
		t.Errorf("finish reason = %q, want length", last.FinishReason)
	}
	if !last.UsageReported || last.Usage.PromptTokens != 20 || last.Usage.CachedTokens != 8 {
		t.Errorf("usage = %+v reported=%v, want prompt=20 cached=8", last.Usage, last.UsageReported)
	}
}

func TestParseSSEStream_FailedRateLimitIsRetryable(t *testing.T) {
	t.Parallel()
	// A response.failed with code=rate_limited is a transient, recoverable
	// condition: it must surface as a typed *llm.RateLimitError (which the
	// runner's retry ladder errors.As on) rather than a plain terminal error.
	stream := strings.Join([]string{
		`data: {"type":"response.failed","response":{"error":{"message":"rate limit","code":"rate_limited"}}}`,
		``,
	}, "\n")
	chunks, err := collectChunks(t, stream)
	if err == nil {
		t.Fatal("expected a terminal stream error")
	}
	if len(chunks) != 0 {
		t.Fatalf("successful chunks = %#v, want none", chunks)
	}
	if !llm.IsRateLimitError(err) {
		t.Fatalf("error = %T %v, want *llm.RateLimitError", err, err)
	}
}

func TestParseSSEStream_TruncatedStream(t *testing.T) {
	t.Parallel()
	// No completed event — parser should still emit synthetic length metadata.
	stream := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"hi"}`,
		``,
	}, "\n")
	chunks, err := collectChunks(t, stream)
	if err != nil {
		t.Fatalf("parseSSEStream: %v", err)
	}
	if len(chunks) != 2 {
		t.Fatalf("expected text + length metadata, got %d", len(chunks))
	}
	if chunks[len(chunks)-1].FinishReason != llm.FinishReasons.LENGTH {
		t.Errorf("last finish reason = %q, want length", chunks[len(chunks)-1].FinishReason)
	}
}
