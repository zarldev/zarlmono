package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/db"
)

func targetCardConfig(t *testing.T, target engine.RateCardTarget, revision string) engine.RateCardConfig {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"version": 1,
		"target":  map[string]any{"provider": target.Provider, "model": target.Model, "base_url": target.BaseURL, "protocol": target.Protocol.String()},
		"card": map[string]any{
			"id": "local-estimate-policy", "revision": revision, "entry": "entry",
			"pricing_provider": "operator", "currency": "EUR", "billing": "token-metered",
			"applicable": true, "token_only": true, "fresh_input_per_million": 1,
			"cache_writes": []any{map[string]any{"lifetime_seconds": 300, "per_million": 2.5}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	config, err := engine.ParseRateCardConfig(data, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	return config
}

func preparedCard(t *testing.T) (engine.TargetUpdate, engine.RateCardConfig) {
	t.Helper()
	settings := preparedGatewaySettings("http://127.0.0.1:1/registered")
	update, err := settings.PrepareTargetCached(t.Context(), engine.ProviderSpec{Name: "gateway-route", APIKey: "local-test-credential"})
	if err != nil {
		t.Fatal(err)
	}
	config := targetCardConfig(t, engine.RateCardTarget{
		Provider: update.Spec.Name, Model: llm.ModelID(update.Spec.Model), Protocol: llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS,
	}, "v1")
	bound, err := engine.BindTargetRateCard(update, config)
	if err != nil {
		t.Fatal(err)
	}
	return bound, config
}

func TestBindTargetRateCardActualPreparation(t *testing.T) {
	t.Parallel()
	update, config := preparedCard(t)
	if update.Spec.Protocol != llm.ModelProtocols.PROTOCOLUNKNOWN || update.Spec.BaseURL != "" || update.Spec.APIKey != "local-test-credential" {
		t.Fatal("binding changed runtime overrides or private credential")
	}
	if update.Facts.RequestedAlias != "claude/gpt-Gemini Production Alias" || update.Facts.SelectedProtocol != llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS || update.Facts.Resolution.Descriptor.RateCard != update.RateCard.Reference {
		t.Fatal("binding did not use the configured default alias and actual codec")
	}
	want := update.RateCard.Clone()
	update.RateCard.CacheWrites[0].Rate.PerMillionTokens = 99
	again, err := engine.BindTargetRateCard(update, config)
	if err != nil || !reflect.DeepEqual(again.RateCard, &want) {
		t.Fatal("binding retained caller rates or changed parsed config", err)
	}
	again.Facts.Resolution.Descriptor.RateCard.Revision = "changed"
	if update.Facts.Resolution.Descriptor.RateCard.Revision != "v1" {
		t.Fatal("binding borrowed facts")
	}

	settings := preparedGatewaySettings("http://127.0.0.1:2/registered")
	spec := engine.ProviderSpec{Name: "gateway-route", Model: " opaque Alias ", BaseURL: "http://127.0.0.1:3/Override/", APIKey: "test", Protocol: llm.ModelProtocols.PROTOCOLMESSAGES}
	prepared, err := settings.PrepareTarget(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	selected := targetCardConfig(t, engine.RateCardTarget{Provider: spec.Name, Model: llm.ModelID(spec.Model), BaseURL: spec.BaseURL, Protocol: llm.ModelProtocols.PROTOCOLMESSAGES}, "override")
	bound, err := engine.BindTargetRateCard(prepared, selected)
	if err != nil || bound.RateCard == nil || prepared.RateCard != nil || prepared.Facts.Resolution.Descriptor.RateCard != (llm.RateCardReference{}) {
		t.Fatal("exact override failed or mutated preparation", err)
	}
}

func TestBindTargetRateCardScopeMismatch(t *testing.T) {
	t.Parallel()
	bound, config := preparedCard(t)
	other, _ := preparedCard(t)
	cases := []struct {
		name   string
		change func(*engine.TargetUpdate)
	}{
		{"nil facts", func(u *engine.TargetUpdate) { u.Facts = nil }},
		{"legacy facts", func(u *engine.TargetUpdate) { u.Facts = &engine.TargetFacts{} }},
		{"provider id", func(u *engine.TargetUpdate) { u.Spec.Name = "other" }},
		{"model", func(u *engine.TargetUpdate) { u.Spec.Model = "other" }},
		{"URL", func(u *engine.TargetUpdate) { u.Spec.BaseURL = "http://127.0.0.1:1/registered" }},
		{"provider construction", func(u *engine.TargetUpdate) { u.Provider = other.Provider }},
		{"missing provider", func(u *engine.TargetUpdate) { u.Provider = nil }},
		{"facts alias", func(u *engine.TargetUpdate) { u.Facts.RequestedAlias = "other" }},
		{"facts protocol", func(u *engine.TargetUpdate) { u.Facts.SelectedProtocol = llm.ModelProtocols.PROTOCOLMESSAGES }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			update := bound
			facts := bound.Facts.Clone()
			update.Facts = &facts
			tc.change(&update)
			got, err := engine.BindTargetRateCard(update, config)
			if !errors.Is(err, engine.ErrRateCardTargetMismatch) || got.RateCard != nil || got.Provider != nil {
				t.Fatalf("mismatch = %+v, %v", got, err)
			}
		})
	}
	for _, change := range []func(*engine.RateCardTarget){
		func(v *engine.RateCardTarget) { v.Provider = "litellm" },
		func(v *engine.RateCardTarget) { v.Model = "canonical/model" },
		func(v *engine.RateCardTarget) { v.BaseURL = "http://127.0.0.1:1/registered" },
		func(v *engine.RateCardTarget) { v.Protocol = llm.ModelProtocols.PROTOCOLRESPONSES },
	} {
		target := engine.RateCardTarget{Provider: bound.Spec.Name, Model: bound.Facts.RequestedAlias, Protocol: bound.Facts.SelectedProtocol}
		change(&target)
		if _, err := engine.BindTargetRateCard(bound, targetCardConfig(t, target, "other")); !errors.Is(err, engine.ErrRateCardTargetMismatch) {
			t.Fatal("selected config ignored exact target", err)
		}
	}
	settings := &engine.Settings{Registry: backends.NewRegistry()}
	unknown, err := settings.PrepareTargetCached(t.Context(), engine.ProviderSpec{Name: "openai", Model: "gpt-4o", APIKey: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.BindTargetRateCard(unknown, config); !errors.Is(err, engine.ErrRateCardTargetMismatch) {
		t.Fatal("unknown actual codec invented proof", err)
	}
}

func TestTargetRateCardPublicationCloneAndClearing(t *testing.T) {
	t.Parallel()
	for _, reserved := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "reserved"}[reserved], func(t *testing.T) {
			t.Parallel()
			bound, _ := preparedCard(t)
			live := reservationRunner(t, bound.Provider)
			if reserved {
				r, err := live.ReserveRuntime()
				if err != nil {
					t.Fatal(err)
				}
				if err := r.ApplyTarget(bound); err != nil {
					r.Release()
					t.Fatal(err)
				}
				r.Release()
			} else {
				live.ApplyTarget(bound)
			}
			want := live.RunTarget()
			bound.RateCard.CacheWrites[0].Rate.PerMillionTokens = 500
			bound.RateCard.Reference.Revision = "caller"
			capture := live.RunTarget()
			clone := capture.Clone()
			capture.RateCard.CacheWrites[0].Rate.PerMillionTokens = 600
			clone.RateCard.CacheWrites[0].Rate.PerMillionTokens = 700
			if !reflect.DeepEqual(live.RunTarget(), want) {
				t.Fatal("card aliased publication or snapshot")
			}
			bound.RateCard = nil // Even retained bound facts cannot keep an old reference.
			live.ApplyTarget(bound)
			if got := live.RunTarget(); got.RateCard != nil || got.Facts.Resolution.Descriptor.RateCard != (llm.RateCardReference{}) {
				t.Fatal("absent card inherited basis")
			}
		})
	}
	cases := []struct {
		name   string
		change func(*engine.LiveRunner)
	}{
		{"provider", func(l *engine.LiveRunner) { l.SetProvider(&requestRecordingProvider{}, "other") }},
		{"spec", func(l *engine.LiveRunner) {
			l.SetProviderSpec(&requestRecordingProvider{}, engine.ProviderSpec{Name: "openai", Model: "other"})
		}},
		{"model", func(l *engine.LiveRunner) { l.SetModel("other") }},
		{"window", func(l *engine.LiveRunner) { l.SetContextWindow(123) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			bound, _ := preparedCard(t)
			live := reservationRunner(t, bound.Provider)
			live.ApplyTarget(bound)
			tc.change(live)
			if live.RunTarget().RateCard != nil {
				t.Fatal("identity/window setter retained unproven basis")
			}
		})
	}
	bound, _ := preparedCard(t)
	settings := preparedGatewaySettings("http://127.0.0.1:1/registered")
	fresh, err := settings.PrepareTargetCached(t.Context(), bound.Spec)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.RateCard != nil || fresh.Facts.Resolution.Descriptor.RateCard != (llm.RateCardReference{}) || fresh.Facts.Generation == bound.Facts.Generation {
		t.Fatal("new preparation selected old card")
	}
	live := reservationRunner(t, fresh.Provider)
	live.ApplyTarget(bound)
	r, err := live.ReserveRuntime()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Release)
	if err := r.ApplyTarget(fresh); err != nil {
		t.Fatal(err)
	}
	if live.RunTarget().RateCard != nil {
		t.Fatal("new preparation inherited active card")
	}
}

func TestTargetRateCardRestoreAndFailure(t *testing.T) {
	t.Parallel()
	bound, _ := preparedCard(t)
	live := reservationRunner(t, &requestRecordingProvider{})
	target := live.RunTarget()
	target.RateCard = bound.RateCard
	r, err := live.ReserveRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Release()
	saved := rewind.Target{Provider: target.Spec.Name, Model: target.Model, Window: target.Window}
	if err := r.RestoreConversation(nil, saved, code.Plan{}, target); err != nil {
		t.Fatal(err)
	}
	want := live.RunTarget()
	target.RateCard.CacheWrites[0].Rate.PerMillionTokens = 99
	if !reflect.DeepEqual(live.RunTarget(), want) {
		t.Fatal("exact restore borrowed card")
	}
	target.RateCard = nil
	if err := r.RestoreConversation(nil, saved, code.Plan{}, target); err != nil {
		t.Fatal(err)
	}
	if live.RunTarget().RateCard != nil {
		t.Fatal("exact restore inherited card")
	}
	r.Release()

	store, err := db.Open(t.Context(), t.TempDir()+"/prefs.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	settings := engine.NewSettings(store, nil, nil, t.TempDir())
	bound, _ = preparedCard(t)
	r, err = live.ReserveRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Release()
	session := engine.StoredSessionRestore{ID: "session", UseSavedTarget: true, Target: bound}
	if err := r.RestoreStoredSession(t.Context(), session, settings.Svc); err != nil {
		t.Fatal(err)
	}
	want = live.RunTarget()
	bound.RateCard.CacheWrites[0].Rate.PerMillionTokens = 123
	if !reflect.DeepEqual(live.RunTarget(), want) {
		t.Fatal("ordinary restore borrowed card")
	}
	if err := r.RestoreSessionContext(nil, engine.MCPActivations{}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(live.RunTarget(), want) {
		t.Fatal("context-only restore changed basis")
	}
	cleared := session
	cleared.Target.RateCard = nil
	if err := r.RestoreStoredSession(t.Context(), cleared, settings.Svc); err != nil {
		t.Fatal(err)
	}
	if got := live.RunTarget(); got.RateCard != nil || got.Facts.Resolution.Descriptor.RateCard != (llm.RateCardReference{}) {
		t.Fatal("ordinary restore inherited card")
	}
	if err := r.RestoreStoredSession(t.Context(), session, settings.Svc); err != nil {
		t.Fatal(err)
	}
	want = live.RunTarget()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.ApplyTargetSelection(t.Context(), bound, settings.Svc, prefs.ModelSelection{Provider: bound.Spec.Name, Model: bound.Spec.Model}); err == nil {
		t.Fatal("closed store accepted selection")
	}
	if err := r.RestoreStoredSession(t.Context(), session, settings.Svc); err == nil {
		t.Fatal("closed store accepted restore")
	}
	if !reflect.DeepEqual(live.RunTarget(), want) {
		t.Fatal("failed persistence partially replaced basis")
	}
}

// This exercises explicit host dispatch, not LiveRunner's still-unwired shared
// option builder. The production hook must take that builder's policy.target.
type boundCardSink struct {
	runner.NopSink
	events []runner.ProviderAttemptSettled
}

func (s *boundCardSink) OnProviderAttemptSettled(_ context.Context, e runner.ProviderAttemptSettled) {
	s.events = append(s.events, e)
	if e.RateCard != nil {
		e.RateCard.Reference.Revision = "sink"
		e.RateCard.CacheWrites[0].Rate.PerMillionTokens = 999
	}
}

func TestBoundTargetRateCardLoopbackRunnerDelivery(t *testing.T) {
	t.Parallel()
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		if strings.Contains(string(body), "local-estimate-policy") || strings.Contains(string(body), "pricing_provider") || strings.Contains(string(body), "cache_writes") || strings.Contains(r.Header.Get("X-Rate-Card"), "local-estimate-policy") {
			t.Error("local pricing policy transmitted")
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		once.Do(func() { close(started); <-release })
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"c1","choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(server.Close)
	settings := preparedGatewaySettings(server.URL)
	update, err := settings.PrepareTargetCached(t.Context(), engine.ProviderSpec{Name: "gateway-route", APIKey: "test"})
	if err != nil {
		t.Fatal(err)
	}
	target := engine.RateCardTarget{Provider: update.Spec.Name, Model: llm.ModelID(update.Spec.Model), Protocol: llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS}
	old, err := engine.BindTargetRateCard(update, targetCardConfig(t, target, "v1"))
	if err != nil {
		t.Fatal(err)
	}
	sink := &boundCardSink{}
	frozen := old.RateCard.Clone()
	run := runner.New(runner.ClientFromProvider(old.Provider), runner.WithRateCard(frozen), runner.WithSink(sink))
	done := make(chan runner.TaskResult, 1)
	go func() { done <- run.Run(t.Context(), runner.TaskSpec{Prompt: "go"}) }()
	select {
	case <-started:
	case early := <-done:
		close(release)
		t.Fatalf("invocation returned before loopback request: %v", early.Err)
	}
	next, bindErr := engine.BindTargetRateCard(update, targetCardConfig(t, target, "v2"))
	old.RateCard.CacheWrites[0].Rate.PerMillionTokens = 88
	close(release)
	result := <-done
	if bindErr != nil {
		t.Fatal(bindErr)
	}
	if result.Err != nil || len(result.ProviderObservations) != 1 || len(sink.events) != 1 {
		t.Fatalf("result = %+v", result)
	}
	observation := result.ProviderObservations[0]
	if !reflect.DeepEqual(observation.RateCard, &frozen) || next.RateCard.Reference.Revision != "v2" {
		t.Fatal("blocked attempt repriced or sink aliased result")
	}
	if observation.Usage != nil || observation.PricingUsage != nil {
		t.Fatal("basis inferred usage evidence")
	}
	observation.RateCard.CacheWrites[0].Rate.PerMillionTokens = 77
	if frozen.CacheWrites[0].Rate.PerMillionTokens != 2.5 || next.RateCard.CacheWrites[0].Rate.PerMillionTokens != 2.5 || sink.events[0].RateCard.CacheWrites[0].Rate.PerMillionTokens != 999 {
		t.Fatal("result/sink/selected snapshots alias")
	}
	unbound := runner.New(runner.ClientFromProvider(update.Provider)).Run(t.Context(), runner.TaskSpec{Prompt: "unbound"})
	if unbound.Err != nil || len(unbound.ProviderObservations) != 1 || unbound.ProviderObservations[0].RateCard != nil {
		t.Fatal("unbound dispatch inferred policy")
	}
}

func TestBindTargetRateCardFreezesActualPolicyNotFutureLookup(t *testing.T) {
	t.Parallel()
	settings, baseURL, _ := routeProtocolSettings(t)
	const alias = "exact alias"
	if err := settings.Registry.SetRouteProtocol(t.Context(), "litellm", alias, llm.ModelProtocols.PROTOCOLMESSAGES); err != nil {
		t.Fatal(err)
	}
	spec := engine.ProviderSpec{Name: "litellm", Model: alias, BaseURL: baseURL, APIKey: "test"}
	prepared, err := settings.PrepareTargetCached(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	config := targetCardConfig(t, engine.RateCardTarget{Provider: spec.Name, Model: alias, BaseURL: baseURL, Protocol: llm.ModelProtocols.PROTOCOLMESSAGES}, "messages")
	if err := settings.Registry.SetRouteProtocol(t.Context(), "litellm", alias, llm.ModelProtocols.PROTOCOLRESPONSES); err != nil {
		t.Fatal(err)
	}
	bound, err := engine.BindTargetRateCard(prepared, config)
	if err != nil || bound.Spec.Protocol != llm.ModelProtocols.PROTOCOLUNKNOWN || bound.Facts.SelectedProtocol != llm.ModelProtocols.PROTOCOLMESSAGES {
		t.Fatal("binding consulted future policy or override intent", err)
	}
	fresh, err := settings.PrepareTargetCached(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.RateCard != nil || fresh.Facts.SelectedProtocol != llm.ModelProtocols.PROTOCOLRESPONSES || fresh.Facts.Generation == bound.Facts.Generation {
		t.Fatal("new preparation inherited basis or old codec")
	}
	if _, err := engine.BindTargetRateCard(fresh, config); !errors.Is(err, engine.ErrRateCardTargetMismatch) {
		t.Fatal("old policy matched newly selected protocol", err)
	}
}

func TestTargetRateCardMetadataRefreshCannotSelectPolicy(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/model/info" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = io.WriteString(w, targetModelInfoRows)
	}))
	t.Cleanup(server.Close)
	settings := preparedGatewaySettings(server.URL)
	unbound, err := settings.PrepareTargetCached(t.Context(), engine.ProviderSpec{Name: "gateway-route", Model: "alias", APIKey: "test"})
	if err != nil {
		t.Fatal(err)
	}
	config := targetCardConfig(t, engine.RateCardTarget{Provider: unbound.Spec.Name, Model: "alias", Protocol: llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS}, "v1")
	bound, err := engine.BindTargetRateCard(unbound, config)
	if err != nil {
		t.Fatal(err)
	}
	before := (engine.RunTarget{Facts: bound.Facts, RateCard: bound.RateCard}).Clone()
	if _, err := unbound.Facts.RefreshModelInfo(t.Context()); err != nil {
		t.Fatal(err)
	}
	if unbound.RateCard != nil || unbound.Facts.Resolution.Descriptor.RateCard != (llm.RateCardReference{}) {
		t.Fatal("advertised prices selected a local policy")
	}
	if !reflect.DeepEqual(bound.Facts, before.Facts) || !reflect.DeepEqual(bound.RateCard, before.RateCard) {
		t.Fatal("metadata refresh repriced explicit policy or changed pure runtime facts")
	}
}

func TestTargetRateCardAtomicPublication(t *testing.T) {
	t.Parallel()
	first, _ := preparedCard(t)
	second, _ := preparedCard(t)
	second.RateCard.Reference.Revision = "v2"
	second.RateCard.CacheWrites[0].Rate.PerMillionTokens = 42
	second.Facts.Resolution.Descriptor.RateCard = second.RateCard.Reference
	live := reservationRunner(t, first.Provider)
	live.ApplyTarget(first)
	var group sync.WaitGroup
	group.Go(func() {
		for range 100 {
			live.ApplyTarget(second)
			live.ApplyTarget(first)
		}
	})
	for range 100 {
		target := live.RunTarget()
		want := first
		if target.Facts.Generation == second.Facts.Generation {
			want = second
		}
		if target.Provider != want.Provider || target.Spec != want.Spec || !reflect.DeepEqual(target.RateCard, want.RateCard) || target.Facts.Resolution.Descriptor.RateCard != target.RateCard.Reference {
			t.Error("partial provider/facts/card publication")
		}
	}
	group.Wait()
}

func TestTargetRateCardRejectedTransitionsPreserveBasis(t *testing.T) {
	t.Parallel()
	bound, _ := preparedCard(t)
	live := reservationRunner(t, bound.Provider)
	live.ApplyTarget(bound)
	live.RestoreContext([]llm.Message{{Role: llm.RoleUser, Parts: []llm.ContentPart{{Type: llm.ContentTypeImage, Image: &llm.ImageData{DataURI: "data:image/png;base64,YQ==", MIMEType: "image/png"}}}}})
	want, history := live.RunTarget(), live.ContextSnapshot()
	r, err := live.ReserveRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Release()
	other, _ := preparedCard(t)
	other.Spec.Name = "claude-code"
	if err := r.ApplyTarget(other); !errors.Is(err, rewind.ErrTarget) {
		t.Fatalf("history rejection = %v", err)
	}
	if !reflect.DeepEqual(live.RunTarget(), want) || !reflect.DeepEqual(live.ContextSnapshot(), history) {
		t.Fatal("rejected history partially replaced basis")
	}
	r.Release()
	dispatch, err := live.ReserveTurn()
	if err != nil {
		t.Fatal(err)
	}
	defer dispatch.Release()
	if err := dispatch.ApplyTarget(other); !errors.Is(err, engine.ErrRuntimeBusy) {
		t.Fatal(err)
	}
	live.ApplyTarget(other)
	if !reflect.DeepEqual(live.RunTarget(), want) {
		t.Fatal("busy target publication changed basis")
	}
}
