package openaicodex_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openaicodex"
)

// freshToken builds a Token wrapper around a JWT carrying the given
// chatgpt_account_id. The Token expires 1h in the future so the
// provider won't try to refresh it during a test.
func freshToken(t *testing.T, accountID string) openaicodex.Token {
	t.Helper()
	tok := makeJWT(t, map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": accountID,
		},
	})
	return openaicodex.Token{
		Access:    tok,
		Refresh:   "r",
		Expires:   time.Now().Add(time.Hour),
		AccountID: accountID,
	}
}

// codexBackend wraps an httptest server that pretends to be
// /codex/responses. The reqRecorder captures the parsed request body
// so tests can assert wire-shape; respond writes the canned SSE
// payload back to the client.
type codexBackend struct {
	t          *testing.T
	srv        *httptest.Server
	lastBody   map[string]any
	lastHeader http.Header
	respond    func(w http.ResponseWriter)
}

func newCodexBackend(t *testing.T, respond func(w http.ResponseWriter)) *codexBackend {
	t.Helper()
	cb := &codexBackend{t: t, respond: respond}
	cb.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/codex/responses" {
			http.Error(w, "wrong path: "+r.URL.Path, http.StatusNotFound)
			return
		}
		cb.lastHeader = r.Header.Clone()
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &cb.lastBody)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		cb.respond(w)
	}))
	return cb
}

func (c *codexBackend) Close() { c.srv.Close() }

func TestProvider_Streaming_TextResponse(t *testing.T) {
	t.Parallel()
	cb := newCodexBackend(t, func(w http.ResponseWriter) {
		// Two text deltas then completed.
		w.Write([]byte("data: " + `{"type":"response.output_text.delta","delta":"Hello"}` + "\n\n"))
		w.Write([]byte("data: " + `{"type":"response.output_text.delta","delta":", world"}` + "\n\n"))
		w.Write(
			[]byte(
				"data: " + `{"type":"response.completed","response":{"usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}}}` + "\n\n",
			),
		)
	})
	defer cb.Close()

	p, err := openaicodex.NewProvider(
		openaicodex.StaticTokenSource{T: freshToken(t, "acct_test")},
		openaicodex.WithBaseURL(cb.srv.URL),
	)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	seq := p.Complete(t.Context(), llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: "system", Content: "you are a friendly bot"},
			{Role: "user", Content: "say hi"},
		},
		Stream: true,
	})

	var content strings.Builder
	var last llm.CompletionChunk
	for c, cerr := range seq {
		if cerr != nil {
			t.Fatalf("chunk error: %v", cerr)
		}
		content.WriteString(c.Content)
		last = c
	}
	if content.String() != "Hello, world" {
		t.Errorf("content = %q, want %q", content.String(), "Hello, world")
	}
	if last.FinishReason != llm.FinishReasons.STOP {
		t.Errorf("last finish reason = %q, want stop", last.FinishReason)
	}
	if !last.UsageReported || last.Usage.PromptTokens != 5 {
		t.Errorf("usage = %+v reported=%v", last.Usage, last.UsageReported)
	}

	// Wire-shape assertions.
	if cb.lastHeader.Get("Authorization") == "" {
		t.Errorf("auth header missing")
	}
	if cb.lastHeader.Get("Chatgpt-Account-Id") != "acct_test" {
		t.Errorf("account-id header = %q", cb.lastHeader.Get("Chatgpt-Account-Id"))
	}
	if cb.lastHeader.Get("Openai-Beta") != "responses=experimental" {
		t.Errorf("OpenAI-Beta header = %q", cb.lastHeader.Get("Openai-Beta"))
	}
	if cb.lastHeader.Get("Originator") != "codex_cli_rs" {
		t.Errorf("originator header = %q", cb.lastHeader.Get("Originator"))
	}
	// Instructions should carry the caller's system message verbatim,
	// without any provider-injected suffix.
	instr, _ := cb.lastBody["instructions"].(string)
	if instr != "you are a friendly bot" {
		t.Errorf("instructions = %q, want exactly the system message", instr)
	}
	// System message should not appear in input.
	input, _ := cb.lastBody["input"].([]any)
	if len(input) != 1 {
		t.Errorf("expected 1 input item (user only), got %d: %v", len(input), input)
	}
}

