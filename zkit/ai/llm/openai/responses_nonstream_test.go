package openai_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
)

const nonstreamReasoning = `{"type":"reasoning","id":"rs_1","status":"completed","encrypted_content":"opaque","summary":[{"type":"summary_text","text":"think"}]}`
const nonstreamText = `{"type":"message","id":"msg_1","role":"assistant","status":"completed","phase":"commentary","content":[{"type":"output_text","text":"before tools","annotations":[]}]}`
const nonstreamTools = `{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read","status":"completed","arguments":"{\"path\":\"a\"}"},{"type":"function_call","id":"fc_2","call_id":"call_2","name":"read","status":"completed","arguments":"{\"path\":\"b\"}"}`

func TestResponsesNonstreamToolsAndNativeFollowup(t *testing.T) {
	t.Parallel()
	requests := make(chan map[string]any, 2)
	var requestNumber atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" || r.Header.Get("Accept") != "application/json" {
			t.Errorf("request route/Accept = %s %q", r.URL.Path, r.Header.Get("Accept"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		requests <- body
		w.Header().Set("Content-Type", "application/json")
		if requestNumber.Add(1) == 2 {
			_, _ = io.WriteString(w, `{"id":"resp_2","status":"completed","output":[{"type":"message","id":"msg_2","role":"assistant","status":"completed","content":[{"type":"output_text","text":"done"}]}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"resp_1","status":"completed","output":[`+nonstreamReasoning+`,`+nonstreamText+`,`+nonstreamTools+`],"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15,"input_tokens_details":{"cached_tokens":4},"output_tokens_details":{"reasoning_tokens":0}}}`)
	}))
	t.Cleanup(server.Close)
	const model = " Company/Production Alias-V7 "
	provider := openai.NewProvider("test", openai.WithBaseURL(server.URL), openai.WithModel(model), openai.WithResponsesAPI(true))
	req := llm.CompletionRequest{
		Messages:       []llm.Message{{Role: llm.RoleUser, Content: "read both"}},
		Tools:          []llm.Tool{{Type: "function", Function: llm.ToolFunction{Name: "read", Parameters: llm.SchemaFromMap(map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}})}}},
		MaxTokens:      42,
		Options:        llm.ModelOptions{"reasoning_effort": "high", "text_verbosity": "low", "prompt_cache_key": "session", "prompt_cache_retention": "in_memory", "service_tier": "priority", "previous_response_id": "ignored", "pro_mode": true},
		ResponseFormat: llm.ResponseFormat{Type: llm.ResponseFormatJSONSchema, Name: "answer", Strict: true, Schema: llm.SchemaFromMap(map[string]any{"type": "object"})},
	}
	before := nonstreamRequestSnapshot(t, req)
	var content, thinking strings.Builder
	var items []llm.ContinuationItem
	var calls []llm.ToolCall
	var final llm.CompletionChunk
	for chunk, err := range provider.Complete(t.Context(), req) {
		if err != nil {
			t.Fatal(err)
		}
		chunk = chunk.Clone()
		content.WriteString(chunk.Content)
		thinking.WriteString(chunk.Thinking)
		items = append(items, chunk.CompletedItems...)
		calls = append(calls, chunk.ToolCalls...)
		if chunk.Content != "" && (chunk.ContentOutputIndex == nil || *chunk.ContentOutputIndex != 1) {
			t.Fatalf("content index = %v", chunk.ContentOutputIndex)
		}
		final = chunk
	}
	if content.String() != "before tools" || thinking.String() != "think" || len(items) != 2 || len(calls) != 2 {
		t.Fatalf("content=%q thinking=%q items=%+v calls=%+v", content.String(), thinking.String(), items, calls)
	}
	for i, raw := range []string{nonstreamReasoning, nonstreamText} {
		if string(items[i].Data) != raw || items[i].OutputIndex == nil || *items[i].OutputIndex != i {
			t.Fatalf("native item %d: %+v data=%s", i, items[i], items[i].Data)
		}
	}
	for i, call := range calls {
		if call.ID != []string{"call_1", "call_2"}[i] || call.OutputIndex == nil || *call.OutputIndex != i+2 || call.Function.Name != "read" || call.Function.Arguments != []string{`{"path":"a"}`, `{"path":"b"}`}[i] {
			t.Fatalf("tool call %d: %+v", i, call)
		}
	}
	if final.FinishReason != llm.FinishReasons.TOOLCALLS || !final.UsageReported || final.Usage.PromptTokens != 10 || final.Usage.CompletionTokens != 5 || final.Usage.TotalTokens != 15 || final.Usage.CachedTokens != 4 || !final.Usage.ReasoningTokensReported || final.Usage.ReasoningTokens != 0 {
		t.Fatalf("metadata = %+v", final)
	}
	body := <-requests
	assertNonstreamRequestControls(t, model, body)
	if after := nonstreamRequestSnapshot(t, req); before != after {
		t.Fatalf("request mutated: before=%s after=%s", before, after)
	}
	req.Messages = append(req.Messages,
		llm.Message{Role: llm.RoleAssistant, Content: content.String(), ContentOutputIndex: llm.OutputPosition(1), ReasoningContent: thinking.String(), ContinuationItems: items, ToolCalls: calls},
		llm.Message{Role: llm.RoleTool, ToolCallID: "call_1", Content: "a contents"},
		llm.Message{Role: llm.RoleTool, ToolCallID: "call_2", Content: "b contents"},
	)
	req.Options = nil
	before = nonstreamRequestSnapshot(t, req)
	var answer strings.Builder
	for chunk, err := range provider.Complete(t.Context(), req) {
		if err != nil {
			t.Fatal(err)
		}
		answer.WriteString(chunk.Content)
		final = chunk
	}
	if answer.String() != "done" || final.FinishReason != llm.FinishReasons.STOP {
		t.Fatalf("followup completion: answer=%q final=%+v", answer.String(), final)
	}
	followup := <-requests
	input := followup["input"].([]any)
	if len(input) != 7 {
		t.Fatalf("followup input (duplicated projection?) = %#v", input)
	}
	for i, want := range []string{"message", "reasoning", "message", "function_call", "function_call", "function_call_output", "function_call_output"} {
		if input[i].(map[string]any)["type"] != want {
			t.Errorf("input[%d] = %#v, want %s", i, input[i], want)
		}
	}
	if input[1].(map[string]any)["encrypted_content"] != "opaque" || input[2].(map[string]any)["phase"] != "commentary" || input[3].(map[string]any)["call_id"] != "call_1" || input[4].(map[string]any)["call_id"] != "call_2" || input[5].(map[string]any)["output"] != "a contents" || input[6].(map[string]any)["output"] != "b contents" {
		t.Fatalf("followup values = %#v", input)
	}
	if _, present := followup["reasoning"]; present || followup["include"] == nil {
		t.Fatalf("followup should request encrypted items without enabling reasoning: %#v", followup)
	}
	if after := nonstreamRequestSnapshot(t, req); before != after {
		t.Fatalf("followup request mutated: before=%s after=%s", before, after)
	}
}

