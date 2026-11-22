package openai_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/litellm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
)

func TestChatNonstreamNullResponse(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		provider func(string) llm.Provider
		wantErr  error
	}{
		{
			name: "openai",
			provider: func(baseURL string) llm.Provider {
				return openai.NewProvider("test", openai.WithBaseURL(baseURL+"/v1"), openai.WithResponsesAPI(false))
			},
		},
		{
			name: "litellm",
			provider: func(baseURL string) llm.Provider {
				return litellm.NewProvider("test", litellm.WithBaseURL(baseURL), litellm.WithModel("gateway-alias"), litellm.WithProtocol(llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS))
			},
			wantErr: litellm.ErrCompletion,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Path != "/v1/chat/completions" {
					t.Errorf("request path = %q, want Chat Completions", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, "null")
			}))
			t.Cleanup(server.Close)

			stream := test.provider(server.URL).Complete(t.Context(), llm.CompletionRequest{
				Messages: []llm.Message{{Role: llm.RoleUser, Content: "hello"}},
			})
			if got := requests.Load(); got != 0 {
				t.Fatalf("requests before iteration = %d, want 0", got)
			}

			var yields int
			for chunk, err := range stream {
				yields++
				if err == nil {
					t.Fatalf("null response yielded success: %+v", chunk)
				}
				if test.wantErr != nil && !errors.Is(err, test.wantErr) {
					t.Errorf("error = %v, want %v", err, test.wantErr)
				}
				if !reflect.DeepEqual(llm.CompletionChunk{}, chunk) {
					t.Errorf("terminal chunk = %+v, want zero chunk", chunk)
				}
			}
			if yields != 1 {
				t.Errorf("yields = %d, want exactly one terminal error", yields)
			}
			if got := requests.Load(); got != 1 {
				t.Errorf("requests = %d, want 1", got)
			}
		})
	}
}