func TestProvider_ResponseFormatWireShape(t *testing.T) {
	t.Parallel()
	schema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"v": map[string]any{"type": "string"}},
	}
	cases := []struct {
		name       string
		format     llm.ResponseFormat
		options    llm.ModelOptions
		wantType   string
		wantName   string
		wantStrict bool
		wantSchema bool
		wantText   bool
		verbosity  string
	}{
		{name: "JSON schema", format: llm.ResponseFormat{Type: llm.ResponseFormatJSONSchema, Name: "verdict", Schema: llm.SchemaFromMap(schema), Strict: true}, wantType: "json_schema", wantName: "verdict", wantStrict: true, wantSchema: true, wantText: true},
		{name: "default schema name", format: llm.ResponseFormat{Type: llm.ResponseFormatJSONSchema, Schema: llm.SchemaFromMap(schema)}, wantType: "json_schema", wantName: "response", wantSchema: true, wantText: true},
		{name: "JSON object", format: llm.ResponseFormat{Type: llm.ResponseFormatJSONObject}, wantType: "json_object", wantText: true},
		{name: "unconstrained", wantText: false},
		{name: "verbosity and format", format: llm.ResponseFormat{Type: llm.ResponseFormatJSONObject}, options: llm.ModelOptions{"text_verbosity": "low"}, wantType: "json_object", wantText: true, verbosity: "low"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cb := newCodexBackend(t, func(w http.ResponseWriter) {
				_, _ = io.WriteString(w, "data: "+`{"type":"response.completed","response":{"usage":{}}}`+"\n\n")
			})
			defer cb.Close()
			p, err := openaicodex.NewProvider(openaicodex.StaticTokenSource{T: freshToken(t, "acct")}, openaicodex.WithBaseURL(cb.srv.URL), openaicodex.WithNoRetry())
			if err != nil {
				t.Fatalf("NewProvider: %v", err)
			}
			for _, streamErr := range p.Complete(t.Context(), llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "test"}}, ResponseFormat: tt.format, Options: tt.options}) {
				if streamErr != nil {
					t.Fatalf("Complete: %v", streamErr)
				}
			}
			text, hasText := cb.lastBody["text"].(map[string]any)
			if hasText != tt.wantText {
				t.Fatalf("text block present = %v, want %v; body=%v", hasText, tt.wantText, cb.lastBody)
			}
			if !tt.wantText {
				return
			}
			format, _ := text["format"].(map[string]any)
			if got := format["type"]; got != tt.wantType {
				t.Errorf("format type = %v, want %q", got, tt.wantType)
			}
			if got := format["name"]; tt.wantName != "" && got != tt.wantName {
				t.Errorf("format name = %v, want %q", got, tt.wantName)
			}
			if got, _ := format["strict"].(bool); got != tt.wantStrict {
				t.Errorf("strict = %v, want %v", got, tt.wantStrict)
			}
			if _, ok := format["schema"]; ok != tt.wantSchema {
				t.Errorf("schema present = %v, want %v", ok, tt.wantSchema)
			}
			if got := text["verbosity"]; tt.verbosity != "" && got != tt.verbosity {
				t.Errorf("verbosity = %v, want %q", got, tt.verbosity)
			}
		})
	}
}

