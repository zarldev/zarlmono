package tui_test

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
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/llm/litellm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func preparedApplicationUI(t *testing.T, endpoint string) (*engine.Settings, *tui.UI, *engine.LiveRunner) {
	t.Helper()
	settings := newTestSettings(t)
	def, _ := backends.Builtin("litellm")
	def.BaseURL, def.DefaultModel, def.ContextWindow = endpoint, "default/Alias", 32768
	settings.Registry = backends.NewRegistry(backends.WithProviderDefinitions([]backends.ProviderDefinition{def}))
	initial, err := (tui.Launch{}).PrepareTarget(t.Context(), settings, engine.ProviderSpec{Name: "litellm", Model: "initial/Alias", APIKey: "fixture-key"})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := code.NewWorkspace(settings.WorkspaceRoot())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	live := engine.NewLiveRunner(initial.Provider, ws, initial.Spec.Model, engine.WithSettings(settings))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := live.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	live.ApplyTarget(initial)
	ui := tui.New()
	ui.SetSettings(settings)
	ui.SetLiveRunner(live)
	ui.SetProviderContext(initial.Spec, initial.Spec)
	requireSetSelection(t, settings, prefs.ScopeWorkspace, prefs.ModelSelection{Provider: initial.Spec.Name, Model: initial.Spec.Model})
	_, _ = ui.Update(tea.WindowSizeMsg{Width: 140, Height: 55})
	return settings, ui, live
}