func assertNonstreamRequestControls(t *testing.T, model string, body map[string]any) {
	t.Helper()
	if body["model"] != model || body["stream"] != false || body["store"] != false || body["max_output_tokens"] != float64(42) || body["parallel_tool_calls"] != true || body["tool_choice"] != "auto" || body["service_tier"] != "priority" || body["prompt_cache_key"] != "session" || body["prompt_cache_retention"] != "in_memory" {
		t.Fatalf("request controls = %#v", body)
	}
	if body["reasoning"].(map[string]any)["effort"] != "high" || body["include"].([]any)[0] != "reasoning.encrypted_content" {
		t.Fatalf("reasoning controls = %#v", body)
	}
	text := body["text"].(map[string]any)
	format := text["format"].(map[string]any)
	if text["verbosity"] != "low" || format["type"] != "json_schema" || format["name"] != "answer" || format["strict"] != true || format["schema"].(map[string]any)["type"] != "object" {
		t.Fatalf("text controls = %#v", text)
	}
	wireTool := body["tools"].([]any)[0].(map[string]any)
	if wireTool["name"] != "read" || wireTool["parameters"].(map[string]any)["properties"].(map[string]any)["path"].(map[string]any)["type"] != "string" {
		t.Fatalf("tool schema = %#v", wireTool)
	}
	for _, key := range []string{"previous_response_id", "pro_mode", "max_tokens", "max_completion_tokens"} {
		if _, present := body[key]; present {
			t.Errorf("unsupported key %s present", key)
		}
	}
}