func TestProvider_RequestWireRegressions(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		model string
		req   llm.CompletionRequest
		check func(*testing.T, map[string]any)
	}{
		{name: "basic shape", model: "gpt-5.1-codex", req: llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "hello"}}}, check: func(t *testing.T, body map[string]any) {
			if body["model"] != "gpt-5.1-codex" || body["stream"] != true || body["store"] != false {
				t.Errorf("basic wire shape = %v", body)
			}
			reasoning := body["reasoning"].(map[string]any)
			if reasoning["effort"] != "medium" {
				t.Errorf("default reasoning effort = %v, want medium", reasoning["effort"])
			}
		}},
		{name: "tool call and output", model: "gpt-5.1-codex", req: llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "search"}, {Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_1", Type: "function", Function: llm.ToolCallFunction{Name: "search", Arguments: `{"q":"foo"}`}}}}, {Role: llm.RoleTool, ToolCallID: "call_1", Content: `{"results":["bar"]}`}}}, check: func(t *testing.T, body map[string]any) {
			input := body["input"].([]any)
			if len(input) != 3 || input[1].(map[string]any)["type"] != "function_call" || input[2].(map[string]any)["type"] != "function_call_output" {
				t.Errorf("tool input shape = %v", input)
			}
		}},
		{name: "options", model: "gpt-5.2", req: llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, Options: llm.ModelOptions{"reasoning_effort": "high", "reasoning_summary": "concise", "text_verbosity": "low", "tool_choice": "required", "prompt_cache_key": "sess-123"}}, check: func(t *testing.T, body map[string]any) {
			if body["tool_choice"] != "required" || body["prompt_cache_key"] != "sess-123" || body["text"].(map[string]any)["verbosity"] != "low" {
				t.Errorf("option wire shape = %v", body)
			}
		}},
		{name: "multimodal image", model: "gpt-5.1-codex", req: llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Parts: []llm.ContentPart{llm.TextPart("look"), llm.ImagePartFromURL("https://example.com/cat.png")}}}}, check: func(t *testing.T, body map[string]any) {
			content := body["input"].([]any)[0].(map[string]any)["content"].([]any)
			if content[1].(map[string]any)["type"] != "input_image" {
				t.Errorf("multimodal content = %v", content)
			}
		}},
		{name: "spark omits summary", model: "gpt-5.3-codex-spark", req: llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, Options: llm.ModelOptions{"reasoning_effort": "high", "reasoning_summary": "concise"}}, check: func(t *testing.T, body map[string]any) {
			if _, ok := body["reasoning"]; ok {
				t.Errorf("unsupported spark effort serialized: %v", body["reasoning"])
			}
		}},
		{name: "codex effort values and cache semantics", model: "gpt-5.6", req: llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, Options: llm.ModelOptions{"reasoning_effort": "max", "text_verbosity": "high", "prompt_cache_key": "cache-local"}}, check: func(t *testing.T, body map[string]any) {
			if body["reasoning"].(map[string]any)["effort"] != "max" || body["text"].(map[string]any)["verbosity"] != "high" || body["prompt_cache_key"] != "cache-local" {
				t.Errorf("Codex options changed: %v", body)
			}
		}},
		{name: "unsupported effort omitted after model switch", model: "gpt-5.4-mini", req: llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, Options: llm.ModelOptions{"reasoning_effort": "max"}}, check: func(t *testing.T, body map[string]any) {
			if _, ok := body["reasoning"]; ok {
				t.Errorf("unsupported mini effort serialized: %v", body["reasoning"])
			}
		}},
		{name: "invalid verbosity omitted", model: "gpt-5.6", req: llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, Options: llm.ModelOptions{"text_verbosity": "verbose"}}, check: func(t *testing.T, body map[string]any) {
			if _, ok := body["text"]; ok {
				t.Errorf("invalid text verbosity serialized: %v", body["text"])
			}
		}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cb := newCodexBackend(t, func(w http.ResponseWriter) {
				_, _ = io.WriteString(w, "data: "+`{"type":"response.completed","response":{"usage":{}}}`+"\n\n")
			})
			defer cb.Close()
			p, err := openaicodex.NewProvider(openaicodex.StaticTokenSource{T: freshToken(t, "acct")}, openaicodex.WithBaseURL(cb.srv.URL), openaicodex.WithNoRetry(), openaicodex.WithModel(tt.model))
			if err != nil {
				t.Fatalf("NewProvider: %v", err)
			}
			for _, streamErr := range p.Complete(t.Context(), tt.req) {
				if streamErr != nil {
					t.Fatalf("Complete: %v", streamErr)
				}
			}
			tt.check(t, cb.lastBody)
		})
	}
}

