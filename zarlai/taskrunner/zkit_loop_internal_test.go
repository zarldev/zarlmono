package taskrunner

import (
	"context"
	"iter"
	"testing"

	"github.com/zarldev/zarlmono/zarlai/service"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// The four built-in chat clients must expose their provider so the zkit-backed
// task loop can reach the streaming llm.Provider. Compile-time contract.
var (
	_ service.ProviderAware = (*service.AnthropicClient)(nil)
	_ service.ProviderAware = (*service.OpenAIClient)(nil)
	_ service.ProviderAware = (*service.OllamaClient)(nil)
	_ service.ProviderAware = (*service.LlamaCppClient)(nil)
)

type fakeProvider struct{ name string }

func (f fakeProvider) Complete(context.Context, llm.CompletionRequest) (iter.Seq2[llm.CompletionChunk, error], error) {
	return func(func(llm.CompletionChunk, error) bool) {}, nil
}
func (f fakeProvider) Name() string { return f.name }

// providerChat is a ChatClient that exposes its provider (service.ProviderAware).
type providerChat struct{ p llm.Provider }

func (providerChat) Chat(context.Context, []service.Message, []llm.Tool) (service.ChatResult, error) {
	return service.ChatResult{}, nil
}
func (c providerChat) Provider() llm.Provider { return c.p }

// bareChat is a ChatClient with no provider accessor.
type bareChat struct{}

func (bareChat) Chat(context.Context, []service.Message, []llm.Tool) (service.ChatResult, error) {
	return service.ChatResult{}, nil
}

func TestPickProvider_unwrapsProviderAwareClient(t *testing.T) {
	want := fakeProvider{name: "fake"}
	r := NewRunner(Config{}, WithChatFactory(func(string) service.ChatClient {
		return providerChat{p: want}
	}))
	got, ok := r.pickProvider("some-model")
	if !ok {
		t.Fatal("pickProvider ok=false, want true for a ProviderAware client")
	}
	if got.Name() != "fake" {
		t.Fatalf("provider = %q, want %q", got.Name(), "fake")
	}
}

func TestPickProvider_falseForBareChatClient(t *testing.T) {
	r := NewRunner(Config{}, WithChatFactory(func(string) service.ChatClient {
		return bareChat{}
	}))
	if _, ok := r.pickProvider("some-model"); ok {
		t.Fatal("pickProvider ok=true for a bare ChatClient, want false (caller falls back to legacy loop)")
	}
}
