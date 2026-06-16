package taskrunner_test

import (
	"context"
	"testing"

	znotify "github.com/zarldev/zarlmono/zkit/znotify"

	"github.com/zarldev/zarlmono/zarlai/service"
	"github.com/zarldev/zarlmono/zarlai/taskrunner"
	"github.com/zarldev/zarlmono/zarlai/taskrunner/taskrunnertest"
	"github.com/zarldev/zarlmono/zkit/agent/profile"
	agentrunner "github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
)

// The conversation lock is the shared runner.ConversationLock; its
// behaviour is covered in zkit/agent/runner/conversation_lock_test.go.

// stubChatClient satisfies service.ChatClient for Runner construction tests.
type stubChatClient struct{}

func (s stubChatClient) Chat(_ context.Context, _ []service.Message, _ []llm.Tool) (service.ChatResult, error) {
	return service.ChatResult{}, nil
}

func TestNewRunner_accepts_profile_registry_and_factory(t *testing.T) {
	reg := taskrunnertest.NewFakeProfileRegistry()
	reg.ByName[profile.NameDefault] = taskrunner.ResolvedProfile{
		Resolved: profile.Resolved{Name: profile.NameDefault, MaxIterations: 5},
		Tools:    []tools.Tool{},
	}

	factory := &taskrunnertest.FakeChatClientFactory{Client: stubChatClient{}}

	runner := taskrunner.NewRunner(
		taskrunner.Config{
			Notifications: znotify.NewNotificationStore(),
			ConvLock:      agentrunner.NewConversationLock(),
		},
		taskrunner.WithChatClient(stubChatClient{}),
		taskrunner.WithRegistry(tools.NewRegistry()),
		taskrunner.WithContextBudget(8192),
		taskrunner.WithProfiles(reg),
		taskrunner.WithChatFactory(factory.Build),
	)
	if runner == nil {
		t.Fatal("NewRunner returned nil")
	}
}

func TestChatClientFactory_records_model(t *testing.T) {
	factory := &taskrunnertest.FakeChatClientFactory{Client: stubChatClient{}}

	// Simulate what executeTask does when it picks up a profile model.
	model := "gemma4:31b"
	got := factory.Build(model)
	if got == nil {
		t.Fatal("factory.Build returned nil client")
	}
	if len(factory.Calls) != 1 || factory.Calls[0] != model {
		t.Errorf("factory.Calls = %v, want [%q]", factory.Calls, model)
	}
}

func TestFakeProfileRegistry_resolves_named_profile(t *testing.T) {
	reg := taskrunnertest.NewFakeProfileRegistry()
	reg.ByName[profile.NameResearcher] = taskrunner.ResolvedProfile{
		Resolved: profile.Resolved{
			Name:          profile.NameResearcher,
			Model:         "gemma4:31b",
			PromptPrefix:  "you are a researcher",
			MaxIterations: 1,
		},
		Tools: []tools.Tool{},
	}
	reg.ByName[profile.NameDefault] = taskrunner.ResolvedProfile{
		Resolved: profile.Resolved{Name: profile.NameDefault, MaxIterations: 5},
		Tools:    []tools.Tool{},
	}

	resolved, err := reg.Resolve(t.Context(), profile.NameResearcher)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Model != "gemma4:31b" {
		t.Errorf("Model = %q, want %q", resolved.Model, "gemma4:31b")
	}
	if resolved.PromptPrefix != "you are a researcher" {
		t.Errorf("PromptPrefix = %q, want %q", resolved.PromptPrefix, "you are a researcher")
	}
	if resolved.MaxIterations != 1 {
		t.Errorf("MaxIterations = %d, want 1", resolved.MaxIterations)
	}
}
