package engine_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/agent/diffrecorder"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/options"
)

func recordedAuxiliarySettings(t *testing.T, values map[string]string) *engine.Settings {
	t.Helper()
	settings := newJudgeTestSettings(t)
	defaults := map[string]string{prefs.KeyJudgeProvider: prefs.ActiveModelTarget, prefs.KeyCompactProvider: prefs.ActiveModelTarget, prefs.KeyProgrammaticTools: "off", prefs.KeyEnableWeb: "off", prefs.KeyEnableMCP: "off", prefs.KeyEnableComputer: "off", prefs.KeyDecomposeJudge: "off"}
	for key, value := range values {
		defaults[key] = value
	}
	for key, value := range defaults {
		if err := settings.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, key, value); err != nil {
			t.Fatal(err)
		}
	}
	return settings
}

func setRecordedAuxiliarySetting(t *testing.T, settings *engine.Settings, key, value string) {
	t.Helper()
	if err := settings.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, key, value); err != nil {
		t.Fatal(err)
	}
}

func recordedAuxiliaryLive(t *testing.T, settings *engine.Settings, provider llm.Provider, opts ...options.Option[engine.LiveRunner]) *engine.LiveRunner {
	t.Helper()
	ws, err := code.NewWorkspace(settings.WorkspaceRoot())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	live := engine.NewLiveRunner(provider, ws, "actual-model", append([]options.Option[engine.LiveRunner]{engine.WithSettings(settings)}, opts...)...)
	live.ApplyTarget(engine.TargetUpdate{Provider: provider, Spec: engine.ProviderSpec{Name: "actual-route", Model: "actual-model"}, RateCard: recordedAccountingFixture().RateCard, Window: 1024})
	live.SetLimits(64, 6, 3, 2)
	t.Cleanup(func() {
		if err := live.Close(context.WithoutCancel(t.Context())); err != nil {
			t.Error(err)
		}
	})
	return live
}

type recordedCompactSink struct {
	runner.NopSink
	labels []string
}

func (*recordedCompactSink) DiffEvent(diffrecorder.DiffEvent) {}
func (*recordedCompactSink) PlanUpdated(string, code.Plan)    {}
func (s *recordedCompactSink) OnCompactionSettled(_ context.Context, e runner.CompactionSettled) {
	if e.Inference.Usage != nil {
		s.labels = append(s.labels, e.Inference.Model)
		e.Inference.Usage.TotalTokens = 99999
	}
}

