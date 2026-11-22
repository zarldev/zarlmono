package engine_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestAuxiliaryJudgeResolvedTargetLabels(t *testing.T) {
	for _, tc := range []struct {
		name, provider, model, activeModel, wantProvider, wantModel string
		off                                                         bool
	}{
		{name: "active", provider: prefs.ActiveModelTarget, activeModel: "active-model", wantProvider: "aux-fixture", wantModel: "active-model"},
		{name: "model only preserves URL", provider: prefs.ActiveModelTarget, model: "judge-model", activeModel: "active-model", wantProvider: "aux-main", wantModel: "judge-model"},
		{name: "dedicated", provider: "aux-judge", model: "dedicated-model", activeModel: "active-model", wantProvider: "aux-judge", wantModel: "dedicated-model"},
		{name: "dedicated inherits model before defaults", provider: "aux-judge", activeModel: "inherited-model", wantProvider: "aux-judge", wantModel: "inherited-model"},
		{name: "registry default only when empty", provider: "aux-judge", wantProvider: "aux-judge", wantModel: "registry-default"},
		{name: "fallback actual active", provider: "unbuildable-fixture", model: "rejected-model", activeModel: "actual-model", wantProvider: "aux-fixture", wantModel: "actual-model"},
		{name: "off", provider: prefs.ActiveModelTarget, off: true},
		{name: "OAuth unknown", provider: "openai-codex", wantProvider: "openai-codex"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Path != "/chat/completions" && r.URL.Path != "/v1/chat/completions" {
					t.Errorf("unexpected probe %s", r.URL.Path)
				}
				var body struct {
					Model string `json:"model"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body.Model != tc.wantModel {
					t.Errorf("wire model=%q want %q", body.Model, tc.wantModel)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"{\\\"action\\\":\\\"smaller_scope\\\",\\\"rationale\\\":\\\"fixture\\\"}\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			}))
			t.Cleanup(server.Close)
			s := newJudgeTestSettings(t)
			defs := append(backends.BuiltinDefinitions(), backends.ProviderDefinition{Name: "aux-main", AdapterType: backends.AdapterTypes.OPENAICOMPATIBLE, BaseURL: "http://127.0.0.1:1", DefaultModel: "wrong-main-default", Enabled: true}, backends.ProviderDefinition{Name: "aux-judge", AdapterType: backends.AdapterTypes.OPENAICOMPATIBLE, BaseURL: server.URL, DefaultModel: "registry-default", Enabled: true})
			s.Registry = backends.NewRegistry(backends.WithProviderDefinitions(defs))
			for key, val := range map[string]string{prefs.KeyJudgeProvider: tc.provider, prefs.KeyJudgeModel: tc.model, prefs.KeyCompactProvider: prefs.ActiveModelTarget, prefs.KeyProgrammaticTools: "off", prefs.KeyCompactionMode: "manual"} {
				if val == "" {
					continue
				}
				if err := s.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, key, val); err != nil {
					t.Fatal(err)
				}
			}
			if tc.off {
				if err := s.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeyDecomposeJudge, "off"); err != nil {
					t.Fatal(err)
				}
			}
			ws, err := code.NewWorkspace(s.WorkspaceRoot())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = ws.Close() })
			p := &auxiliaryRunProvider{}
			live := engine.NewLiveRunner(p, ws, tc.activeModel, engine.WithSettings(s))
			live.SetProviderSpec(p, engine.ProviderSpec{Name: "aux-main", Model: tc.activeModel, BaseURL: server.URL})
			t.Cleanup(func() { _ = live.Close(context.WithoutCancel(t.Context())) })
			result := live.RunHeadlessWithObservations(t.Context(), "Exercise the real judge target.", 5)
			o := result.Auxiliary.Judge
			if tc.off {
				if o != nil {
					t.Fatal("off fabricated judge")
				}
				return
			}
			if o == nil || o.Inference.Provider != tc.wantProvider || o.Inference.Model != tc.wantModel {
				t.Fatalf("observation=%+v want provider=%q model=%q", o, tc.wantProvider, tc.wantModel)
			}
			if tc.wantProvider == "aux-main" || tc.wantProvider == "aux-judge" {
				if requests.Load() != 1 {
					t.Fatalf("requests=%d", requests.Load())
				}
			}
		})
	}
}

func TestAuxiliaryHeadlessRedriveLatestSurvives(t *testing.T) {
	p := &auxiliaryRunProvider{}
	live := newAuxiliaryRunner(t, p, func(context.Context, engine.AuxiliaryObservation) {})
	// First attempt triggers the helper, the second does not. This is one drive,
	// so the slot must not be replaced by an empty last-attempt snapshot.
	live.SetVerifyLoop("false", 2)
	result := live.RunHeadlessWithObservations(t.Context(), "Exercise re-drive retention.", 5)
	if p.judges != 1 || result.Auxiliary.Judge == nil || result.Auxiliary.Judge.Inference.Usage.PromptTokens != 1000 {
		t.Fatalf("judge=%d slot=%+v", p.judges, result.Auxiliary.Judge)
	}
	if result.Task.Timing.ProviderAttempts != 1 {
		t.Fatalf("last attempt should have one request, got %d", result.Task.Timing.ProviderAttempts)
	}
}
