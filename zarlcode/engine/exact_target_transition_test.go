package engine_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestExactLiveTargetSwitchPreservesCompatibleHistory(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		source   string
		model    string
		native   llm.ContinuationItem
		history  []llm.Message
		next     engine.ProviderSpec
		provider llm.Provider
		err      error
	}{
		{name: "same provider model", source: "openai", model: "saved-model", next: engine.ProviderSpec{Name: "openai", Model: "other-model"}},
		{name: "same model budget change", source: "openai", model: "saved-model", next: engine.ProviderSpec{Name: "openai", Model: "saved-model"}},
		{name: "portable provider switch", source: "openai", model: "saved-model", next: engine.ProviderSpec{Name: "anthropic", Model: "claude-sonnet-4-6"}},
		{name: "Codex native model switch", source: "openai-codex", model: "gpt-6-astra", next: engine.ProviderSpec{Name: "openai-codex", Model: "gpt-6-luna"},
			native: llm.ContinuationItem{Provider: "openai-codex", Format: "responses.reasoning.v1", Kind: "reasoning", Data: []byte(`{"type":"reasoning","id":"reason","encrypted_content":"opaque"}`)}},
		{name: "Anthropic native model switch", source: "anthropic", model: "claude-sonnet-4-6", next: engine.ProviderSpec{Name: "anthropic", Model: "claude-opus-4-6"},
			native: llm.ContinuationItem{Provider: "anthropic", Format: "content_block.v1", Kind: "thinking", Data: []byte(`{"type":"thinking","thinking":"retained reasoning","signature":"opaque"}`)}},
		{name: "OpenAI native model switch", source: "openai", model: "gpt-5.6", next: engine.ProviderSpec{Name: "openai", Model: "gpt-6-luna"},
			provider: openai.NewProvider("test-key", openai.WithModel("gpt-6-luna")),
			native:   llm.ContinuationItem{Provider: "openai", Format: "responses.output_item", Kind: "reasoning", ID: "reason", Data: []byte(`{"type":"reasoning","id":"reason","encrypted_content":"opaque"}`)}},
		{name: "native provider mismatch", source: "openai-codex", model: "gpt-6-astra", next: engine.ProviderSpec{Name: "anthropic", Model: "claude-sonnet-4-6"}, err: rewind.ErrTarget,
			native: llm.ContinuationItem{Provider: "openai-codex", Format: "responses.reasoning.v1", Kind: "reasoning", Data: []byte(`{"type":"reasoning","id":"reason","encrypted_content":"opaque"}`)}},
		{name: "Responses to Chat loses native items", source: "openai", model: "gpt-5.6", next: engine.ProviderSpec{Name: "openai", Model: "gpt-4o"}, err: rewind.ErrTarget,
			provider: openai.NewProvider("test-key", openai.WithModel("gpt-4o")),
			native:   llm.ContinuationItem{Provider: "openai", Format: "responses.output_item", Kind: "reasoning", ID: "reason", Data: []byte(`{"type":"reasoning","id":"reason","encrypted_content":"opaque"}`)}},
		{name: "portable Responses to Chat", source: "openai", model: "gpt-5.6", next: engine.ProviderSpec{Name: "openai", Model: "gpt-4o"},
			provider: openai.NewProvider("test-key", openai.WithModel("gpt-4o"))},
		{name: "Responses content position to Chat", source: "openai", model: "gpt-5.6", next: engine.ProviderSpec{Name: "openai", Model: "gpt-4o"}, err: rewind.ErrTarget,
			provider: openai.NewProvider("test-key", openai.WithModel("gpt-4o")),
			history:  []llm.Message{{Role: llm.RoleAssistant, Content: "retained answer", ContentOutputIndex: new(0)}}},
		{name: "Responses tool position to Chat", source: "openai", model: "gpt-5.6", next: engine.ProviderSpec{Name: "openai", Model: "gpt-4o"}, err: rewind.ErrTarget,
			provider: openai.NewProvider("test-key", openai.WithModel("gpt-4o")),
			history: []llm.Message{
				{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call", Type: "function", Function: llm.ToolCallFunction{Name: "read", Arguments: `{}`}, OutputIndex: new(0)}}},
				{Role: llm.RoleTool, ToolCallID: "call", Content: "retained result"},
			}},
		{name: "unsupported route", source: "openai", model: "saved-model", next: engine.ProviderSpec{Name: "custom", Model: "other-model"}, err: rewind.ErrTarget},
		{name: "custom endpoint", source: "openai", model: "saved-model", next: engine.ProviderSpec{Name: "openai", Model: "other-model", BaseURL: "https://custom.invalid"}, err: rewind.ErrTarget},
		{name: "Responses disabled", source: "openai", model: "saved-model", next: engine.ProviderSpec{Name: "openai", Model: "gpt-5.6"}, err: rewind.ErrTarget,
			provider: openai.NewProvider("test-key", openai.WithModel("gpt-5.6"), openai.WithResponsesAPI(false))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var provider llm.Provider = &requestRecordingProvider{}
			if tc.source == "openai" && openai.SupportsResponsesReplay(tc.model) {
				provider = openai.NewProvider("test-key", openai.WithModel(tc.model))
			}
			live := reservationRunner(t, provider)
			live.SetProviderSpec(provider, engine.ProviderSpec{Name: tc.source, Model: tc.model})
			before := live.RunTarget()
			store, err := db.Open(t.Context(), t.TempDir()+"/settings.db")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			settings := engine.NewSettings(store, nil, nil, t.TempDir())
			selection := prefs.ModelSelection{Provider: tc.source, Model: tc.model}
			if err := settings.Svc.SetModelSelection(t.Context(), prefs.ScopeWorkspace, selection); err != nil {
				t.Fatal(err)
			}
			reservation, err := live.ReserveRuntime()
			if err != nil {
				t.Fatal(err)
			}
			defer reservation.Release()
			_, saved, err := reservation.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			history := []llm.Message{{Role: llm.RoleUser, Content: "retained prompt"}, {Role: llm.RoleAssistant, Content: "retained answer"}}
			if tc.native.Provider != "" {
				native := tc.native
				native.OutputIndex = new(0)
				history[1].ContinuationItems = []llm.ContinuationItem{native}
				history[1].ContentOutputIndex = new(1)
			}
			if tc.history != nil {
				history = llm.CloneMessages(tc.history)
			}
			if err := reservation.RestoreConversation(history, saved, code.Plan{}, before); err != nil {
				t.Fatal(err)
			}
			nextProvider := tc.provider
			if nextProvider == nil {
				nextProvider = &requestRecordingProvider{}
			}
			nextSelection := prefs.ModelSelection{Provider: tc.next.Name, Model: tc.next.Model}
			err = reservation.ApplyTargetSelection(t.Context(), engine.TargetUpdate{Provider: nextProvider, Spec: tc.next, Window: 128000}, settings.Svc, nextSelection)
			if !errors.Is(err, tc.err) {
				t.Fatalf("switch = %v, want %v", err, tc.err)
			}
			if !reflect.DeepEqual(live.ContextSnapshot(), history) {
				t.Fatal("switch changed historical messages/native bytes")
			}
			wantSelection := selection
			if tc.err == nil {
				wantSelection = nextSelection
			}
			for key, want := range map[string]string{prefs.KeyProvider: wantSelection.Provider, prefs.KeyModel: wantSelection.Model} {
				got, err := settings.Svc.GetSetting(t.Context(), prefs.ScopeWorkspace, key)
				if err != nil || got.Value != want {
					t.Fatalf("saved %s = %q, %v, want %q", key, got.Value, err, want)
				}
			}
			if tc.err != nil {
				if !reflect.DeepEqual(live.RunTarget(), before) {
					t.Fatal("rejected switch changed target")
				}
				return
			}
			got := live.RunTarget()
			if got.Provider != nextProvider || got.Spec != tc.next || got.Model != tc.next.Model || got.Window != 128000 {
				t.Fatalf("switch did not publish complete target: %+v", got.Spec)
			}
			// Snapshot uses the new target; restoring the old snapshot still requires its old target.
			context, target, err := reservation.Snapshot()
			if err != nil || target.Provider != tc.next.Name || target.Model != tc.next.Model || !reflect.DeepEqual(context, history) {
				t.Fatalf("new checkpoint snapshot disagrees with live target: %+v, %v", target, err)
			}
			if _, err := rewind.EncodeResume(1, context, target, "turn", 1, rewind.InitialContinuation{}); err != nil {
				t.Fatalf("admitted target cannot persist retained context: %v", err)
			}
			if err := reservation.RestoreConversation(history, saved, code.Plan{}, got); !errors.Is(err, rewind.ErrTarget) {
				t.Fatalf("saved restore accepted a different target: %v", err)
			}
			// Protection survives a successful switch rather than silently becoming legacy.
			if err := reservation.ApplyTarget(engine.TargetUpdate{Provider: nextProvider, Spec: engine.ProviderSpec{Name: "custom", Model: "other"}}); !errors.Is(err, rewind.ErrTarget) {
				t.Fatalf("switch lost exact protection: %v", err)
			}
		})
	}
}

