package tui_test

import (
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

func TestLaunchProviderSpecProtocol(t *testing.T) {
	t.Parallel()
	for _, protocol := range []llm.ModelProtocol{
		llm.ModelProtocols.PROTOCOLUNKNOWN,
		llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS,
		llm.ModelProtocols.PROTOCOLRESPONSES,
		llm.ModelProtocols.PROTOCOLMESSAGES,
		llm.ModelProtocols.PROTOCOLGENERATECONTENT,
	} {
		t.Run(protocol.String(), func(t *testing.T) {
			t.Parallel()
			settings := newTestSettings(t)
			selection := prefs.ModelSelection{Provider: "litellm", Model: "deployment/Production Alias"}
			if err := settings.Svc.SetModelSelection(t.Context(), prefs.ScopeWorkspace, selection); err != nil {
				t.Fatal(err)
			}
			launch := tui.Launch{Protocol: protocol}
			spec := launch.ProviderSpec(t.Context(), settings)
			if spec.Name != selection.Provider || spec.Model != selection.Model || spec.Protocol != protocol {
				t.Fatalf("launch spec = %+v", spec)
			}
			// Build uses the real engine boundary, but no request or credential
			// lookup is needed with an explicit ordinary gateway credential.
			spec.APIKey = "fixture-key"
			provider, err := engine.BuildProvider(t.Context(), settings.Registry, settings.Svc, spec)
			if err != nil {
				t.Fatal(err)
			}
			if provider.Name() != "litellm" {
				t.Fatalf("provider = %q", provider.Name())
			}
			if got := (tui.Launch{}).ProviderSpec(t.Context(), settings); got.Protocol != llm.ModelProtocols.PROTOCOLUNKNOWN {
				t.Fatalf("runtime protocol persisted: %+v", got)
			}
		})
	}
}

func TestLaunchProtocolNonLiteLLMRejected(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"openai", "llamacpp", "openai-codex", "claude-code"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			settings := newTestSettings(t)
			if err := settings.Svc.SetModelSelection(t.Context(), prefs.ScopeWorkspace, prefs.ModelSelection{Provider: name, Model: "fixture"}); err != nil {
				t.Fatal(err)
			}
			spec := (tui.Launch{Protocol: llm.ModelProtocols.PROTOCOLRESPONSES}).ProviderSpec(t.Context(), settings)
			_, err := engine.BuildProvider(t.Context(), settings.Registry, settings.Svc, spec)
			if !errors.Is(err, backends.ErrInvalidProviderDef) {
				t.Fatalf("protocol rejection = %v", err)
			}
		})
	}
}

func TestLaunchOmittedProtocolDefaults(t *testing.T) {
	t.Parallel()
	settings := newTestSettings(t)
	spec := (tui.Launch{}).ProviderSpec(t.Context(), settings)
	if spec.Name != prefs.DefaultProvider || spec.Model != prefs.DefaultModel || spec.Protocol != llm.ModelProtocols.PROTOCOLUNKNOWN {
		t.Fatalf("default launch spec = %+v", spec)
	}
}
