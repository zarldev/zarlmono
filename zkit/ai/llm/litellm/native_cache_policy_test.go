package litellm_test

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/litellm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
)

type nativePolicyRejectTransport struct{ calls atomic.Int64 }

func (transport *nativePolicyRejectTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	transport.calls.Add(1)
	return nil, errors.New("unexpected native policy HTTP")
}

func TestNativeCachePolicyRejectsEveryGatewayProtocol(t *testing.T) {
	for protocol := range llm.ModelProtocols.All() {
		if protocol == llm.ModelProtocols.PROTOCOLUNKNOWN {
			continue
		}
		t.Run(protocol.String(), func(t *testing.T) {
			transport := &nativePolicyRejectTransport{}
			provider := litellm.NewProvider("test", litellm.WithHTTPClient(&http.Client{Transport: transport}), litellm.WithProtocol(protocol), litellm.WithModel("opaque-canary"))
			for _, value := range []any{nil, "secret-canary", map[string]any{"mode": "explicit"}} {
				req := llm.CompletionRequest{Options: llm.ModelOptions{openai.PromptCacheOption: value}}
				stream := provider.Complete(t.Context(), req)
				if transport.calls.Load() != 0 {
					t.Fatal("Complete is not lazy")
				}
				yields := 0
				for chunk, err := range stream {
					yields++
					if !errors.Is(err, litellm.ErrCompletion) || !reflect.DeepEqual(chunk, llm.CompletionChunk{}) {
						t.Fatalf("chunk=%#v, err=%v", chunk, err)
					}
					if strings.Contains(err.Error(), "canary") {
						t.Fatalf("unsafe error: %v", err)
					}
				}
				if yields != 1 || transport.calls.Load() != 0 {
					t.Errorf("yields/HTTP=%d/%d", yields, transport.calls.Load())
				}
			}
			cause := errors.New("caller stopped")
			ctx, cancel := context.WithCancelCause(t.Context())
			cancel(cause)
			for chunk, err := range provider.Complete(ctx, llm.CompletionRequest{Options: llm.ModelOptions{openai.PromptCacheOption: nil}}) {
				if !errors.Is(err, cause) || !reflect.DeepEqual(chunk, llm.CompletionChunk{}) {
					t.Fatalf("cancellation precedence: chunk=%#v, err=%v", chunk, err)
				}
			}
			if transport.calls.Load() != 0 {
				t.Fatal("canceled gateway policy reached HTTP")
			}
		})
	}
}