func TestRecordedAuxiliaryActualCompactorPurposesAndConstructionBinding(t *testing.T) {
	for _, engineName := range []string{"summary", "executive", "handover", "tiered", "structural"} {
		for _, binding := range []string{"active", "model-only", "rebuild-same-label", "fallback"} {
			t.Run(engineName+"/"+binding, func(t *testing.T) {
				store := recordedAccountingStore(t)
				settings := recordedAuxiliarySettings(t, map[string]string{prefs.KeyCompactEngine: engineName, prefs.KeyCompactionMode: "auto"})
				diagnosticModel := "actual-model"
				if binding == "model-only" {
					diagnosticModel = "diagnostic-only"
					setRecordedAuxiliarySetting(t, settings, prefs.KeyCompactModel, diagnosticModel)
				}
				if binding == "fallback" {
					setRecordedAuxiliarySetting(t, settings, prefs.KeyCompactProvider, "missing-backend")
					setRecordedAuxiliarySetting(t, settings, prefs.KeyCompactModel, "rejected")
				}
				var rawHelpers, rebuilt atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					rebuilt.Add(1)
					var req struct {
						Model string `json:"model"`
					}
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Error(err)
					}
					if req.Model != "actual-model" {
						t.Error("rebuilt model changed")
					}
					fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"frozen compaction document\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3,\"total_tokens\":10}}\n\ndata: [DONE]\n\n")
				}))
				t.Cleanup(server.Close)
				if binding == "rebuild-same-label" {
					settings.Registry = backends.NewRegistry(backends.WithProviderDefinitions([]backends.ProviderDefinition{{Name: "actual-route", AdapterType: backends.AdapterTypes.OPENAICOMPATIBLE, BaseURL: server.URL, Enabled: true}}))
					setRecordedAuxiliarySetting(t, settings, prefs.KeyCompactProvider, "actual-route")
					setRecordedAuxiliarySetting(t, settings, prefs.KeyCompactModel, "actual-model")
				}
				roots := 0
				provider := recordedAccountingProvider(func(_ context.Context, req llm.CompletionRequest) llm.CompletionStream {
					if len(req.Tools) == 0 {
						rawHelpers.Add(1)
						return func(yield func(llm.CompletionChunk, error) bool) {
							e := auxiliaryEvidence()
							yield(llm.CompletionChunk{Content: "frozen compaction document", UsageReported: true, Usage: *e.Usage, PricingUsage: e.PricingUsage, Execution: e.Execution}, nil)
							e.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = 99999
						}
					}
					roots++
					return func(yield func(llm.CompletionChunk, error) bool) {
						yield(llm.CompletionChunk{UsageReported: true, Usage: llm.Usage{PromptTokens: 2000, CompletionTokens: 1, TotalTokens: 2001}, ToolCalls: []llm.ToolCall{{ID: fmt.Sprintf("read-%d", roots), Type: "function", Function: llm.ToolCallFunction{Name: "read", Arguments: fmt.Sprintf(`{"path":"missing-%d"}`, roots)}}}}, nil)
					}
				})
				sink := &recordedCompactSink{}
				live := recordedAuxiliaryLive(t, settings, provider, engine.WithLiveSink(sink))
				live.SetLimits(64, 12, 3, 2)
				var restored []llm.Message
				for range 20 {
					restored = append(restored, llm.Message{Role: llm.RoleUser, Content: strings.Repeat("retained evidence ", 15)}, llm.Message{Role: llm.RoleAssistant, Content: "old answer"})
				}
				live.RestoreContext(restored)
				_ = live.RunRecordedTurn(t.Context(), "force production compaction after root receipt", nil, store, "session")
				_, batch := auxiliaryBatch(t, live)
				if engineName == "tiered" || engineName == "structural" {
					if len(batch.Auxiliaries) != 0 || rawHelpers.Load() != 0 || rebuilt.Load() != 0 {
						t.Fatal("non-LLM compactor invoked helper")
					}
					return
				}
				calls := int(rawHelpers.Load() + rebuilt.Load())
				if calls <= 3 || len(batch.Auxiliaries) != calls {
					t.Fatalf("calls=%d aux=%d roots=%d", calls, len(batch.Auxiliaries), roots)
				}
				purposes := map[string]engine.AuxiliaryAccountingPurpose{"summary": engine.AuxiliaryAccountingPurposes.PURPOSESUMMARY, "executive": engine.AuxiliaryAccountingPurposes.PURPOSEEXECUTIVE, "handover": engine.AuxiliaryAccountingPurposes.PURPOSEHANDOVER}
				for _, row := range batch.Auxiliaries {
					got, err := engine.DecodeRecordedSessionAuxiliaryAccounting(row.PayloadJSON)
					if err != nil {
						t.Fatal(err)
					}
					if got.Purpose != purposes[engineName] || got.Model != "actual-model" || got.Provider != "actual-route" || got.OwnerRequestBatchID != batch.Roots[0].RequestBatchID {
						t.Fatalf("actual binding %+v", got)
					}
					if binding == "rebuild-same-label" {
						if got.RateCard != nil || got.ConfiguredEstimate != nil || rawHelpers.Load() != 0 {
							t.Fatal("rebuilt provider inherited parent card")
						}
					} else if got.RateCard == nil || got.ConfiguredEstimate == nil || got.Usage.PromptTokens != 1000 {
						t.Fatal("reused handle lost bound evidence")
					}
				}
				for _, label := range sink.labels {
					if label != diagnosticModel {
						t.Fatalf("diagnostics=%q want %q", label, diagnosticModel)
					}
				}
			})
		}
	}
}

