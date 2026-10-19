package claudecode_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestProviderCacheCreationUsage(t *testing.T) {
	tests := []struct {
		name     string
		stream   string
		want     llm.Usage
		reported bool
		wantErr  bool
	}{
		{name: "absent", stream: `{"type":"result","subtype":"success"}`},
		{name: "reported zero", stream: `{"type":"result","subtype":"success","usage":{}}`, reported: true},
		{
			name:     "aggregate only",
			stream:   `{"type":"result","subtype":"success","usage":{"input_tokens":100,"output_tokens":5,"cache_creation_input_tokens":20,"cache_read_input_tokens":30}}`,
			want:     llm.Usage{PromptTokens: 150, CompletionTokens: 5, TotalTokens: 155, CachedTokens: 30, CacheCreationTokens: 20},
			reported: true,
		},
		{
			name: "terminal totals replace assistant fallback",
			stream: `{"type":"assistant","message":{"usage":{"input_tokens":10,"output_tokens":2,"cache_creation_input_tokens":3}}}
{"type":"result","subtype":"success","usage":{"input_tokens":100,"output_tokens":5,"cache_creation_input_tokens":20,"cache_read_input_tokens":30}}`,
			want:     llm.Usage{PromptTokens: 150, CompletionTokens: 5, TotalTokens: 155, CachedTokens: 30, CacheCreationTokens: 20},
			reported: true,
		},
		{
			name:     "5m detail",
			stream:   `{"type":"result","subtype":"success","usage":{"cache_creation_input_tokens":20,"cache_creation":{"ephemeral_5m_input_tokens":20}}}`,
			want:     llm.Usage{PromptTokens: 20, TotalTokens: 20, CacheCreationTokens: 20, CacheCreation5mTokens: 20},
			reported: true,
		},
		{
			name:     "1h detail",
			stream:   `{"type":"result","subtype":"success","usage":{"cache_creation_input_tokens":20,"cache_creation":{"ephemeral_1h_input_tokens":20}}}`,
			want:     llm.Usage{PromptTokens: 20, TotalTokens: 20, CacheCreationTokens: 20, CacheCreation1hTokens: 20},
			reported: true,
		},
		{
			name:     "mixed TTL with unknown residual",
			stream:   `{"type":"result","subtype":"success","usage":{"cache_creation_input_tokens":20,"cache_creation":{"ephemeral_5m_input_tokens":7,"ephemeral_1h_input_tokens":11}}}`,
			want:     llm.Usage{PromptTokens: 20, TotalTokens: 20, CacheCreationTokens: 20, CacheCreation5mTokens: 7, CacheCreation1hTokens: 11},
			reported: true,
		},
		{
			name:     "invalid detail discarded",
			stream:   `{"type":"result","subtype":"success","usage":{"cache_creation_input_tokens":20,"cache_creation":{"ephemeral_5m_input_tokens":12,"ephemeral_1h_input_tokens":11}}}`,
			want:     llm.Usage{PromptTokens: 20, TotalTokens: 20, CacheCreationTokens: 20},
			reported: true,
		},
		{
			name:     "malformed detail discarded",
			stream:   `{"type":"result","subtype":"success","usage":{"cache_creation_input_tokens":20,"cache_creation":{"ephemeral_5m_input_tokens":"unknown"}}}`,
			want:     llm.Usage{PromptTokens: 20, TotalTokens: 20, CacheCreationTokens: 20},
			reported: true,
		},
		{
			name:    "invalid aggregate",
			stream:  `{"type":"result","subtype":"success","usage":{"input_tokens":-1}}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, _ := newFixtureProvider(t, tt.stream)
			var got llm.CompletionChunk
			var streamErr error
			for chunk, err := range provider.Complete(t.Context(), llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}}) {
				if err != nil {
					streamErr = err
					continue
				}
				got = chunk
			}
			if tt.wantErr {
				if streamErr == nil {
					t.Fatal("Complete error = nil, want invalid aggregate error")
				}
				if !strings.Contains(streamErr.Error(), "invalid usage input_tokens") {
					t.Fatalf("Complete error = %q, want invalid usage detail", streamErr)
				}
				return
			}
			if streamErr != nil {
				t.Fatalf("Complete: %v", streamErr)
			}
			if got.UsageReported != tt.reported || got.Usage != tt.want {
				t.Fatalf("usage = %+v reported=%v, want %+v reported=%v", got.Usage, got.UsageReported, tt.want, tt.reported)
			}
		})
	}
}