func nonstreamRequestSnapshot(t *testing.T, req llm.CompletionRequest) string {
	t.Helper()
	// Snapshot borrowed reference-backed fields. Scalar fields are already
	// copied into Complete's request value and cannot be mutated by the callee.
	data, err := json.Marshal(map[string]any{"messages": req.Messages, "tools": req.Tools, "options": req.Options, "schema": req.ResponseFormat.Schema.Map()})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestResponsesNonstreamStatusesAndUsage(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, response   string
		finish           llm.FinishReason
		usage, reasoning bool
	}{
		{"absent usage", `{"status":"completed","output":[]}`, llm.FinishReasons.STOP, false, false},
		{"null usage", `{"status":"completed","output":[],"usage":null}`, llm.FinishReasons.STOP, false, false},
		{"reported zero", `{"status":"completed","output":[],"usage":{}}`, llm.FinishReasons.STOP, true, false},
		{"reported reasoning zero", `{"status":"completed","output":[],"usage":{"output_tokens_details":{"reasoning_tokens":0}}}`, llm.FinishReasons.STOP, true, true},
		{"length", `{"status":"incomplete","output":[],"incomplete_details":{"reason":"max_output_tokens"}}`, llm.FinishReasons.LENGTH, false, false},
		{"filter", `{"status":"incomplete","output":[],"incomplete_details":{"reason":"content_filter"}}`, llm.FinishReasons.CONTENTFILTER, false, false},
		{"other incomplete", `{"status":"incomplete","output":[],"incomplete_details":{"reason":"steered"}}`, llm.FinishReasons.UNKNOWN, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			provider := nonstreamServer(t, tc.response, http.StatusOK)
			count := 0
			for chunk, err := range provider.Complete(t.Context(), llm.CompletionRequest{}) {
				count++
				if err != nil {
					t.Fatal(err)
				}
				if chunk.FinishReason != tc.finish || chunk.UsageReported != tc.usage || chunk.Usage.ReasoningTokensReported != tc.reasoning {
					t.Fatalf("metadata = %+v", chunk)
				}
			}
			if count != 1 {
				t.Fatalf("yields = %d, want 1", count)
			}
		})
	}
}

func TestResponsesNonstreamTerminalErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, response       string
		status               int
		rateLimit, permanent bool
	}{
		{name: "empty", status: http.StatusOK},
		{name: "truncated", response: `{"status":"completed","output":[`, status: http.StatusOK},
		{name: "malformed", response: `not json`, status: http.StatusOK},
		{name: "null", response: `null`, status: http.StatusOK},
		{name: "missing status", response: `{"output":[]}`, status: http.StatusOK},
		{name: "missing output", response: `{"status":"completed"}`, status: http.StatusOK},
		{name: "null output", response: `{"status":"completed","output":null}`, status: http.StatusOK},
		{name: "trailing value", response: `{"status":"completed","output":[]} {}`, status: http.StatusOK},
		{name: "trailing malformed", response: `{"status":"completed","output":[]} x`, status: http.StatusOK},
		{name: "unknown status", response: `{"status":"surprise","output":[]}`, status: http.StatusOK},
		{name: "in progress", response: `{"status":"in_progress","output":[]}`, status: http.StatusOK},
		{name: "queued", response: `{"status":"queued","output":[]}`, status: http.StatusOK},
		{name: "cancelled", response: `{"status":"cancelled","output":[]}`, status: http.StatusOK},
		{name: "incomplete without reason", response: `{"status":"incomplete","output":[]}`, status: http.StatusOK},
		{name: "failed", response: `{"status":"failed","error":{"code":"server_error","message":"broken"}}`, status: http.StatusOK},
		{name: "failed rate limit", response: `{"status":"failed","error":{"code":"rate_limit_exceeded","message":"try again in 2s"}}`, status: http.StatusOK, rateLimit: true},
		{name: "failed quota", response: `{"status":"failed","error":{"code":"insufficient_quota","message":"billing; try again in 2s"}}`, status: http.StatusOK, rateLimit: true, permanent: true},
		{name: "status error", response: `{"error":{"message":"broken"}}`, status: http.StatusBadRequest},
		{name: "HTTP rate limit", response: `{"error":{"message":"rate limit"}}`, status: http.StatusTooManyRequests, rateLimit: true},
		{name: "message missing text", response: `{"status":"completed","output":[{"type":"message","id":"m1","role":"assistant","status":"completed","content":[{"type":"output_text"}]}]}`, status: http.StatusOK},
		{name: "message still in progress", response: `{"status":"completed","output":[{"type":"message","id":"m1","role":"assistant","status":"in_progress","content":[]}]}`, status: http.StatusOK},
		{name: "tool incomplete", response: `{"status":"incomplete","output":[{"type":"function_call","call_id":"call_1","name":"read","status":"incomplete","arguments":"{}"}],"incomplete_details":{"reason":"max_output_tokens"}}`, status: http.StatusOK},
		{name: "unknown item", response: `{"status":"completed","output":[{"type":"unexpected"}]}`, status: http.StatusOK},
		{name: "malformed reasoning", response: `{"status":"completed","output":[{"type":"reasoning"}]}`, status: http.StatusOK},
		{name: "empty item", response: `{"status":"completed","output":[{}]}`, status: http.StatusOK},
		{name: "tool missing ID", response: `{"status":"completed","output":[{"type":"function_call","name":"read","arguments":"{}"}]}`, status: http.StatusOK},
		{name: "tool missing name", response: `{"status":"completed","output":[{"type":"function_call","call_id":"call_1","arguments":"{}"}]}`, status: http.StatusOK},
		{name: "tool bad args", response: `{"status":"completed","output":[{"type":"function_call","call_id":"call_1","name":"read","arguments":"{"}]}`, status: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			provider := nonstreamServer(t, tc.response, tc.status)
			count := 0
			for chunk, err := range provider.Complete(t.Context(), llm.CompletionRequest{}) {
				count++
				if err == nil || chunk.Content != "" || len(chunk.ToolCalls) != 0 || chunk.FinishReason != (llm.FinishReason{}) {
					t.Fatalf("expected terminal error only: chunk=%+v err=%v", chunk, err)
				}
				var rateLimit *llm.RateLimitError
				if errors.As(err, &rateLimit) != tc.rateLimit {
					t.Fatalf("rate limit error = %v", err)
				}
				if rateLimit != nil && (rateLimit.Permanent != tc.permanent || rateLimit.Retryable == tc.permanent || rateLimit.RetryAfter != 2*time.Second) {
					t.Fatalf("rate limit metadata = %+v", rateLimit)
				}
			}
			if count != 1 {
				t.Fatalf("terminal yields = %d, want 1", count)
			}
		})
	}
}

