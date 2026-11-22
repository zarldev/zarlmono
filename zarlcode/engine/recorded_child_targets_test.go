package engine_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/catalog"
	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestRecordedChildConstructedTargetIsolation(t *testing.T) {
	for _, routing := range []string{"named", "default", "fallback"} {
		t.Run(routing, func(t *testing.T) {
			for _, changed := range []bool{false, true} {
				t.Run(map[bool]string{false: "unchanged construction inherits", true: "changed alias unknown basis"}[changed], func(t *testing.T) {
					settings := newJudgeTestSettings(t)
					store := recordedAccountingStore(t)
					const parentAlias = "opaque Parent/Alias"
					childAlias := parentAlias
					if changed {
						childAlias = "opaque Child/Alias"
					}
					for key, value := range map[string]string{
						prefs.KeySpawnDefaultExploreProvider:   prefs.ActiveModelTarget,
						prefs.KeySpawnDefaultVerifyProvider:    prefs.ActiveModelTarget,
						prefs.KeySpawnDefaultImplementProvider: prefs.ActiveModelTarget,
						prefs.KeyCompactProvider:               prefs.ActiveModelTarget,
						prefs.KeyDecomposeJudge:                "off",
						prefs.KeySpawnFallback:                 "parent",
					} {
						if err := settings.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, key, value); err != nil {
							t.Fatal(err)
						}
					}
					agentDir := filepath.Join(settings.WorkspaceRoot(), ".zarlcode", "agents")
					if err := os.MkdirAll(agentDir, 0o700); err != nil {
						t.Fatal(err)
					}
					profile := "---\nname: card-child\ndescription: Rate card child fixture\nmode: explore\n"
					if changed {
						profile += "model: " + childAlias + "\n"
					}
					profile += "---\nReport fixture evidence without making changes.\n"
					if err := os.WriteFile(filepath.Join(agentDir, "card-child.md"), []byte(profile), 0o600); err != nil {
						t.Fatal(err)
					}
					if routing == "default" {
						for key, value := range map[string]string{prefs.KeySpawnDefaultExploreProvider: "litellm", prefs.KeySpawnDefaultExploreModel: childAlias} {
							if err := settings.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, key, value); err != nil {
								t.Fatal(err)
							}
						}
					}
					started, release := make(chan struct{}), make(chan struct{})
					unblock := sync.OnceFunc(func() { close(release) })
					var parentCalls, childCalls atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						data, err := io.ReadAll(r.Body)
						if err != nil {
							t.Error(err)
							return
						}
						if strings.Contains(string(data), "local-child-policy") || strings.Contains(string(data), "rate_card") {
							t.Error("private policy sent to gateway")
						}
						var req struct {
							Model    string `json:"model"`
							Messages []struct {
								Role    string          `json:"role"`
								Content json.RawMessage `json:"content"`
							} `json:"messages"`
						}
						if err := json.Unmarshal(data, &req); err != nil {
							t.Error(err)
							return
						}
						child := false
						for _, message := range req.Messages {
							if message.Role == "user" && strings.Contains(string(message.Content), "card child assignment") {
								child = true
							}
						}
						if child {
							if childCalls.Add(1) == 1 {
								close(started)
							}
							wantModel := childAlias
							if routing == "fallback" {
								wantModel = parentAlias
							}
							if req.Model != wantModel {
								t.Errorf("child model=%q want=%q", req.Model, childAlias)
							}
							select {
							case <-release:
							case <-r.Context().Done():
								return
							}
							completionFixture(w, "The child fixture evidence is complete.", "")
							return
						}
						if parentCalls.Add(1) == 1 {
							args := `{"agent":"card-child","prompt":"card child assignment","mode":"explore"}`
							if routing == "default" {
								args = `{"prompt":"card child assignment","mode":"explore"}`
							}
							if routing == "fallback" {
								args = `{"agent":"missing-agent","prompt":"card child assignment","mode":"explore"}`
							}
							completionFixture(w, "", args)
							return
						}
						completionFixture(w, "The requested read-only evidence is complete.", "")
					}))
					t.Cleanup(server.Close)
					t.Cleanup(unblock)
					def, _ := backends.Builtin("litellm")
					def.BaseURL, def.DefaultModel, def.ContextWindow = server.URL, parentAlias, 32768
					settings.Registry = backends.NewRegistry(backends.WithSettingsService(liteLLMKeys{}), backends.WithProviderDefinitions([]backends.ProviderDefinition{def}))
					prepared, err := settings.PrepareTargetCached(t.Context(), engine.ProviderSpec{Name: "litellm", APIKey: "loopback-only"})
					if err != nil {
						t.Fatal(err)
					}
					config := targetCardConfig(t, engine.RateCardTarget{Provider: "litellm", Model: parentAlias, Protocol: llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS}, "v1")
					prepared, err = engine.BindTargetRateCard(prepared, config)
					if err != nil {
						t.Fatal(err)
					}
					want := prepared.RateCard.Clone()
					workspace, err := code.NewWorkspace(settings.WorkspaceRoot())
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = workspace.Close() })
					sink := &childRateCardSink{}
					live := engine.NewLiveRunner(prepared.Provider, workspace, prepared.Spec.Model, engine.WithSettings(settings), engine.WithLiveSink(sink))
					testCtx := t.Context()
					t.Cleanup(func() {
						ctx, cancel := context.WithTimeout(context.WithoutCancel(testCtx), 5*time.Second)
						defer cancel()
						if err := live.Close(ctx); err != nil {
							t.Error(err)
						}
					})
					live.ApplyTarget(prepared)
					live.SetLimits(1024, 6, 2, 1)
					unchanged, err := live.PrepareChildTarget(t.Context(), catalog.Agent{Name: "same"})
					if err != nil || !reflect.DeepEqual(unchanged.RateCard, &want) || unchanged.RateCard == prepared.RateCard {
						t.Fatal("unchanged child lost cloned basis", err)
					}
					unchanged.RateCard.CacheWrites[0].Rate.PerMillionTokens = 99
					override, err := live.PrepareChildTarget(t.Context(), catalog.Agent{Name: "other", Model: "different alias"})
					if err != nil || override.RateCard != nil || override.Facts.Resolution.Descriptor.RateCard != (llm.RateCardReference{}) {
						t.Fatal("changed child borrowed basis", err)
					}
					ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
					defer cancel()
					done := make(chan error, 1)
					go func() {
						done <- live.RunRecordedTurn(ctx, "Delegate a read-only fixture inspection and summarize it.", nil, store, "session")
					}()
					joined := false
					defer func() {
						cancel()
						unblock()
						if !joined {
							<-done
						}
					}()
					select {
					case <-started:
					case err := <-done:
						joined = true
						t.Fatalf("ended before child dispatch: %v", err)
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					}
					// The invoked child retains v1 independently of caller/snapshot
					// mutation. Configuration is not consulted at settlement.
					prepared.RateCard.Reference.Revision = "caller"
					prepared.RateCard.CacheWrites[0].Rate.PerMillionTokens = 88
					capture := live.RunTarget()
					capture.RateCard.Reference.Revision = "snapshot"
					capture.RateCard.CacheWrites[0].Rate.PerMillionTokens = 77
					if err := settings.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeySpawnDefaultExploreModel, "after-construction"); err != nil {
						t.Fatal(err)
					}
					unblock()
					err = <-done
					joined = true
					if err != nil {
						t.Fatal(err)
					}
					if childCalls.Load() != 1 {
						t.Fatalf("actual child invocations=%d", childCalls.Load())
					}
					assertRecordedChildTarget(t, live, routing, childAlias, parentAlias, changed, want)
					sink.mu.Lock()
					defer sink.mu.Unlock()
					found := false
					for _, event := range sink.events {
						if event.Depth != 1 {
							continue
						}
						found = true
						if (changed || routing == "default") && routing != "fallback" {
							if event.RateCard != nil {
								t.Fatal("changed child's actual invocation inherited parent pricing")
							}
						} else if !reflect.DeepEqual(event.RateCard, &want) {
							t.Fatal("actual child handoff lost frozen v1 basis")
						}
					}
					if !found {
						t.Fatal("no settled child invocation observed")
					}
				})
			}
		})
	}
}

func assertRecordedChildTarget(t *testing.T, live *engine.LiveRunner, routing, childAlias, parentAlias string, changed bool, want llm.RateCard) {
	t.Helper()
	_, roots, childRows, err := live.RecordedHistoryWithAccounting("session")
	if err != nil || len(childRows) != 1 || len(roots) < 2 {
		t.Fatal("combined actual dispatch", err)
	}
	captured, err := engine.DecodeRecordedChildProviderAccounting(childRows[0].PayloadJSON)
	expectedModel := childAlias
	if routing == "fallback" {
		expectedModel = parentAlias
	}
	if err != nil || captured.Model != expectedModel || captured.Provider != "litellm" || captured.OwnerRequestBatchID != roots[0].RequestBatchID {
		t.Fatal("child labeled from mutable/root target", err)
	}
	if (changed || routing == "default") && routing != "fallback" {
		if captured.RateCard != nil {
			t.Fatal("changed child borrowed parent card")
		}
	} else if !reflect.DeepEqual(captured.RateCard, &want) {
		t.Fatal("child lost frozen card")
	}
}
