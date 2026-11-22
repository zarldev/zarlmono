package engine_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/llm/litellm"
)

func preparedGatewaySettings(url string) *engine.Settings {
	def, _ := backends.Builtin("litellm")
	def.Name, def.Builtin, def.BaseURL = "gateway-route", false, url
	def.DefaultModel = "claude/gpt-Gemini Production Alias"
	return &engine.Settings{Registry: backends.NewRegistry(backends.WithProviderDefinitions([]backends.ProviderDefinition{def}))}
}

func TestPrepareTargetDefaultAliasAndActualProtocol(t *testing.T) {
	t.Parallel()
	for _, protocol := range []llm.ModelProtocol{
		llm.ModelProtocols.PROTOCOLUNKNOWN, llm.ModelProtocols.PROTOCOLRESPONSES,
		llm.ModelProtocols.PROTOCOLMESSAGES, llm.ModelProtocols.PROTOCOLGENERATECONTENT,
	} {
		t.Run(protocol.String(), func(t *testing.T) {
			t.Parallel()
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Path != "/v1/chat/completions" {
					t.Errorf("completion path = %s", r.URL.Path)
				}
				var body struct {
					Model string `json:"model"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body.Model != "claude/gpt-Gemini Production Alias" {
					t.Errorf("wire alias = %q", body.Model)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"c1","choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`)
			}))
			t.Cleanup(server.Close)
			settings := preparedGatewaySettings(server.URL)
			update, err := settings.PrepareTarget(t.Context(), engine.ProviderSpec{Name: "gateway-route", APIKey: "test-key", Protocol: protocol})
			if err != nil {
				t.Fatal(err)
			}
			if requests.Load() != 0 {
				t.Fatal("preparation fetched optional enrichment")
			}
			facts := update.Facts
			if facts == nil || update.Spec.Model != "claude/gpt-Gemini Production Alias" || string(facts.RequestedAlias) != update.Spec.Model {
				t.Fatalf("default alias = %+v", update)
			}
			if update.Spec.Protocol != protocol {
				t.Fatal("selected protocol replaced override intent")
			}
			inner := update.Provider.(interface{ Unwrap() llm.Provider }).Unwrap().(*litellm.Provider)
			if facts.SelectedProtocol != inner.Protocol() {
				t.Fatal("facts do not match actual built codec")
			}
			if protocol == llm.ModelProtocols.PROTOCOLUNKNOWN && facts.SelectedProtocol != llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS {
				t.Fatal("alias text inferred a native protocol")
			}
			if facts.Resolution.Descriptor.CanonicalModel != "" || facts.Resolution.Descriptor.Deployment != "" || facts.Resolution.Descriptor.RateCard != (llm.RateCardReference{}) || len(facts.Resolution.Issues) == 0 {
				t.Fatal("missing identity/binding became complete")
			}
			if facts.Budget.Tokens != update.Window || facts.Budget.Source != engine.OperationalBudgetSources.BUDGETAPPLICATIONFALLBACK {
				t.Fatalf("budget = %+v", facts.Budget)
			}
			if protocol == llm.ModelProtocols.PROTOCOLUNKNOWN {
				for _, err := range update.Provider.Complete(t.Context(), llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "hello"}}}) {
					if err != nil {
						t.Fatal(err)
					}
				}
			}
		})
	}
}

