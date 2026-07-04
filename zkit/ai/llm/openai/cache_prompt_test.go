package openai_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
	"github.com/zarldev/zarlmono/zkit/options"
)

// TestRequest_CachePromptGating checks that the non-standard llama.cpp
// `cache_prompt` field is sent only when the provider was built with
// WithCachePrompt(true). Strict OpenAI-compatible backends and forwarding
// proxies (LiteLLM) reject the unknown field with HTTP 400, so it must be
// absent by default.
func TestRequest_CachePromptGating(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
		want    bool
	}{
		{name: "Default_NoCachePrompt", enabled: false, want: false},
		{name: "WithCachePrompt_Sent", enabled: true, want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var captured []byte
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				captured, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(
					`data: {"id":"x","object":"chat.completion.chunk","choices":[{"delta":{"content":"ok"},"finish_reason":"stop","index":0}]}` + "\n\n",
				))
				_, _ = w.Write([]byte("data: [DONE]\n\n"))
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}))
			defer srv.Close()

			opts := []options.Option[openai.Provider]{openai.WithBaseURL(srv.URL)}
			if tc.enabled {
				opts = append(opts, openai.WithCachePrompt(true))
			}
			provider, err := openai.NewProvider("test-key", opts...)
			if err != nil {
				t.Fatalf("NewProvider: %v", err)
			}

			seq, err := provider.Complete(t.Context(), llm.CompletionRequest{
				Messages: []llm.Message{{Role: "user", Content: "hi"}},
				Stream:   true,
			})
			if err != nil {
				t.Fatalf("Complete: %v", err)
			}
			for range seq {
			}

			var body map[string]any
			if err := json.Unmarshal(captured, &body); err != nil {
				t.Fatalf("unmarshal request body: %v\nbody: %s", err, captured)
			}
			_, got := body["cache_prompt"]
			if got != tc.want {
				t.Errorf("cache_prompt present = %v, want %v (body: %s)", got, tc.want, captured)
			}
		})
	}
}

// TestRequest_ChatTemplateKwargsGating checks that the non-standard
// `chat_template_kwargs` field is sent only when explicitly enabled. GPT
// providers and strict OpenAI-compatible proxies reject this as an unknown
// parameter, while local llama.cpp/Ollama-style wrappers opt in.
func TestRequest_ChatTemplateKwargsGating(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
		want    bool
	}{
		{name: "Default_NoChatTemplateKwargs", enabled: false, want: false},
		{name: "WithChatTemplateKwargs_Sent", enabled: true, want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var captured []byte
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				captured, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(
					`data: {"id":"x","object":"chat.completion.chunk","choices":[{"delta":{"content":"ok"},"finish_reason":"stop","index":0}]}` + "\n\n",
				))
				_, _ = w.Write([]byte("data: [DONE]\n\n"))
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}))
			defer srv.Close()

			opts := []options.Option[openai.Provider]{openai.WithBaseURL(srv.URL)}
			if tc.enabled {
				opts = append(opts, openai.WithChatTemplateKwargs(true))
			}
			provider, err := openai.NewProvider("test-key", opts...)
			if err != nil {
				t.Fatalf("NewProvider: %v", err)
			}

			seq, err := provider.Complete(t.Context(), llm.CompletionRequest{
				Messages:           []llm.Message{{Role: "user", Content: "hi"}},
				Stream:             true,
				ChatTemplateKwargs: llm.ChatTemplateKwargs{EnableThinking: true},
			})
			if err != nil {
				t.Fatalf("Complete: %v", err)
			}
			for range seq {
			}

			var body map[string]any
			if err := json.Unmarshal(captured, &body); err != nil {
				t.Fatalf("unmarshal request body: %v\nbody: %s", err, captured)
			}
			_, got := body["chat_template_kwargs"]
			if got != tc.want {
				t.Errorf("chat_template_kwargs present = %v, want %v (body: %s)", got, tc.want, captured)
			}
		})
	}
}
