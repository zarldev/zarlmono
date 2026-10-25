package anthropic_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/anthropic"
)

func TestProviderCacheCreationUsage(t *testing.T) {
	tests := []struct {
		name      string
		usageJSON string
		want      llm.Usage
		wantErr   bool
	}{
		{
			name:      "reported zero",
			usageJSON: `{}`,
			want:      llm.Usage{},
		},
		{
			name:      "aggregate only",
			usageJSON: `{"input_tokens":100,"output_tokens":5,"cache_creation_input_tokens":20,"cache_read_input_tokens":30}`,
			want:      llm.Usage{PromptTokens: 150, CompletionTokens: 5, TotalTokens: 155, CachedTokens: 30, CacheCreationTokens: 20},
		},
		{
			name:      "mixed TTL",
			usageJSON: `{"input_tokens":100,"output_tokens":5,"cache_creation_input_tokens":20,"cache_read_input_tokens":30,"cache_creation":{"ephemeral_5m_input_tokens":7,"ephemeral_1h_input_tokens":11}}`,
			want:      llm.Usage{PromptTokens: 150, CompletionTokens: 5, TotalTokens: 155, CachedTokens: 30, CacheCreationTokens: 20, CacheCreation5mTokens: 7, CacheCreation1hTokens: 11},
		},
		{
			name:      "invalid detail discarded",
			usageJSON: `{"input_tokens":100,"output_tokens":5,"cache_creation_input_tokens":20,"cache_read_input_tokens":30,"cache_creation":{"ephemeral_5m_input_tokens":12,"ephemeral_1h_input_tokens":11}}`,
			want:      llm.Usage{PromptTokens: 150, CompletionTokens: 5, TotalTokens: 155, CachedTokens: 30, CacheCreationTokens: 20},
		},
		{
			name:      "invalid aggregate",
			usageJSON: `{"input_tokens":-1,"output_tokens":5,"cache_creation_input_tokens":20,"cache_read_input_tokens":30}`,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-test","stop_reason":"end_turn","usage":%s}`, tt.usageJSON)
			}))
			t.Cleanup(server.Close)
			provider := anthropic.NewProvider("test-key", anthropic.WithBaseURL(server.URL))

			var got llm.CompletionChunk
			var streamErr error
			for chunk, err := range provider.Complete(t.Context(), llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}}) {
				if err != nil {
					streamErr = err
					continue
				}
				if chunk.UsageReported {
					got = chunk
				}
			}
			if tt.wantErr {
				if streamErr == nil {
					t.Fatal("Complete error = nil, want invalid aggregate error")
				}
				return
			}
			if streamErr != nil {
				t.Fatalf("Complete: %v", streamErr)
			}
			if !got.UsageReported || got.Usage != tt.want {
				t.Fatalf("usage = %+v reported=%v, want %+v", got.Usage, got.UsageReported, tt.want)
			}
		})
	}
}

func TestProviderStreamingCacheCreationUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: message_start\n")
		_, _ = fmt.Fprint(w, `data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-test","stop_reason":null,"usage":{"input_tokens":100,"output_tokens":0,"cache_creation_input_tokens":20,"cache_read_input_tokens":30,"cache_creation":{"ephemeral_5m_input_tokens":7,"ephemeral_1h_input_tokens":11}}}}`+"\n\n")
		_, _ = fmt.Fprint(w, "event: message_delta\n")
		_, _ = fmt.Fprint(w, `data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":5}}`+"\n\n")
		_, _ = fmt.Fprint(w, "event: message_stop\n")
		_, _ = fmt.Fprint(w, `data: {"type":"message_stop"}`+"\n\n")
	}))
	t.Cleanup(server.Close)
	provider := anthropic.NewProvider("test-key", anthropic.WithBaseURL(server.URL))

	var got llm.CompletionChunk
	for chunk, err := range provider.Complete(t.Context(), llm.CompletionRequest{Stream: true, Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}}) {
		if err != nil {
			t.Fatalf("Complete: %v", err)
		}
		if chunk.UsageReported {
			got = chunk
		}
	}
	want := llm.Usage{PromptTokens: 150, CompletionTokens: 5, TotalTokens: 155, CachedTokens: 30, CacheCreationTokens: 20, CacheCreation5mTokens: 7, CacheCreation1hTokens: 11}
	if !got.UsageReported || got.Usage != want {
		t.Fatalf("usage = %+v reported=%v, want %+v", got.Usage, got.UsageReported, want)
	}
}
