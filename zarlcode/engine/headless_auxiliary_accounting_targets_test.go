package engine_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

func TestHeadlessAuxiliaryAccountingActualCompactorPurposesAndConstructionBinding(t *testing.T) {
	for _, engineName := range []string{"summary", "executive", "handover", "tiered", "structural"} {
		for _, binding := range []string{"active", "model-only", "rebuild-same-label", "fallback"} {
			t.Run(engineName+"/"+binding, func(t *testing.T) {
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
				result := live.RunHeadlessWithObservations(t.Context(), "force production compaction", 12)
				rows := headlessAuxiliaryRows(t, settings.Store, string(result.Task.ID))
				if engineName == "tiered" || engineName == "structural" {
					if len(rows) != 0 || rawHelpers.Load() != 0 || rebuilt.Load() != 0 {
						t.Fatal("non-LLM compactor invoked helper")
					}
					return
				}
				calls := int(rawHelpers.Load() + rebuilt.Load())
				if calls <= 3 || len(rows) != calls {
					t.Fatalf("calls=%d aux=%d roots=%d", calls, len(rows), roots)
				}
				purposes := map[string]engine.AuxiliaryAccountingPurpose{"summary": engine.AuxiliaryAccountingPurposes.PURPOSESUMMARY, "executive": engine.AuxiliaryAccountingPurposes.PURPOSEEXECUTIVE, "handover": engine.AuxiliaryAccountingPurposes.PURPOSEHANDOVER}
				for _, row := range rows {
					got, err := engine.DecodeRecordedHeadlessAuxiliaryAccounting(row.PayloadJSON)
					if err != nil {
						t.Fatal(err)
					}
					if got.Purpose != purposes[engineName] || got.Model != "actual-model" || got.Provider != "actual-route" {
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

func TestHeadlessAuxiliaryAccountingJudgeActualReuseRebuildAndFallback(t *testing.T) {
	for _, binding := range []string{"reuse", "model-only rebuild", "same-label rebuild", "fallback", "off"} {
		t.Run(binding, func(t *testing.T) {
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
			result := live.RunHeadlessWithObservations(t.Context(), "judge actual binding", 6)
			rows := headlessAuxiliaryRows(t, settings.Store, string(result.Task.ID))
			if binding == "off" {
				if len(rows) != 0 {
					t.Fatal("disabled helper captured")
				}
				return
			}
			if len(rows) != 1 {
				t.Fatalf("aux=%d", len(rows))
			}
			got, err := engine.DecodeRecordedHeadlessAuxiliaryAccounting(rows[0].PayloadJSON)
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
			} else if !cmp.Equal(got.RateCard, recordedAccountingFixture().RateCard, cmpopts.EquateComparable(llm.BillingMode{}, llm.MetadataSource{})) || got.Model != "actual-model" || got.Usage.PromptTokens != 1000 {
				t.Fatal("reuse/fallback lost actual target")
			}
		})
	}
}

func TestHeadlessAuxiliaryAccountingTargetsFrozenBeforeFirstRootRequest(t *testing.T) {
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
	result := live.RunHeadlessWithObservations(t.Context(), "freeze judge construction before root invocation", 6)
	if result.Task.Err != nil {
		t.Fatal(result.Task.Err)
	}
	rows := headlessAuxiliaryRows(t, settings.Store, string(result.Task.ID))
	if len(rows) != 1 {
		t.Fatal("in-flight settings changed constructed judge")
	}
	got, err := engine.DecodeRecordedHeadlessAuxiliaryAccounting(rows[0].PayloadJSON)
	if err != nil || got.Model != "actual-model" || got.Provider != "actual-route" || got.RateCard == nil {
		t.Fatal("in-flight target was resnapshotted", err)
	}
	live.ClearContext()
	next := live.RunHeadlessWithObservations(t.Context(), "next turn observes disabled judge", 6)
	if next.Task.Err != nil {
		t.Fatal(next.Task.Err)
	}
	later := headlessAuxiliaryRows(t, settings.Store, string(next.Task.ID))
	if len(later) != 0 {
		t.Fatal("next turn ignored new settings")
	}
}

func TestHeadlessAuxiliaryAccountingChildCompactorUsesLiveParentNotSelectedChild(t *testing.T) {
	for _, kind := range []string{"summary", "executive", "handover"} {
		t.Run(kind, func(t *testing.T) {
			settings := recordedAuxiliarySettings(t, map[string]string{prefs.KeyCompactEngine: kind, prefs.KeyCompactionMode: "auto", prefs.KeySpawnDefaultExploreProvider: prefs.ActiveModelTarget, prefs.KeySpawnDefaultVerifyProvider: prefs.ActiveModelTarget, prefs.KeySpawnDefaultImplementProvider: prefs.ActiveModelTarget})
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
			result := live.RunHeadlessWithObservations(ctx, "select a child but preserve parent compactor binding", 4)
			if result.Task.Err != nil {
				t.Fatal(result.Task.Err)
			}
			rows := headlessAuxiliaryRows(t, settings.Store, string(result.Task.ID))
			if childCalls.Load() < 3 || helperCalls.Load() < 1 || len(rows) != int(helperCalls.Load()) {
				t.Fatalf("child=%d helper=%d aux=%d", childCalls.Load(), helperCalls.Load(), len(rows))
			}
			for _, row := range rows {
				got, err := engine.DecodeRecordedHeadlessAuxiliaryAccounting(row.PayloadJSON)
				if err != nil || got.Purpose != map[string]engine.AuxiliaryAccountingPurpose{"summary": engine.AuxiliaryAccountingPurposes.PURPOSESUMMARY, "executive": engine.AuxiliaryAccountingPurposes.PURPOSEEXECUTIVE, "handover": engine.AuxiliaryAccountingPurposes.PURPOSEHANDOVER}[kind] || got.Provider != "actual-route" || got.Model != "actual-model" || got.RateCard == nil {
					t.Fatalf("child compactor borrowed selected child identity: %+v %v", got, err)
				}
			}
		})
	}
}

func TestHeadlessAuxiliaryAccountingHTTPRetryIsOneOuterInvocation(t *testing.T) {
	settings := headlessAuxiliarySettings(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":{"message":"private transport retry canary"}}`)
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"{\\\"action\\\":\\\"smaller_scope\\\",\\\"rationale\\\":\\\"fixture\\\"}\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	settings.Registry = backends.NewRegistry(backends.WithProviderDefinitions([]backends.ProviderDefinition{{Name: "retry-route", AdapterType: backends.AdapterTypes.OPENAICOMPATIBLE, BaseURL: server.URL, Enabled: true}}))
	setRecordedAuxiliarySetting(t, settings, prefs.KeyJudgeProvider, "retry-route")
	setRecordedAuxiliarySetting(t, settings, prefs.KeyJudgeModel, "retry-model")
	p := &auxiliaryRunProvider{}
	live := recordedAuxiliaryLive(t, settings, p)
	result := live.RunHeadlessWithObservations(t.Context(), "one outer stream despite HTTP retry", 6)
	rows := headlessAuxiliaryRows(t, settings.Store, string(result.Task.ID))
	if requests.Load() != 2 || len(rows) != 1 {
		t.Fatalf("HTTP attempts=%d outer rows=%d", requests.Load(), len(rows))
	}
	got, err := engine.DecodeRecordedHeadlessAuxiliaryAccounting(rows[0].PayloadJSON)
	if err != nil || got.Provider != "retry-route" || got.Model != "retry-model" || got.RateCard != nil {
		t.Fatalf("retry capture %+v %v", got, err)
	}
}
