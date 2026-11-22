package google_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestProviderRejectsUnsupportedMediaOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		response    string
		wantContent string
	}{
		{
			name:     "audio only",
			response: `{"candidates":[{"content":{"role":"model","parts":[{"inlineData":{"mimeType":"audio/wav","data":"YXVkaW8tY2FuYXJ5"}}]},"finishReason":"STOP"}]}`,
		},
		{
			name:        "partial text then media",
			response:    `{"candidates":[{"content":{"role":"model","parts":[{"text":"visible prefix"},{"fileData":{"mimeType":"image/png","fileUri":"gs://output-canary-secret/image.png"}}]},"finishReason":"STOP"}]}`,
			wantContent: "visible prefix",
		},
	}
	for _, route := range googleRoutes {
		t.Run(route.name, func(t *testing.T) {
			t.Parallel()
			for _, test := range tests {
				t.Run(test.name, func(t *testing.T) {
					t.Parallel()
					var requests atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						requests.Add(1)
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = w.Write([]byte("data: " + test.response + "\n\n"))
					}))
					t.Cleanup(server.Close)

					provider := newGoogleRouteProvider(t, route, server.URL)
					var content strings.Builder
					var streamErr error
					var errorsSeen int
					for chunk, err := range provider.Complete(t.Context(), llm.CompletionRequest{
						Stream:   true,
						Messages: []llm.Message{{Role: llm.RoleUser, Content: "respond"}},
					}) {
						content.WriteString(chunk.Content)
						if err != nil {
							errorsSeen++
							streamErr = err
						}
					}
					if got := content.String(); got != test.wantContent {
						t.Fatalf("content = %q, want %q", got, test.wantContent)
					}
					if errorsSeen != 1 || !errors.Is(streamErr, llm.ErrModelNotSupported) {
						t.Fatalf("errors = %d, final error = %v, want one ErrModelNotSupported", errorsSeen, streamErr)
					}
					if strings.Contains(streamErr.Error(), "audio/wav") || strings.Contains(streamErr.Error(), "output-canary-secret") {
						t.Fatalf("error exposed model media metadata: %v", streamErr)
					}
					if got := requests.Load(); got != 1 {
						t.Fatalf("HTTP requests = %d, want 1", got)
					}
				})
			}
		})
	}
}

func TestProviderPreservesControlOnlyPartsAndUsage(t *testing.T) {
	t.Parallel()

	for _, route := range googleRoutes {
		t.Run(route.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte("data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"thoughtSignature\":\"c2lnbmF0dXJl\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":2,\"candidatesTokenCount\":0,\"totalTokenCount\":2}}\n\n"))
			}))
			t.Cleanup(server.Close)

			provider := newGoogleRouteProvider(t, route, server.URL)
			var chunks []llm.CompletionChunk
			for chunk, err := range provider.Complete(t.Context(), llm.CompletionRequest{
				Stream:   true,
				Messages: []llm.Message{{Role: llm.RoleUser, Content: "respond"}},
			}) {
				if err != nil {
					t.Fatalf("Complete: %v", err)
				}
				chunks = append(chunks, chunk.Clone())
			}
			if len(chunks) != 2 {
				t.Fatalf("chunks = %#v, want native continuation and metadata chunks", chunks)
			}
			native := chunks[0]
			if native.Content != "" || native.Thinking != "" || len(native.ToolCalls) != 0 || native.UsageReported || native.FinishReason != llm.FinishReasons.UNKNOWN {
				t.Fatalf("native continuation chunk contains visible output or metadata: %#v", native)
			}
			if len(native.CompletedItems) != 1 {
				t.Fatalf("native continuation items = %#v, want one", native.CompletedItems)
			}
			item := native.CompletedItems[0]
			if item.Provider != "google" || item.Format != "generate_content.part.v2" {
				t.Fatalf("native continuation routing = %#v", item)
			}
			var part struct {
				ThoughtSignature string `json:"thoughtSignature"`
			}
			if err := json.Unmarshal(item.Data, &part); err != nil {
				t.Fatalf("decode native continuation part: %v", err)
			}
			if part.ThoughtSignature != "c2lnbmF0dXJl" {
				t.Fatalf("native thought signature = %q, want c2lnbmF0dXJl", part.ThoughtSignature)
			}
			metadata := chunks[1]
			if len(metadata.CompletedItems) != 0 || metadata.FinishReason != llm.FinishReasons.STOP || !metadata.UsageReported || metadata.Usage.PromptTokens != 2 || metadata.Usage.CompletionTokens != 0 || metadata.Usage.TotalTokens != 2 {
				t.Fatalf("metadata chunk = %#v", metadata)
			}
		})
	}
}