func nonstreamServer(t *testing.T, response string, status int) *openai.Provider {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Errorf("path = %q, want /responses", r.URL.Path)
		}
		w.Header().Set("Retry-After", "2")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, response)
	}))
	t.Cleanup(server.Close)
	var closed atomic.Int64
	t.Cleanup(func() {
		if closed.Load() != 1 {
			t.Errorf("response body closes = %d, want 1", closed.Load())
		}
	})
	client := &http.Client{Transport: optionTransport(func(req *http.Request) (*http.Response, error) {
		resp, err := http.DefaultTransport.RoundTrip(req)
		if err == nil {
			resp.Body = nonstreamCloseBody{ReadCloser: resp.Body, closed: &closed}
		}
		return resp, err
	})}
	return openai.NewProvider("test", openai.WithBaseURL(server.URL), openai.WithResponsesAPI(true), openai.WithModel("alias"), openai.WithHTTPClient(client))
}

func TestResponsesNonstreamLifecycle(t *testing.T) {
	t.Parallel()
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		for _, key := range []string{"reasoning", "include", "text", "parallel_tool_calls"} {
			if _, present := body[key]; present {
				t.Errorf("unrequested control %s present", key)
			}
		}
		_, _ = io.WriteString(w, `{"status":"completed","output":[`+nonstreamText+`,`+nonstreamText+`]}`)
	}))
	t.Cleanup(server.Close)
	var closed atomic.Int64
	client := &http.Client{Transport: optionTransport(func(req *http.Request) (*http.Response, error) {
		resp, err := http.DefaultTransport.RoundTrip(req)
		if err == nil {
			resp.Body = nonstreamCloseBody{ReadCloser: resp.Body, closed: &closed}
		}
		return resp, err
	})}
	provider := openai.NewProvider("test", openai.WithBaseURL(server.URL), openai.WithResponsesAPI(true), openai.WithHTTPClient(client))
	stream := provider.Complete(t.Context(), llm.CompletionRequest{})
	if requests.Load() != 0 {
		t.Fatal("HTTP before iteration")
	}
	yields := 0
	stream(func(_ llm.CompletionChunk, err error) bool {
		yields++
		if err != nil {
			t.Fatal(err)
		}
		return false
	})
	if yields != 1 || requests.Load() != 1 || closed.Load() != 1 {
		t.Fatalf("early stop: yields=%d HTTP=%d closed=%d", yields, requests.Load(), closed.Load())
	}
	cause := errors.New("stop nonstream")
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	yields = 0
	provider.Complete(ctx, llm.CompletionRequest{})(func(_ llm.CompletionChunk, err error) bool {
		yields++
		if yields == 1 {
			cancel(cause)
		} else if yields != 2 || !errors.Is(err, cause) {
			t.Fatalf("cancel: yield=%d err=%v", yields, err)
		}
		return true
	})
	if yields != 2 || closed.Load() != 2 {
		t.Fatalf("cancel yields=%d closed=%d", yields, closed.Load())
	}
	yields = 0
	for _, err := range provider.Complete(ctx, llm.CompletionRequest{}) {
		yields++
		if !errors.Is(err, cause) {
			t.Fatalf("pre-cancel error = %v", err)
		}
	}
	if yields != 1 || requests.Load() != 2 {
		t.Fatalf("pre-cancel yields=%d HTTP=%d", yields, requests.Load())
	}
}