func TestRecordedAuxiliaryJudgeActualReuseRebuildAndFallback(t *testing.T) {
	for _, binding := range []string{"reuse", "model-only rebuild", "same-label rebuild", "fallback", "off"} {
		t.Run(binding, func(t *testing.T) {
			store := recordedAccountingStore(t)
			settings := recordedAuxiliarySettings(t, map[string]string{prefs.KeyDecomposeJudge: "on", prefs.KeyCompactionMode: "manual"})
			p := &auxiliaryRunProvider{}
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"{\\\"action\\\":\\\"smaller_scope\\\",\\\"rationale\\\":\\\"fixture\\\"}\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			}))
			t.Cleanup(server.Close)
			settings.Registry = backends.NewRegistry(backends.WithProviderDefinitions([]backends.ProviderDefinition{{Name: "actual-route", AdapterType: backends.AdapterTypes.OPENAICOMPATIBLE, BaseURL: server.URL, Enabled: true}}))
			switch binding {
			case "model-only rebuild":
				setRecordedAuxiliarySetting(t, settings, prefs.KeyJudgeModel, "judge-model")
			case "same-label rebuild":
				setRecordedAuxiliarySetting(t, settings, prefs.KeyJudgeProvider, "actual-route")
			case "fallback":
				setRecordedAuxiliarySetting(t, settings, prefs.KeyJudgeProvider, "missing-backend")
			case "off":
				setRecordedAuxiliarySetting(t, settings, prefs.KeyDecomposeJudge, "off")
			}
			live := recordedAuxiliaryLive(t, settings, p)
			_ = live.RunRecordedTurn(t.Context(), "judge actual binding", nil, store, "session")
			_, batch := auxiliaryBatch(t, live)
			if binding == "off" {
				if len(batch.Auxiliaries) != 0 {
					t.Fatal("disabled helper captured")
				}
				return
			}
			if len(batch.Auxiliaries) != 1 {
				t.Fatalf("aux=%d", len(batch.Auxiliaries))
			}
			got, err := engine.DecodeRecordedSessionAuxiliaryAccounting(batch.Auxiliaries[0].PayloadJSON)
			if err != nil {
				t.Fatal(err)
			}
			if binding == "model-only rebuild" || binding == "same-label rebuild" {
				if requests.Load() != 1 || got.RateCard != nil || got.ConfiguredEstimate != nil {
					t.Fatal("rebuild inherited card")
				}
				if binding == "model-only rebuild" && got.Model != "judge-model" {
					t.Fatal("wrong constructed model")
				}
			} else if !reflect.DeepEqual(got.RateCard, recordedAccountingFixture().RateCard) || got.Model != "actual-model" || got.Usage.PromptTokens != 1000 {
				t.Fatal("reuse/fallback lost actual target")
			}
		})
	}
}

func TestRecordedAuxiliaryTargetsFrozenBeforeFirstRootRequest(t *testing.T) {
	store := recordedAccountingStore(t)
	settings := recordedAuxiliarySettings(t, map[string]string{prefs.KeyDecomposeJudge: "on", prefs.KeyCompactionMode: "manual"})
	p := &auxiliaryRunProvider{}
	first := true
	provider := recordedAccountingProvider(func(ctx context.Context, req llm.CompletionRequest) llm.CompletionStream {
		if first {
			first = false
			setRecordedAuxiliarySetting(t, settings, prefs.KeyDecomposeJudge, "off")
			setRecordedAuxiliarySetting(t, settings, prefs.KeyJudgeProvider, "missing-backend")
			setRecordedAuxiliarySetting(t, settings, prefs.KeyJudgeModel, "later-model")
		}
		return p.Complete(ctx, req)
	})
	live := recordedAuxiliaryLive(t, settings, provider)
	if err := live.RunRecordedTurn(t.Context(), "freeze judge construction before root invocation", nil, store, "session"); err != nil {
		t.Fatal(err)
	}
	_, batch := auxiliaryBatch(t, live)
	if len(batch.Auxiliaries) != 1 {
		t.Fatal("in-flight settings changed constructed judge")
	}
	got, err := engine.DecodeRecordedSessionAuxiliaryAccounting(batch.Auxiliaries[0].PayloadJSON)
	if err != nil || got.Model != "actual-model" || got.Provider != "actual-route" || got.RateCard == nil {
		t.Fatal("in-flight target was resnapshotted", err)
	}
	live.ClearContext()
	if err := live.RunRecordedTurn(t.Context(), "next turn observes disabled judge", nil, store, "session"); err != nil {
		t.Fatal(err)
	}
	_, later := auxiliaryBatch(t, live)
	if len(later.Auxiliaries) != 1 {
		t.Fatal("next turn ignored new settings")
	}
}