func TestProvider_AstraMaxPresetMapsBaseModelAndEffort(t *testing.T) {
	t.Parallel()
	cb := newCodexBackend(t, func(w http.ResponseWriter) {
		_, _ = io.WriteString(w, "data: "+`{"type":"response.completed","response":{"usage":{}}}`+"\n\n")
	})
	defer cb.Close()

	provider, err := openaicodex.NewProvider(
		openaicodex.StaticTokenSource{T: freshToken(t, "acct_test")},
		openaicodex.WithBaseURL(cb.srv.URL),
		openaicodex.WithNoRetry(),
		openaicodex.WithModel("gpt-6-astra-max"),
	)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	for _, streamErr := range provider.Complete(t.Context(), llm.CompletionRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hello"}},
	}) {
		if streamErr != nil {
			t.Fatalf("Complete: %v", streamErr)
		}
	}

	if got := cb.lastBody["model"]; got != "gpt-6-astra" {
		t.Fatalf("wire model = %v, want gpt-6-astra", got)
	}
	reasoning, ok := cb.lastBody["reasoning"].(map[string]any)
	if !ok {
		t.Fatalf("reasoning = %#v, want object", cb.lastBody["reasoning"])
	}
	if got := reasoning["effort"]; got != "max" {
		t.Fatalf("reasoning effort = %v, want max", got)
	}
}

func TestGPT6AstraSupportsAllReasoningEfforts(t *testing.T) {
	t.Parallel()
	want := []string{"low", "medium", "high", "xhigh", "max"}
	if got := openaicodex.EffortVariants("gpt-6-astra"); !slices.Equal(got, want) {
		t.Fatalf("EffortVariants(gpt-6-astra) = %v, want %v", got, want)
	}
}

func TestProvider_ReplaysEncryptedReasoningInNativeOrder(t *testing.T) {
	t.Parallel()
	cb := newCodexBackend(t, func(w http.ResponseWriter) {
		_, _ = io.WriteString(w, "data: "+`{"type":"response.completed","response":{"usage":{}}}`+"\n\n")
	})
	defer cb.Close()
	provider, err := openaicodex.NewProvider(
		openaicodex.StaticTokenSource{T: freshToken(t, "acct")},
		openaicodex.WithBaseURL(cb.srv.URL),
		openaicodex.WithNoRetry(),
	)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	reasoningData := []byte(`{"type":"reasoning","id":"rs_reason","encrypted_content":"opaque-ciphertext","summary":[{"type":"summary_text","text":"preserve me"}],"status":"completed","vendor":{"nested":7}}`)
	req := llm.CompletionRequest{Messages: []llm.Message{
		{Role: llm.RoleUser, Content: "search"},
		{
			Role:               llm.RoleAssistant,
			Content:            "calling search",
			ContentOutputIndex: llm.OutputPosition(2),
			ContinuationItems: []llm.ContinuationItem{
				{
					OutputIndex: llm.OutputPosition(1),
					Provider:    "openai-codex",
					Format:      "responses.reasoning.v1",
					Kind:        "reasoning",
					ID:          "rs_reason",
					Data:        reasoningData,
				},
				{
					OutputIndex: llm.OutputPosition(0),
					Provider:    "anthropic",
					Format:      "content_block.v1",
					Kind:        "thinking",
					Data:        []byte(`{"type":"reasoning","id":"foreign","encrypted_content":"must-not-replay"}`),
				},
			},
			ToolCalls: []llm.ToolCall{{
				ID: "call_tool", Type: "function", OutputIndex: llm.OutputPosition(0),
				Function: llm.ToolCallFunction{Name: "search", Arguments: `{"q":"foo"}`},
			}},
		},
		{Role: llm.RoleTool, ToolCallID: "call_tool", Content: "bar"},
	}}
	for _, streamErr := range provider.Complete(t.Context(), req) {
		if streamErr != nil {
			t.Fatalf("Complete: %v", streamErr)
		}
	}

	input := cb.lastBody["input"].([]any)
	if len(input) != 5 {
		t.Fatalf("input = %#v, want user/message/reasoning/function/output with foreign item ignored", input)
	}
	wantTypes := []string{"message", "function_call", "reasoning", "message", "function_call_output"}
	for i, want := range wantTypes {
		if got := input[i].(map[string]any)["type"]; got != want {
			t.Fatalf("input[%d].type = %v, want %s; input=%#v", i, got, want, input)
		}
	}
	reasoning := input[2].(map[string]any)
	if reasoning["id"] != "rs_reason" || reasoning["encrypted_content"] != "opaque-ciphertext" {
		t.Fatalf("reasoning item = %#v", reasoning)
	}
	if reasoning["status"] != "completed" || reasoning["vendor"].(map[string]any)["nested"] != float64(7) || reasoning["summary"].([]any)[0].(map[string]any)["text"] != "preserve me" {
		t.Fatalf("reasoning additional fields not preserved: %#v", reasoning)
	}
	if _, ok := reasoning["call_id"]; ok {
		t.Fatalf("reasoning item confused id with call_id: %#v", reasoning)
	}
	call := input[1].(map[string]any)
	if call["call_id"] != "call_tool" {
		t.Fatalf("function call = %#v", call)
	}

	reasoningData[0] = 'X'
	if got := input[2].(map[string]any)["type"]; got != "reasoning" {
		t.Fatalf("request retained continuation backing bytes: %#v", input[2])
	}
}

