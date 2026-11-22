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

func TestLatestCodexPresets(t *testing.T) {
	want := []string{"gpt-6.2-astra", "gpt-6.2-sol", "gpt-6.1-terra"}
	models := openaicodex.ListPresetModels()
	for i, id := range want {
		if models[i].ID != id || models[i].MaxTokens != openaicodex.DefaultContextWindow {
			t.Fatalf("preset %d = %+v, want %s at conservative budget", i, models[i], id)
		}
		if caps := models[i].Capabilities; !caps.SupportsThinking || !caps.SupportsVision || !caps.SupportsTools || caps.SupportsVideo {
			t.Fatalf("%s capabilities = %+v", id, caps)
		}
		if got := openaicodex.EffortVariants(id); !slices.Equal(got, []string{"low", "medium", "high", "xhigh"}) {
			t.Fatalf("%s efforts = %v", id, got)
		}
		for _, effort := range []string{"low", "medium", "high", "xhigh"} {
			alias := id + "-" + effort
			if got := openaicodex.BaseModelFor(alias); got != id {
				t.Fatalf("BaseModelFor(%s) = %s", alias, got)
			}
			if got := openaicodex.ContextWindowFor(alias); got != openaicodex.DefaultContextWindow {
				t.Fatalf("ContextWindowFor(%s) = %d", alias, got)
			}
		}
		if got := openaicodex.BaseModelFor(id + "-max"); got != id+"-max" {
			t.Fatalf("unsupported max suffix resolved: %s", got)
		}
	}
}

func TestLatestCodexWireEfforts(t *testing.T) {
	for _, model := range []string{"gpt-6.2-astra", "gpt-6.2-sol", "gpt-6.1-terra"} {
		for _, effort := range []string{"low", "medium", "high", "xhigh", "max"} {
			t.Run(model+"/"+effort, func(t *testing.T) {
				var body struct {
					Model     string `json:"model"`
					Reasoning *struct {
						Effort  string `json:"effort"`
						Summary string `json:"summary"`
					} `json:"reasoning"`
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Errorf("decode: %v", err)
					}
					_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{}}}\n\n")
				}))
				t.Cleanup(server.Close)
				p := openaicodex.NewProvider(openaicodex.StaticTokenSource{T: freshToken(t, "acct_test")},
					openaicodex.WithBaseURL(server.URL), openaicodex.WithNoRetry(), openaicodex.WithModel(model))
				for _, err := range p.Complete(t.Context(), llm.CompletionRequest{
					Messages: []llm.Message{{Role: llm.RoleUser, Content: "hello"}},
					Options:  llm.ModelOptions{"reasoning_effort": effort},
				}) {
					if err != nil {
						t.Fatal(err)
					}
				}
				if body.Model != model {
					t.Fatalf("wire model = %s", body.Model)
				}
				if effort == "max" {
					if body.Reasoning != nil {
						t.Fatalf("unsupported effort forwarded: %+v", body.Reasoning)
					}
				} else if body.Reasoning == nil || body.Reasoning.Effort != effort || body.Reasoning.Summary != "detailed" {
					t.Fatalf("wire reasoning = %+v", body.Reasoning)
				}
			})
		}
	}
}

func TestLatestCodexLiveBudgetOverridesFallback(t *testing.T) {
	for _, model := range []string{"gpt-6.2-astra", "gpt-6.2-sol", "gpt-6.1-terra"} {
		t.Run(model, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/codex/models" {
					t.Errorf("path = %s", r.URL.Path)
				}
				_, _ = fmt.Fprintf(w, `{"models":[{"slug":%q,"context_window":1000000,"auto_compact_token_limit":420000}]}`, model)
			}))
			t.Cleanup(server.Close)
			got, err := openaicodex.FetchContextWindow(t.Context(), openaicodex.StaticTokenSource{T: freshToken(t, "acct_test")}, server.URL, model+"-high")
			if err != nil || got != 420_000 {
				t.Fatalf("live budget = %d, %v", got, err)
			}
		})
	}
}