func TestRecordedAuxiliaryChildCompactorUsesLiveParentNotSelectedChild(t *testing.T) {
	store := recordedAccountingStore(t)
	settings := recordedAuxiliarySettings(t, map[string]string{prefs.KeyCompactEngine: "summary", prefs.KeyCompactionMode: "auto", prefs.KeySpawnDefaultExploreProvider: prefs.ActiveModelTarget, prefs.KeySpawnDefaultVerifyProvider: prefs.ActiveModelTarget, prefs.KeySpawnDefaultImplementProvider: prefs.ActiveModelTarget})
	var childCalls, helperCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.Model != "selected-child-model" {
			t.Errorf("child construction model %q", request.Model)
		}
		n := childCalls.Add(1)
		fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"child-%d\",\"type\":\"function\",\"function\":{\"name\":\"read\",\"arguments\":\"{\\\"path\\\":\\\"missing-%d\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":2000,\"completion_tokens\":1,\"total_tokens\":2001}}\n\ndata: [DONE]\n\n", n, n)
	}))
	t.Cleanup(server.Close)
	settings.Registry = backends.NewRegistry(backends.WithProviderDefinitions([]backends.ProviderDefinition{{Name: "child-route", AdapterType: backends.AdapterTypes.OPENAICOMPATIBLE, BaseURL: server.URL, ContextWindow: 1024, Enabled: true}}))
	dir := filepath.Join(settings.WorkspaceRoot(), ".zarlcode", "agents")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "child.md"), []byte("---\nname: child\ndescription: Selected child\nprovider: child-route\nmodel: selected-child-model\nmode: explore\n---\nInspect evidence.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	roots := 0
	provider := recordedAccountingProvider(func(_ context.Context, req llm.CompletionRequest) llm.CompletionStream {
		if len(req.Tools) == 0 {
			helperCalls.Add(1)
			return func(yield func(llm.CompletionChunk, error) bool) {
				e := auxiliaryEvidence()
				yield(llm.CompletionChunk{Content: "parent-bound child summary", UsageReported: true, Usage: *e.Usage, PricingUsage: e.PricingUsage}, nil)
			}
		}
		roots++
		return func(yield func(llm.CompletionChunk, error) bool) {
			if roots == 1 {
				yield(childSpawnChunk(`{"agent":"child","prompt":"selected child inspection","mode":"explore"}`), nil)
				return
			}
			if roots == 2 {
				for _, message := range req.Messages {
					var receipt struct {
						TaskID string `json:"task_id"`
					}
					if message.Role == llm.RoleTool && json.Unmarshal([]byte(message.Content), &receipt) == nil && receipt.TaskID != "" {
						args, _ := json.Marshal(map[string]string{"task_id": receipt.TaskID})
						yield(llm.CompletionChunk{ToolCalls: []llm.ToolCall{{ID: "await-child", Type: "function", Function: llm.ToolCallFunction{Name: "agent_await", Arguments: string(args)}}}}, nil)
						return
					}
				}
			}
			yield(llm.CompletionChunk{Content: "Parent report complete."}, nil)
		}
	})
	live := recordedAuxiliaryLive(t, settings, provider)
	live.SetLimits(64, 4, 12, 2)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := live.RunRecordedTurn(ctx, "select a child but preserve parent compactor binding", nil, store, "session"); err != nil {
		t.Fatal(err)
	}
	_, batch := auxiliaryBatch(t, live)
	if childCalls.Load() < 3 || helperCalls.Load() < 1 || len(batch.Auxiliaries) != int(helperCalls.Load()) {
		t.Fatalf("child=%d helper=%d aux=%d", childCalls.Load(), helperCalls.Load(), len(batch.Auxiliaries))
	}
	for _, row := range batch.Auxiliaries {
		got, err := engine.DecodeRecordedSessionAuxiliaryAccounting(row.PayloadJSON)
		if err != nil || got.Purpose != engine.AuxiliaryAccountingPurposes.PURPOSESUMMARY || got.Provider != "actual-route" || got.Model != "actual-model" || got.RateCard == nil {
			t.Fatalf("child compactor borrowed selected child identity: %+v %v", got, err)
		}
	}
}