func TestProviderOptionsAssignEmptyValues(t *testing.T) {
	t.Parallel()
	cb := newCodexBackend(t, func(w http.ResponseWriter) {
		_, _ = io.WriteString(w, "data: "+`{"type":"response.completed","response":{"usage":{}}}`+"\n\n")
	})
	defer cb.Close()

	p, err := openaicodex.NewProvider(
		openaicodex.StaticTokenSource{T: freshToken(t, "acct_test")},
		openaicodex.WithBaseURL(cb.srv.URL),
		openaicodex.WithModel(""),
		openaicodex.WithDefaultReasoningEffort(""),
	)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	seq := p.Complete(t.Context(), llm.CompletionRequest{
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	for _, err := range seq {
		if err != nil {
			t.Fatalf("chunk error: %v", err)
		}
	}
	if got := cb.lastBody["model"]; got != "" {
		t.Errorf("wire model = %v, want empty option value", got)
	}
}

func TestProvider_ToolCallStream(t *testing.T) {
	t.Parallel()
	cb := newCodexBackend(t, func(w http.ResponseWriter) {
		w.Write(
			[]byte(
				"data: " + `{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_a","name":"search","arguments":""}}` + "\n\n",
			),
		)
		w.Write(
			[]byte(
				"data: " + `{"type":"response.function_call_arguments.delta","output_index":0,"item_id":"fc_1","delta":"{\"q\":\"foo\"}"}` + "\n\n",
			),
		)
		w.Write([]byte("data: " + `{"type":"response.completed","response":{"usage":{}}}` + "\n\n"))
	})
	defer cb.Close()

	p, _ := openaicodex.NewProvider(
		openaicodex.StaticTokenSource{T: freshToken(t, "acct_test")},
		openaicodex.WithBaseURL(cb.srv.URL),
	)
	seq := p.Complete(t.Context(), llm.CompletionRequest{
		Messages: []llm.Message{{Role: "user", Content: "search foo"}},
		Tools: []llm.Tool{{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "search",
				Description: "search the web",
				Parameters:  llm.Schema{Type: "object"},
			},
		}},
		Stream: true,
	})
	var args strings.Builder
	var finishReason llm.FinishReason
	for c := range seq {
		for _, tc := range c.ToolCalls {
			args.WriteString(tc.Function.Arguments)
		}
		if c.FinishReason != llm.FinishReasons.UNKNOWN {
			finishReason = c.FinishReason
		}
	}
	if args.String() != `{"q":"foo"}` {
		t.Errorf("accumulated args = %q", args.String())
	}
	if finishReason != llm.FinishReasons.TOOLCALLS {
		t.Errorf("finish reason = %q, want tool_calls", finishReason)
	}

	tools, _ := cb.lastBody["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool in body, got %d", len(tools))
	}
	tool, _ := tools[0].(map[string]any)
	if tool["type"] != "function" || tool["name"] != "search" {
		t.Errorf("tool wire shape wrong: %v", tool)
	}
}

