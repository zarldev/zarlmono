package openaicodex_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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

	seq, err := p.Complete(context.Background(), llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: "system", Content: "you are a friendly bot"},
			{Role: "user", Content: "say hi"},
		},
		Stream: true,
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

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
	if !last.Done || last.FinishReason != "stop" {
		t.Errorf("last chunk done/reason = %v/%q", last.Done, last.FinishReason)
	}
	if last.Usage == nil || last.Usage.PromptTokens != 5 {
		t.Errorf("usage = %+v", last.Usage)
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

func TestProvider_PresetModelMapsBaseAndEffort(t *testing.T) {
	t.Parallel()
	cb := newCodexBackend(t, func(w http.ResponseWriter) {
		w.Write([]byte("data: " + `{"type":"response.completed","response":{"usage":{}}}` + "\n\n"))
	})
	defer cb.Close()

	p, _ := openaicodex.NewProvider(
		openaicodex.StaticTokenSource{T: freshToken(t, "acct_test")},
		openaicodex.WithBaseURL(cb.srv.URL),
		openaicodex.WithModel("gpt-5.3-codex-high"),
	)
	seq, _ := p.Complete(context.Background(), llm.CompletionRequest{
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
		Stream:   true,
	})
	for range seq {
	}

	if got := cb.lastBody["model"]; got != "gpt-5.3-codex" {
		t.Errorf("wire model = %v, want gpt-5.3-codex", got)
	}
	reasoning, _ := cb.lastBody["reasoning"].(map[string]any)
	if reasoning == nil {
		t.Fatalf("reasoning block missing: %v", cb.lastBody)
	}
	if reasoning["effort"] != "high" {
		t.Errorf("effort = %v, want high", reasoning["effort"])
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
	seq, _ := p.Complete(context.Background(), llm.CompletionRequest{
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
	var sawDone bool
	var finishReason string
	for c := range seq {
		for _, tc := range c.ToolCalls {
			args.WriteString(tc.Function.Arguments)
		}
		if c.Done {
			sawDone = true
			finishReason = c.FinishReason
		}
	}
	if args.String() != `{"q":"foo"}` {
		t.Errorf("accumulated args = %q", args.String())
	}
	if !sawDone || finishReason != "tool_calls" {
		t.Errorf("done/reason = %v/%q", sawDone, finishReason)
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
	seq, _ := p.Complete(context.Background(), llm.CompletionRequest{
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
	if !strings.Contains(lastErr.Error(), "429") {
		t.Errorf("err = %v, want 429 in message", lastErr)
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
	seq, _ := p.Complete(context.Background(), llm.CompletionRequest{
		Messages: []llm.Message{{Role: "user", Content: "x"}},
		Stream:   true,
	})
	var content strings.Builder
	var last llm.CompletionChunk
	for c, cerr := range seq {
		if cerr != nil {
			t.Fatalf("unexpected chunk error: %v", cerr)
		}
		content.WriteString(c.Content)
		last = c
	}
	if attempts != 3 {
		t.Errorf("server saw %d attempts, want 3 (two 429s then success)", attempts)
	}
	if content.String() != "hi" {
		t.Errorf("content = %q, want %q", content.String(), "hi")
	}
	if !last.Done {
		t.Errorf("expected terminal Done chunk")
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
	seq, _ := p.Complete(context.Background(), llm.CompletionRequest{
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
	seq, _ := p.Complete(context.Background(), llm.CompletionRequest{
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
	if len(models) < 5 {
		t.Errorf("expected >= 5 preset models, got %d", len(models))
	}
	wantIDs := map[string]bool{
		"gpt-5.5":             false,
		"gpt-5.4":             false,
		"gpt-5.4-mini":        false,
		"gpt-5.3-codex":       false,
		"gpt-5.3-codex-spark": false,
		"gpt-5.2":             false,
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
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"models":[{"slug":"gpt-5.5","display_name":"GPT-5.5","context_window":272000,"auto_compact_token_limit":204000,"effective_context_window_percent":95}]}`)
	}))
	defer srv.Close()

	cw, err := openaicodex.FetchContextWindow(
		context.Background(),
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
		context.Background(),
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

	seq, err := p.Complete(context.Background(), llm.CompletionRequest{
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
		Stream:   false,
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	var content strings.Builder
	var last llm.CompletionChunk
	for c, cerr := range seq {
		if cerr != nil {
			t.Fatalf("chunk error: %v", cerr)
		}
		content.WriteString(c.Content)
		last = c
	}
	if content.String() != "hi" || !last.Done {
		t.Fatalf("content/done = %q/%v, want hi/true", content.String(), last.Done)
	}
	if got, ok := cb.lastBody["stream"].(bool); !ok || !got {
		t.Fatalf("wire stream = %#v, want true", cb.lastBody["stream"])
	}
}