func TestPreparedLaunchUsesCachedFirstFrameAndFullHeadlessBudget(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/props" {
			t.Errorf("unexpected startup request %s", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"default_generation_settings":{"n_ctx":45678}}`)
	}))
	t.Cleanup(server.Close)
	settings := &engine.Settings{Registry: backends.NewRegistry()}
	spec := engine.ProviderSpec{Name: "llamacpp", Model: "opaque Alias", BaseURL: server.URL + "/v1"}
	cached, err := (tui.Launch{}).PrepareTarget(t.Context(), settings, spec)
	if err != nil {
		t.Fatal(err)
	}
	ui := tui.New()
	ui.SetProviderContext(cached.Spec, cached.Spec)
	_, _ = ui.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	_ = ui.View()
	if calls.Load() != 0 || cached.Facts == nil || cached.Window != engine.LiveContextWindow {
		t.Fatal("interactive preparation/render probed or lost facts")
	}
	full, err := (tui.Launch{Headless: true}).PrepareTarget(t.Context(), settings, spec)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || full.Window != 45678 || full.Facts.Budget.Tokens != full.Window {
		t.Fatalf("headless budget = %+v, calls=%d", full, calls.Load())
	}
}

func TestPreparedModelSelectionPersistsResolvedDefaultAndKeepsProtocolIntent(t *testing.T) {
	settings, ui, live := preparedApplicationUI(t, "http://127.0.0.1:1")
	before := live.RunTarget()
	runSettingsCloseCmd(t, ui, ui.SwitchTarget(prefs.ModelSelection{Provider: "litellm"}))
	after := live.RunTarget()
	if after.Spec.Model != "default/Alias" || after.Facts == nil || string(after.Facts.RequestedAlias) != after.Spec.Model {
		t.Fatalf("resolved alias lost: %+v", after)
	}
	if after.Facts.Generation == before.Facts.Generation || after.Provider == before.Provider {
		t.Fatal("selection reused old actual construction")
	}
	if after.Spec.Protocol != llm.ModelProtocols.PROTOCOLUNKNOWN || after.Facts.SelectedProtocol != llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS {
		t.Fatal("actual selected codec replaced protocol override intent")
	}
	requireSelection(t, settings, prefs.ScopeWorkspace, prefs.ModelSelection{Provider: "litellm", Model: after.Spec.Model})
}

func TestPreparedTransitionsPreserveEntireTargetOnFailure(t *testing.T) {
	for _, failure := range []string{"build", "admission", "preference"} {
		t.Run(failure, func(t *testing.T) {
			settings, ui, live := preparedApplicationUI(t, "http://127.0.0.1:1")
			before := live.RunTarget()
			cmd := ui.SwitchTarget(prefs.ModelSelection{Provider: "litellm", Model: "new/Alias"})
			switch failure {
			case "build":
				settings.Registry = backends.NewRegistry(backends.WithProviderDefinitions(nil))
			case "admission":
				reservation, err := live.ReserveTurn()
				if err != nil {
					t.Fatal(err)
				}
				defer reservation.Release()
			case "preference":
				if err := settings.Store.Close(); err != nil {
					t.Fatal(err)
				}
			}
			runSettingsCloseCmd(t, ui, cmd)
			if after := live.RunTarget(); !reflect.DeepEqual(before, after) || after.Provider != before.Provider {
				t.Fatal("failed transition replaced provider/spec/window/facts")
			}
		})
	}
}

func TestPreparedRootCompletionBindsExactAlias(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		var request struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.Model != "default/Alias" {
			t.Errorf("wire model = %q", request.Model)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"c1","choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(server.Close)
	_, ui, live := preparedApplicationUI(t, server.URL)
	runSettingsCloseCmd(t, ui, ui.SwitchTarget(prefs.ModelSelection{Provider: "litellm"}))
	if calls.Load() != 0 {
		t.Fatal("build fetched optional metadata")
	}
	for _, err := range live.RunTarget().Provider.Complete(t.Context(), llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "hello"}}}) {
		if err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("completion did not reach actual construction")
	}
}

func TestPreparedGatewayDetailHonestDuplicateRowsAndNoRenderIO(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/model/info" {
			t.Errorf("metadata path = %s", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"data":[
		{"model_name":"initial/Alias","litellm_params":{"model":"vendor/upstream"},"model_info":{"id":"deployment-A","max_input_tokens":1234,"max_tokens":999999,"input_cost_per_token":0,"supported_endpoints":["/v1/responses"]}},
		{"model_name":"initial/Alias","litellm_params":{"model":"other/upstream"},"model_info":{"id":"deployment-B","max_output_tokens":2345,"output_cost_per_token":0}}
		]}`)
	}))
	t.Cleanup(server.Close)
	_, ui, live := preparedApplicationUI(t, server.URL)
	ui.OpenTargetModelInfo()
	_ = ui.View()
	_ = ui.View()
	cmd := ui.RefreshTargetModelInfo()
	if calls.Load() != 0 {
		t.Fatal("open/render/command creation performed I/O")
	}
	runSettingsCloseCmd(t, ui, cmd)
	view := ansi.Strip(ui.View().Content)
	for _, want := range []string{"gateway advertised", `alias "initial/Alias"`, `deployment "deployment-A"`, `deployment "deployment-B"`, "IN 1234 · OUT unknown · context unknown", "IN unknown · OUT 2345 · context unknown", "Declared protocols (not grants)", "Currency unknown · applicability unknown", "Rates /M tokens · IN 0 · OUT unknown", "Rates /M tokens · IN unknown · OUT 0", "not an exhaustive model list"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "999999") || strings.Contains(view, "$0/M") || calls.Load() != 1 {
		t.Fatal("render inferred context/currency or repeated metadata I/O")
	}
	if got := live.RunTarget(); got.Window != 32768 || got.Facts.Budget.Tokens != 32768 {
		t.Fatal("advertised limits changed operational budget")
	}
}