func TestProvider_HTTPErrorSurfacesAsChunkError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"message":"rate limit"}}`)
	}))
	defer srv.Close()

	p, _ := openaicodex.NewProvider(
		openaicodex.StaticTokenSource{T: freshToken(t, "acct_test")},
		openaicodex.WithBaseURL(srv.URL),
		openaicodex.WithNoRetry(),
	)
	seq := p.Complete(t.Context(), llm.CompletionRequest{
		Messages: []llm.Message{{Role: "user", Content: "x"}},
		Stream:   true,
	})
	var lastErr error
	for _, cerr := range seq {
		if cerr != nil {
			lastErr = cerr
		}
	}
	if lastErr == nil {
		t.Fatalf("expected error chunk")
	}
	if !llm.IsRateLimitError(lastErr) {
		t.Errorf("err = %v, want a typed rate-limit error", lastErr)
	}
	// The clean body message is surfaced; the raw JSON / status code is not.
	if !strings.Contains(lastErr.Error(), "rate limit") {
		t.Errorf("err = %v, want the provider's body message", lastErr)
	}
	if strings.Contains(lastErr.Error(), "{") {
		t.Errorf("err = %v, should not contain raw JSON", lastErr)
	}
}

// A Codex usage-limit 429 carries its reset window and human message in the
// JSON body (not headers). The provider must extract them into the typed
// error and never leak the raw JSON.
func TestProvider_UsageLimitBodyParsedIntoRateLimitError(t *testing.T) {
	t.Parallel()
	const body = `{"error":{"type":"usage_limit_reached","message":"The usage limit has been reached","plan_type":"prolite","resets_at":1782686042,"eligible_promo":null,"resets_in_seconds":203391}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()

	p, _ := openaicodex.NewProvider(
		openaicodex.StaticTokenSource{T: freshToken(t, "acct_test")},
		openaicodex.WithBaseURL(srv.URL),
		openaicodex.WithNoRetry(),
	)
	seq := p.Complete(t.Context(), llm.CompletionRequest{
		Messages: []llm.Message{{Role: "user", Content: "x"}},
		Stream:   true,
	})
	var lastErr error
	for _, cerr := range seq {
		if cerr != nil {
			lastErr = cerr
		}
	}
	rle, ok := errors.AsType[*llm.RateLimitError](lastErr)
	if !ok {
		t.Fatalf("err = %v, want *llm.RateLimitError", lastErr)
	}
	if rle.Message != "The usage limit has been reached" {
		t.Errorf("Message = %q, want the clean body message", rle.Message)
	}
	if want := time.Unix(1782686042, 0); !rle.ResetAt.Equal(want) {
		t.Errorf("ResetAt = %v, want %v", rle.ResetAt, want)
	}
	if rle.RetryAfter != 203391*time.Second {
		t.Errorf("RetryAfter = %v, want %v", rle.RetryAfter, 203391*time.Second)
	}
	if strings.Contains(rle.Error(), "{") || strings.Contains(rle.Error(), "resets_at") {
		t.Errorf("error string leaks raw JSON: %q", rle.Error())
	}
}

func TestProvider_RetriesOn429ThenSucceeds(t *testing.T) {
	t.Parallel()
	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"detail":"Rate limit exceeded"}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: "+`{"type":"response.output_text.delta","delta":"hi"}`+"\n\n")
		_, _ = io.WriteString(w, "data: "+`{"type":"response.completed","response":{"usage":{}}}`+"\n\n")
	}))
	defer srv.Close()

	p, _ := openaicodex.NewProvider(
		openaicodex.StaticTokenSource{T: freshToken(t, "acct_test")},
		openaicodex.WithBaseURL(srv.URL),
		// Tight policy so the test stays fast; Retry-After: 0 keeps
		// the backoff at the minimum.
		openaicodex.WithRetryPolicy(4, 10*time.Millisecond, 50*time.Millisecond),
	)
	seq := p.Complete(t.Context(), llm.CompletionRequest{
		Messages: []llm.Message{{Role: "user", Content: "x"}},
		Stream:   true,
	})
	var content strings.Builder
	for c, cerr := range seq {
		if cerr != nil {
			t.Fatalf("unexpected chunk error: %v", cerr)
		}
		content.WriteString(c.Content)
	}
	if attempts != 3 {
		t.Errorf("server saw %d attempts, want 3 (two 429s then success)", attempts)
	}
	if content.String() != "hi" {
		t.Errorf("content = %q, want %q", content.String(), "hi")
	}
}