func TestExactCodexModelSwitchContinuesWithNativeHistory(t *testing.T) {
	// These subtests switch the same conversation in sequence.
	provider := &requestRecordingProvider{}
	live := reservationRunner(t, provider)
	live.SetProviderSpec(provider, engine.ProviderSpec{Name: "openai-codex", Model: "gpt-6-astra"})
	r, err := live.ReserveRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Release()
	_, saved, err := r.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "prior prompt"},
		{Role: llm.RoleAssistant, Content: "prior answer", ContentOutputIndex: llm.OutputPosition(1), ContinuationItems: []llm.ContinuationItem{{Provider: "openai-codex", Format: "responses.reasoning.v1", Kind: "reasoning", OutputIndex: llm.OutputPosition(0), Data: []byte(`{"type":"reasoning","id":"reason","encrypted_content":"opaque"}`)}}},
	}
	if err := r.RestoreConversation(messages, saved, code.Plan{}, live.RunTarget()); err != nil {
		t.Fatal(err)
	}
	r.Release()
	for index, model := range []string{"gpt-6-sol", "gpt-6-astra", "gpt-6.1-sol", "gpt-6-astra"} {
		t.Run(model, func(t *testing.T) {
			reservation, err := live.ReserveRuntime()
			if err != nil {
				t.Fatal(err)
			}
			defer reservation.Release()
			if err := reservation.ApplyTarget(engine.TargetUpdate{Provider: provider, Spec: engine.ProviderSpec{Name: "openai-codex", Model: model}, Window: 100000}); err != nil {
				t.Fatal(err)
			}
			if got := live.RunTarget().Model; got != model {
				t.Fatalf("target model = %q, want %q", got, model)
			}
			reservation.Release()
			if err := live.RunTurn(t.Context(), "continue on new model"); err != nil {
				t.Fatal(err)
			}
			if len(provider.requests) != index+1 {
				t.Fatalf("turns dispatched %d requests, want %d", len(provider.requests), index+1)
			}
			for _, message := range provider.requests[index].Messages {
				if message.Role == llm.RoleAssistant && message.Content == "prior answer" {
					if !reflect.DeepEqual(message, messages[1]) {
						t.Fatal("next request changed native history")
					}
					return
				}
			}
			t.Fatal("next request omitted retained answer")
		})
	}
}
