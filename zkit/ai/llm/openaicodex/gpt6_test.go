package openaicodex_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openaicodex"
)

func TestGPT6SolLunaPresetsAndWireEfforts(t *testing.T) {
	t.Parallel()
	for _, model := range []string{"gpt-6.1-sol", "gpt-6-sol", "gpt-6-luna"} {
		t.Run(model, func(t *testing.T) {
			t.Parallel()
			wantEfforts := []string{"low", "medium", "high", "xhigh", "max"}
			if got := openaicodex.EffortVariants(model); !slices.Equal(got, wantEfforts) {
				t.Fatalf("efforts = %v, want %v", got, wantEfforts)
			}
			models := openaicodex.ListPresetModels()
			idx := slices.IndexFunc(models, func(m llm.Model) bool { return m.ID == model })
			if idx < 0 || models[idx].MaxTokens != 1_050_000 {
				t.Fatalf("missing preset/context for %s: %v", model, models)
			}
			if caps := models[idx].Capabilities; !caps.SupportsVision || caps.SupportsVideo {
				t.Fatalf("preset capabilities = %+v, want images but not video", caps)
			}
			for _, effort := range append([]string{""}, wantEfforts...) {
				t.Run("effort="+effort, func(t *testing.T) {
					t.Parallel()
					id := model
					if effort != "" {
						id += "-" + effort
					}
					if got := openaicodex.ContextWindowFor(id); got != 1_050_000 {
						t.Fatalf("context = %d", got)
					}
					caps := openaicodex.Capabilities(id)
					if !caps.SupportsThinking || !caps.SupportsVision || !caps.SupportsTools || caps.SupportsVideo {
						t.Fatalf("capabilities = %+v", caps)
					}
					backend := newCodexBackend(t, func(w http.ResponseWriter) {
						_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{}}}\n\n")
					})
					t.Cleanup(backend.Close)
					provider := openaicodex.NewProvider(
						openaicodex.StaticTokenSource{T: freshToken(t, "acct_test")},
						openaicodex.WithBaseURL(backend.srv.URL), openaicodex.WithNoRetry(), openaicodex.WithModel(id),
					)
					for _, err := range provider.Complete(t.Context(), llm.CompletionRequest{
						Stream: true, Messages: []llm.Message{{Role: llm.RoleUser, Content: "hello"}},
						Tools: []llm.Tool{{Type: "function", Function: llm.ToolFunction{Name: "read", Parameters: llm.SchemaFromMap(map[string]any{"type": "object"})}}},
					}) {
						if err != nil {
							t.Fatal(err)
						}
					}
					if got := backend.lastBody["model"]; got != model {
						t.Fatalf("wire model = %v, want %s", got, model)
					}
					if effort != "" {
						reasoning, _ := backend.lastBody["reasoning"].(map[string]any)
						if reasoning["effort"] != effort {
							t.Fatalf("wire reasoning = %v, want %s", reasoning, effort)
						}
					}
					if backend.lastBody["tool_choice"] != "auto" {
						t.Fatalf("tools not enabled: %v", backend.lastBody)
					}
				})
			}
		})
	}
}

func TestGPT6SolLunaLiveContextOverridesStaticPreset(t *testing.T) {
	t.Parallel()
	for _, model := range []string{"gpt-6.1-sol", "gpt-6-sol", "gpt-6-luna"} {
		for _, limit := range []int{800_000, 1_000_000, 0} {
			t.Run(fmt.Sprintf("%s/limit=%d", model, limit), func(t *testing.T) {
				t.Parallel()
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/codex/models" {
						t.Errorf("path = %s", r.URL.Path)
					}
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]any{"models": []map[string]any{{
						"slug": model, "context_window": 1_050_000, "auto_compact_token_limit": limit,
					}}})
				}))
				t.Cleanup(server.Close)
				tokens := openaicodex.StaticTokenSource{T: freshToken(t, "acct_test")}
				got, err := openaicodex.FetchContextWindow(t.Context(), tokens, server.URL, model+"-high")
				want := limit
				if want == 0 {
					want = 945_000 // 90% fallback only when no explicit pressure threshold is provided
				}
				if err != nil || got != want {
					t.Fatalf("live context = %d, %v; want %d", got, err, want)
				}
				models, err := openaicodex.FetchModels(t.Context(), tokens, server.URL)
				if err != nil || len(models) != 1 {
					t.Fatalf("live models = %v, %v", models, err)
				}
				if caps := models[0].Capabilities; !caps.SupportsVision || caps.SupportsVideo {
					t.Fatalf("live capabilities = %+v, want images but not video", caps)
				}
			})
		}
	}
}

func TestGPT61SolOmitsUnsupportedReasoningEfforts(t *testing.T) {
	t.Parallel()
	for _, effort := range []string{"none", "minimal"} {
		for _, source := range []string{"request", "provider"} {
			t.Run(effort+"/"+source, func(t *testing.T) {
				t.Parallel()
				backend := newCodexBackend(t, func(w http.ResponseWriter) {
					_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{}}}\n\n")
				})
				t.Cleanup(backend.Close)
				defaultEffort := ""
				req := llm.CompletionRequest{Stream: true}
				if source == "provider" {
					defaultEffort = effort
				} else {
					req.Options = llm.ModelOptions{"reasoning_effort": effort}
				}
				p := openaicodex.NewProvider(
					openaicodex.StaticTokenSource{T: freshToken(t, "acct_test")},
					openaicodex.WithBaseURL(backend.srv.URL), openaicodex.WithNoRetry(),
					openaicodex.WithModel("gpt-6.1-sol"), openaicodex.WithDefaultReasoningEffort(defaultEffort),
				)
				for _, err := range p.Complete(t.Context(), req) {
					if err != nil {
						t.Fatal(err)
					}
				}
				if got := backend.lastBody["model"]; got != "gpt-6.1-sol" {
					t.Fatalf("wire model = %v", got)
				}
				if reasoning, _ := backend.lastBody["reasoning"].(map[string]any); reasoning["effort"] != nil {
					t.Fatalf("unsupported effort should be omitted: %v", reasoning)
				}
			})
		}
	}
}