func TestProvider_NoRetryOptionUsesActiveClient(t *testing.T) {
	t.Parallel()
	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	p, err := openaicodex.NewProvider(
		openaicodex.StaticTokenSource{T: freshToken(t, "acct_test")},
		openaicodex.WithNoRetry(),
		openaicodex.WithBaseURL(srv.URL),
	)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	seq := p.Complete(t.Context(), llm.CompletionRequest{
		Messages: []llm.Message{{Role: "user", Content: "x"}},
	})
	for range seq {
	}
	if attempts != 1 {
		t.Errorf("server saw %d attempts, want 1", attempts)
	}
}

func TestProvider_RetriesHonorRetryAfter(t *testing.T) {
	t.Parallel()
	var attempts int
	var firstAt, secondAt time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			firstAt = time.Now()
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"detail":"Rate limit exceeded"}`)
			return
		}
		secondAt = time.Now()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: "+`{"type":"response.completed","response":{"usage":{}}}`+"\n\n")
	}))
	defer srv.Close()

	p, _ := openaicodex.NewProvider(
		openaicodex.StaticTokenSource{T: freshToken(t, "acct_test")},
		openaicodex.WithBaseURL(srv.URL),
		// Set exponential base far below the Retry-After hint so any
		// gap shorter than ~1s proves the header didn't win.
		openaicodex.WithRetryPolicy(4, 10*time.Millisecond, 5*time.Second),
	)
	seq := p.Complete(t.Context(), llm.CompletionRequest{
		Messages: []llm.Message{{Role: "user", Content: "x"}},
		Stream:   true,
	})
	for _, cerr := range seq {
		if cerr != nil {
			t.Fatalf("unexpected chunk error: %v", cerr)
		}
	}
	gap := secondAt.Sub(firstAt)
	if gap < 900*time.Millisecond {
		t.Errorf("gap between attempts = %v, want >= ~1s to prove Retry-After honored", gap)
	}
}

func TestProvider_DoesNotRetryOn4xxOtherThan429(t *testing.T) {
	t.Parallel()
	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"malformed"}`)
	}))
	defer srv.Close()

	p, _ := openaicodex.NewProvider(
		openaicodex.StaticTokenSource{T: freshToken(t, "acct_test")},
		openaicodex.WithBaseURL(srv.URL),
		openaicodex.WithRetryPolicy(4, 10*time.Millisecond, 50*time.Millisecond),
	)
	seq := p.Complete(t.Context(), llm.CompletionRequest{
		Messages: []llm.Message{{Role: "user", Content: "x"}},
		Stream:   true,
	})
	var lastErr error
	for _, cerr := range seq {
		if cerr != nil {
			lastErr = cerr
		}
	}
	if attempts != 1 {
		t.Errorf("server saw %d attempts on 400, want 1 (no retry on non-429 4xx)", attempts)
	}
	if lastErr == nil || !strings.Contains(lastErr.Error(), "400") {
		t.Errorf("err = %v, want 400 surfaced", lastErr)
	}
}

func TestListPresetModelsContainsExpectedIDs(t *testing.T) {
	t.Parallel()
	models := openaicodex.ListPresetModels()
	if len(models) < 9 {
		t.Errorf("expected >= 9 preset models, got %d", len(models))
	}
	wantIDs := map[string]bool{
		"gpt-6-astra":         false,
		"gpt-5.6":             false,
		"gpt-5.6-sol":         false,
		"gpt-5.6-terra":       false,
		"gpt-5.6-luna":        false,
		"gpt-5.5":             false,
		"gpt-5.4":             false,
		"gpt-5.4-mini":        false,
		"gpt-5.3-codex-spark": false,
	}
	for _, m := range models {
		if _, ok := wantIDs[m.ID]; ok {
			wantIDs[m.ID] = true
		}
	}
	for id, found := range wantIDs {
		if !found {
			t.Errorf("preset %q missing from ListPresetModels", id)
		}
	}
}