type nonstreamCloseBody struct {
	io.ReadCloser
	closed *atomic.Int64
}

func (body nonstreamCloseBody) Close() error {
	body.closed.Add(1)
	return body.ReadCloser.Close()
}

func TestResponsesNonstreamCancelDuringRead(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	cause := errors.New("cancel JSON body read")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"completed","output":[`)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	var closed atomic.Int64
	client := &http.Client{Transport: optionTransport(func(req *http.Request) (*http.Response, error) {
		resp, err := http.DefaultTransport.RoundTrip(req)
		if err == nil {
			resp.Body = nonstreamCloseBody{ReadCloser: resp.Body, closed: &closed}
			// Headers and a partial JSON document arrived, but EOF has not.
			cancel(cause)
		}
		return resp, err
	})}
	provider := openai.NewProvider("test", openai.WithBaseURL(server.URL), openai.WithResponsesAPI(true), openai.WithHTTPClient(client))
	yields := 0
	for _, err := range provider.Complete(ctx, llm.CompletionRequest{}) {
		yields++
		if !errors.Is(err, cause) {
			t.Fatalf("read cancellation = %v, want cause", err)
		}
	}
	if yields != 1 || closed.Load() != 1 {
		t.Fatalf("read cancellation: yields=%d closed=%d", yields, closed.Load())
	}
}

func TestResponsesNonstreamThinkingAndJSONObject(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		thinking bool
		effort   string
	}{
		{name: "plain"},
		{name: "thinking", thinking: true, effort: "medium"},
		{name: "option override", thinking: true, effort: "low"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requests := make(chan map[string]any, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode request: %v", err)
				}
				requests <- body
				_, _ = io.WriteString(w, `{"status":"completed","output":[{"type":"reasoning","id":"r1","summary":[{"type":"summary_text","text":"summary"}],"content":[{"type":"reasoning_text","text":"not a second projection"}]},{"type":"reasoning","id":"r2","summary":[],"content":[{"type":"reasoning_text","text":" fallback"}]}]}`)
			}))
			t.Cleanup(server.Close)
			provider := openai.NewProvider("test", openai.WithBaseURL(server.URL), openai.WithResponsesAPI(true))
			req := llm.CompletionRequest{Thinking: llm.ThinkingConfig{Enabled: tc.thinking}, ResponseFormat: llm.JSONObjectResponseFormat()}
			if tc.effort == "low" {
				req.Options = llm.ModelOptions{"reasoning_effort": tc.effort}
			}
			var thinking strings.Builder
			for chunk, err := range provider.Complete(t.Context(), req) {
				if err != nil {
					t.Fatal(err)
				}
				thinking.WriteString(chunk.Thinking)
			}
			if thinking.String() != "summary fallback" {
				t.Fatalf("thinking projection = %q", thinking.String())
			}
			body := <-requests
			if body["text"].(map[string]any)["format"].(map[string]any)["type"] != "json_object" {
				t.Fatalf("JSON object format = %#v", body["text"])
			}
			if tc.thinking {
				if body["reasoning"].(map[string]any)["effort"] != tc.effort || body["include"].([]any)[0] != "reasoning.encrypted_content" {
					t.Fatalf("thinking controls = %#v", body)
				}
			} else if body["reasoning"] != nil || body["include"] != nil {
				t.Fatalf("unrequested reasoning controls = %#v", body)
			}
		})
	}
}