func TestPreparedGatewayDeniedMetadataDoesNotGateListOrInference(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusNotFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/model/info":
					w.WriteHeader(status)
				case "/v1/models":
					_, _ = io.WriteString(w, `{"data":[{"id":"still-available"}]}`)
				case "/v1/chat/completions":
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"id":"c1","choices":[{"message":{"role":"assistant","content":"works"},"finish_reason":"stop"}]}`)
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
				}
			}))
			t.Cleanup(server.Close)
			_, ui, live := preparedApplicationUI(t, server.URL)
			before := live.RunTarget()
			ui.OpenTargetModelInfo()
			runSettingsCloseCmd(t, ui, ui.RefreshTargetModelInfo())
			if !strings.Contains(ansi.Strip(ui.View().Content), "inference and model list unaffected") {
				t.Fatal("denied metadata did not remain optional")
			}
			models, err := before.Provider.(*litellm.Provider).Models(t.Context())
			if err != nil || len(models) != 1 || models[0] != "still-available" {
				t.Fatalf("ordinary model list = %v, %v", models, err)
			}
			for _, err := range before.Provider.Complete(t.Context(), llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "hello"}}}) {
				if err != nil {
					t.Fatal(err)
				}
			}
			if !reflect.DeepEqual(before, live.RunTarget()) {
				t.Fatal("denied enrichment changed target")
			}
		})
	}
}

func TestPreparedGatewayLateOldGenerationReplyIgnored(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/chat/completions" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"c1","choices":[{"message":{"role":"assistant","content":"works while metadata blocked"},"finish_reason":"stop"}]}`)
			return
		}
		close(started)
		select {
		case <-release:
			w.WriteHeader(http.StatusForbidden)
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(server.Close)
	settings, ui, live := preparedApplicationUI(t, server.URL)
	ui.OpenTargetModelInfo()
	cmd := ui.RefreshTargetModelInfo()
	result := make(chan tea.Msg, 1)
	joined := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	go func() {
		defer close(joined)
		result <- cmd()
	}()
	t.Cleanup(func() { unblock(); <-joined })
	<-started
	old := live.RunTarget()
	for _, err := range old.Provider.Complete(t.Context(), llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "hello"}}}) {
		if err != nil {
			t.Fatal(err)
		}
	}
	// Same visible spec, different actual preparation (e.g. rotated credentials).
	next, err := settings.PrepareTargetCached(t.Context(), old.Spec)
	if err != nil {
		t.Fatal(err)
	}
	live.ApplyTarget(next)
	ui.OpenTargetModelInfo()
	_ = ui.View() // A blocked old refresh cannot hold snapshot reads or rendering.
	unblock()
	_, _ = ui.Update(<-result)
	view := ansi.Strip(ui.View().Content)
	if strings.Contains(view, "refresh unavailable") || !strings.Contains(view, "No cached gateway declarations") {
		t.Fatal("old-generation failure affected the new preparation")
	}
	if _, err := live.RunTarget().Facts.CachedModelInfo(); !errors.Is(err, litellm.ErrModelInfoNotCached) {
		t.Fatalf("new cache = %v", err)
	}
}

func TestPreparedModeAndOrdinaryRestorePublishNewFacts(t *testing.T) {
	settings, ui, live := preparedApplicationUI(t, "http://127.0.0.1:1")
	before := live.RunTarget()
	if err := settings.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeyMainPlanModel, "plan/Alias"); err != nil {
		t.Fatal(err)
	}
	ui.SetPlanMode(true)
	_, cmd := ui.Update(nil)
	runSettingsCloseCmd(t, ui, cmd)
	planned := live.RunTarget()
	if planned.Model != "plan/Alias" || planned.Facts == nil || planned.Facts.Generation == before.Facts.Generation || planned.Facts.RequestedAlias != "plan/Alias" {
		t.Fatalf("mode operation dropped prepared facts: %+v", planned)
	}
	selectOrdinaryProtocolSession(t, settings, ui, "litellm", "saved/Alias")
	restored := live.RunTarget()
	if restored.Model != "saved/Alias" || restored.Facts == nil || restored.Facts.Generation == planned.Facts.Generation || restored.Facts.RequestedAlias != "saved/Alias" {
		t.Fatalf("ordinary restore dropped prepared facts: %+v", restored)
	}
	if restored.Spec.Protocol != llm.ModelProtocols.PROTOCOLUNKNOWN {
		t.Fatal("ordinary restore persisted resolved codec as intent")
	}
}

func TestPreparedSettingsGatewayDetailIsReachableWithoutMetadataIO(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(http.StatusForbidden) }))
	t.Cleanup(server.Close)
	_, ui, _ := preparedApplicationUI(t, server.URL)
	// Do not execute the returned ordinary model-list fetch: opening/rendering
	// must remain synchronous, cached-only work on the update owner.
	_, _ = ui.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	_, _ = ui.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	for range 64 { // clamp at the appended row, independent of P2 row additions
		_, _ = ui.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	_, _ = ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	view := ansi.Strip(ui.View().Content)
	if !strings.Contains(view, "No cached gateway declarations") || !strings.Contains(view, "Prepared connection snapshot") || calls.Load() != 0 {
		t.Fatalf("settings detail missing or performed I/O (%d):\n%s", calls.Load(), view)
	}
}