func TestFetchContextWindowUsesBackendAutoCompactLimit(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/codex/models" {
			http.Error(w, "wrong path: "+r.URL.Path, http.StatusNotFound)
			return
		}
		if r.Header.Get("Chatgpt-Account-Id") != "acct_test" {
			http.Error(w, "missing account header", http.StatusUnauthorized)
			return
		}
		if got := r.URL.Query().Get("client_version"); got != "0.0.0" {
			http.Error(w, "client_version = "+got, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"models":[{"slug":"gpt-5.5","display_name":"GPT-5.5","context_window":272000,"auto_compact_token_limit":204000,"effective_context_window_percent":95}]}`)
	}))
	defer srv.Close()

	cw, err := openaicodex.FetchContextWindow(
		t.Context(),
		openaicodex.StaticTokenSource{T: freshToken(t, "acct_test")},
		srv.URL,
		"gpt-5.5-high",
	)
	if err != nil {
		t.Fatalf("FetchContextWindow: %v", err)
	}
	if cw != 204000 {
		t.Errorf("context window = %d, want backend auto_compact_token_limit 204000", cw)
	}
}

func TestFetchContextWindowDerivesUpstreamAutoCompactDefault(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/codex/models" {
			http.Error(w, "wrong path: "+r.URL.Path, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"models":[{"slug":"gpt-5.5","display_name":"GPT-5.5","context_window":272000,"effective_context_window_percent":95}]}`)
	}))
	defer srv.Close()

	cw, err := openaicodex.FetchContextWindow(
		t.Context(),
		openaicodex.StaticTokenSource{T: freshToken(t, "acct_test")},
		srv.URL,
		"gpt-5.5",
	)
	if err != nil {
		t.Fatalf("FetchContextWindow: %v", err)
	}
	if cw != 244800 {
		t.Errorf("context window = %d, want upstream auto-compaction default 244800 (272000 × 90%%)", cw)
	}
}

func TestPresetContextWindowIsConservativeForOAuthBackend(t *testing.T) {
	t.Parallel()
	for _, model := range []string{"gpt-5.5", "gpt-5.5-high", "gpt-5.4", "unknown-experimental"} {
		if got := openaicodex.ContextWindowFor(model); got != openaicodex.DefaultContextWindow {
			t.Errorf("ContextWindowFor(%q) = %d, want %d", model, got, openaicodex.DefaultContextWindow)
		}
	}
}

func TestGPT6AstraPresetContextWindow(t *testing.T) {
	t.Parallel()
	const wantContextWindow = 1_050_000

	for _, id := range []string{"gpt-6-astra", "gpt-6-astra-max"} {
		if got := openaicodex.ContextWindowFor(id); got != wantContextWindow {
			t.Errorf("ContextWindowFor(%q) = %d, want %d", id, got, wantContextWindow)
		}
	}

	for _, model := range openaicodex.ListPresetModels() {
		if model.ID != "gpt-6-astra" {
			continue
		}
		if model.MaxTokens != wantContextWindow {
			t.Fatalf("gpt-6-astra MaxTokens = %d, want %d", model.MaxTokens, wantContextWindow)
		}
		return
	}
	t.Fatal("gpt-6-astra missing from ListPresetModels")
}

func TestProvider_NonStreamingCallerStillRequestsSSE(t *testing.T) {
	t.Parallel()
	cb := newCodexBackend(t, func(w http.ResponseWriter) {
		_, _ = w.Write([]byte("data: " + `{"type":"response.output_text.delta","delta":"hi"}` + "\n\n"))
		_, _ = w.Write([]byte("data: " + `{"type":"response.completed","response":{"usage":{}}}` + "\n\n"))
	})
	defer cb.Close()

	p, err := openaicodex.NewProvider(
		openaicodex.StaticTokenSource{T: freshToken(t, "acct_test")},
		openaicodex.WithBaseURL(cb.srv.URL),
	)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	seq := p.Complete(t.Context(), llm.CompletionRequest{
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
		Stream:   false,
	})
	var content strings.Builder
	var last llm.CompletionChunk
	for c, cerr := range seq {
		if cerr != nil {
			t.Fatalf("chunk error: %v", cerr)
		}
		content.WriteString(c.Content)
		last = c
	}
	if content.String() != "hi" || last.FinishReason != llm.FinishReasons.STOP {
		t.Fatalf("content/reason = %q/%q, want hi/stop", content.String(), last.FinishReason)
	}
	if got, ok := cb.lastBody["stream"].(bool); !ok || !got {
		t.Fatalf("wire stream = %#v, want true", cb.lastBody["stream"])
	}
}