func TestPrepareTargetCachedPreservesProbeFreeStartup(t *testing.T) {
	t.Parallel()
	var probes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probes.Add(1)
		if r.URL.Path != "/props" {
			t.Errorf("probe path = %s", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"default_generation_settings":{"n_ctx":43210}}`)
	}))
	t.Cleanup(server.Close)
	settings := &engine.Settings{Registry: backends.NewRegistry()}
	spec := engine.ProviderSpec{Name: "llamacpp", Model: "local-alias", BaseURL: server.URL + "/v1"}
	cached, err := settings.PrepareTargetCached(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if probes.Load() != 0 || cached.Window != engine.LiveContextWindow || cached.Facts.Budget.Source != engine.OperationalBudgetSources.BUDGETAPPLICATIONFALLBACK {
		t.Fatal("cached preparation probed or claimed a resolved budget")
	}
	full, err := settings.PrepareTarget(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if probes.Load() != 1 || full.Window != 43210 || full.Facts.Budget.Source != engine.OperationalBudgetSources.BUDGETOPAQUELEGACYRESOLUTION {
		t.Fatalf("full resolution = %d, %+v", probes.Load(), full.Facts.Budget)
	}
	if full.Facts.SelectedProtocol != llm.ModelProtocols.PROTOCOLUNKNOWN {
		t.Fatal("provider name granted unproven codec knowledge")
	}
	if _, err := full.Facts.CachedModelInfo(); !errors.Is(err, engine.ErrTargetModelInfoUnavailable) {
		t.Fatal(err)
	}
}

func TestPrepareTargetBudgetSourcesAndFailure(t *testing.T) {
	t.Parallel()
	def, _ := backends.Builtin("litellm")
	def.ContextWindow = 12345
	settings := &engine.Settings{Registry: backends.NewRegistry(backends.WithProviderDefinitions([]backends.ProviderDefinition{def}))}
	update, err := settings.PrepareTargetCached(t.Context(), engine.ProviderSpec{Name: "litellm", APIKey: "test-key", Model: "alias"})
	if err != nil {
		t.Fatal(err)
	}
	if update.Window != 12345 || update.Facts.Budget.Source != engine.OperationalBudgetSources.BUDGETEXPLICITCONFIGURATION {
		t.Fatal(update.Facts.Budget)
	}
	settings = &engine.Settings{Registry: backends.NewRegistry()}
	static, err := settings.PrepareTargetCached(t.Context(), engine.ProviderSpec{Name: "openai", APIKey: "test-key", Model: "gpt-4o"})
	if err != nil {
		t.Fatal(err)
	}
	if static.Window != settings.Registry.ResolveContextWindowCached("openai", "gpt-4o") || static.Facts.Budget.Source != engine.OperationalBudgetSources.BUDGETOPAQUELEGACYRESOLUTION {
		t.Fatal(static.Facts.Budget)
	}
	bad, err := settings.PrepareTargetCached(t.Context(), engine.ProviderSpec{Name: "openai", APIKey: "test-key", Protocol: llm.ModelProtocols.PROTOCOLRESPONSES})
	if !errors.Is(err, backends.ErrInvalidProviderDef) || bad.Facts != nil || bad.Provider != nil {
		t.Fatalf("failed construction = %+v, %v", bad, err)
	}
}

func TestPrepareTargetAlwaysCreatesNewScope(t *testing.T) {
	t.Parallel()
	settings := preparedGatewaySettings("http://127.0.0.1:1")
	spec := engine.ProviderSpec{Name: "gateway-route", Model: "same-alias", APIKey: "first-test-key"}
	seen := make(map[engine.TargetGeneration]struct{})
	for i := range 5 {
		if i == 2 {
			spec.APIKey = "second-test-key"
		}
		if i == 3 {
			spec.Protocol = llm.ModelProtocols.PROTOCOLMESSAGES
		}
		if i == 4 {
			spec.BaseURL = "http://127.0.0.1:2"
		}
		update, err := settings.PrepareTargetCached(t.Context(), spec)
		if err != nil {
			t.Fatal(err)
		}
		generation := update.Facts.Generation
		if generation == (engine.TargetGeneration{}) {
			t.Fatal("absent generation")
		}
		if _, ok := seen[generation]; ok {
			t.Fatal("reused construction scope")
		}
		seen[generation] = struct{}{}
		if _, err := update.Facts.CachedModelInfo(); !errors.Is(err, litellm.ErrModelInfoNotCached) {
			t.Fatalf("new scope cache = %v", err)
		}
	}
}
