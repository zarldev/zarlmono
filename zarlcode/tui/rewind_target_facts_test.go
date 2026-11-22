package tui_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

func TestRewindAndExactResumeClearConstructionFacts(t *testing.T) {
	for _, operation := range []string{"rewind", "exact resume"} {
		for _, route := range []struct {
			name     string
			model    string
			registry bool
		}{
			{"same spec rebuilt", "saved-model", true},
			{"different saved model", "different-current-model", true},
			{"embedder reused", "saved-model", false},
		} {
			t.Run(operation+"/"+route.name, func(t *testing.T) {
				f := newBeforeFixture(t)
				f.provider.check = func(context.Context) {}
				settleRewindTurn(t, f, "saved prompt")
				if operation == "exact resume" {
					previewRewindPrompt(f, 1)
					_, cmd := f.ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
					driveBeforeCommand(f, cmd)
					if _, err := f.store.GetSessionBranch(t.Context(), f.ui.SessionIdentity()); err != nil {
						t.Fatal("exact child not created", err)
					}
				}
				savedID := f.ui.SessionIdentity()
				savedTarget := f.live.RunTarget()
				wantContext := []llm.Message{{Role: llm.RoleUser, Content: rewind.FilesUnchangedNotice}}
				if operation == "exact resume" {
					state, err := f.store.GetSessionResumeState(t.Context(), savedID)
					if err != nil {
						t.Fatal(err)
					}
					head, err := rewind.DecodeResume(state.Session.ContextJSON)
					if err != nil {
						t.Fatal("missing exact resume envelope", err)
					}
					wantContext = head.Context
				}

				settings, current := rewindCurrentFacts(t, f, route.model)
				if !route.registry {
					settings.Registry = nil
				}
				f.ui.SetSettings(settings)
				f.live.ApplyTarget(current)
				f.live.SetLimits(1024, 7, 3, 2)
				f.live.SetPlanMode(true)
				before := f.live.RunTarget()
				if before.Facts == nil || before.Facts.Generation == (engine.TargetGeneration{}) || before.RateCard == nil || before.Window == savedTarget.Window {
					t.Fatal("fixture lacks current construction facts, rates or a distinct saved window")
				}
				if !f.live.SupportsExactTarget(current) {
					t.Fatal("current direct provider is not qualified for exact history")
				}

				if operation == "rewind" {
					previewRewindPrompt(f, 1)
					_, cmd := f.ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
					if f.ui.SessionIdentity() == savedID {
						t.Fatalf("rewind did not activate a branch: %s", f.ui.ToastText())
					}
					driveBeforeCommand(f, cmd)
				} else if err := f.ui.ResumeSavedSession(t.Context(), savedID); err != nil {
					t.Fatal(err)
				}
				after := f.live.RunTarget()
				if (after.Provider == before.Provider) == route.registry {
					t.Fatal("rebuild must replace the provider; embedder must reuse it")
				}
				if after.Spec.Name != savedTarget.Spec.Name || after.Spec.Model != savedTarget.Spec.Model || after.Model != savedTarget.Model || after.Window != savedTarget.Window || after.Reserve != savedTarget.Reserve || after.Plan != savedTarget.Plan {
					t.Fatal("saved target policy was not restored")
				}
				if after.MaxIter != before.MaxIter || after.SpawnMaxIter != before.SpawnMaxIter || after.SpawnDepth != before.SpawnDepth || after.WebSearch != before.WebSearch {
					t.Fatal("current tool/iteration policy changed")
				}
				if !reflect.DeepEqual(f.live.ContextSnapshot(), wantContext) {
					t.Fatal("exact conversation changed during target reconstruction")
				}
				if after.Facts != nil || after.RateCard != nil {
					t.Fatalf("restored target retained old construction facts/rates: facts=%t rates=%t", after.Facts != nil, after.RateCard != nil)
				}
				if before.Facts.RequestedAlias != llm.ModelID(route.model) || before.RateCard.Reference.Revision != "current" {
					t.Fatal("restoration mutated the retained current snapshot")
				}
			})
		}
	}
}

func rewindCurrentFacts(t *testing.T, f *beforeFixture, model string) (*engine.Settings, engine.TargetUpdate) {
	t.Helper()
	settings := engine.NewSettings(f.store, nil, nil, f.ws.Root())
	defs := backends.BuiltinDefinitions()
	for i := range defs {
		if defs[i].Name == "openai" || defs[i].Name == "litellm" {
			defs[i].ContextWindow = 65536 // explicit policy, no metadata probes
		}
		if defs[i].Name == "litellm" {
			defs[i].BaseURL = "http://127.0.0.1:1"
		}
	}
	settings.Registry = backends.NewRegistry(backends.WithProviderDefinitions(defs), backends.WithSettingsService(sessionFixtureKeys{}))
	current, err := settings.PrepareTarget(t.Context(), engine.ProviderSpec{Name: "openai", Model: model})
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := settings.PrepareTarget(t.Context(), engine.ProviderSpec{Name: "litellm", Model: model})
	if err != nil {
		t.Fatal(err)
	}
	selector := engine.RateCardTarget{Provider: gateway.Spec.Name, Model: llm.ModelID(model), Protocol: gateway.Facts.SelectedProtocol}
	config, err := engine.ParseRateCardConfig(launchCardData(t, selector, "current"), time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	bound, err := engine.BindTargetRateCard(gateway, config)
	if err != nil {
		t.Fatal(err)
	}
	if f.live.SupportsExactTarget(bound) {
		t.Fatal("gateway fixture must not qualify for exact resume")
	}
	// Binding currently supports gateways only. Model an explicit host-selected
	// card on the valid direct TargetUpdate to test clearing independently of
	// that restriction. Keep the direct provider's real prepared facts; this is
	// not evidence that gateway pricing applies to OpenAI or grants exact replay.
	card := bound.RateCard.Clone()
	current.RateCard = &card
	current.Facts.Resolution.Descriptor.RateCard = card.Reference
	return settings, current
}
