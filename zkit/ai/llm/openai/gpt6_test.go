package openai_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
	"github.com/zarldev/zarlmono/zkit/options"
)

func TestReasoningNoneIsModelGatedAndPreservesResponsesOptions(t *testing.T) {
	t.Parallel()
	for _, model := range []string{"gpt-6.1-sol", "gpt-6-sol", "gpt-6-luna", "gpt-6-astra", "gpt-5.6"} {
		for _, source := range []string{"request", "provider"} {
			t.Run(model+"/"+source, func(t *testing.T) {
				t.Parallel()
				var body map[string]any
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/responses" {
						t.Errorf("path = %s, want /responses", r.URL.Path)
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{}}}\n\n"))
				}))
				t.Cleanup(server.Close)
				opts := []options.Option[openai.Provider]{openai.WithBaseURL(server.URL), openai.WithModel(model), openai.WithResponsesAPI(true)}
				req := llm.CompletionRequest{Stream: true, Options: llm.ModelOptions{
					"text_verbosity": "high", "prompt_cache_key": "task-key", "prompt_cache_retention": "24h",
				}}
				req.Thinking.Enabled = true
				if source == "provider" {
					opts = append(opts, openai.WithDefaultReasoningEffort(openai.ReasoningEfforts.REASONINGEFFORTNONE))
				} else {
					req.Options["reasoning_effort"] = "none"
				}
				p := openai.NewProvider("test", opts...)
				for _, err := range p.Complete(t.Context(), req) {
					if err != nil {
						t.Fatal(err)
					}
				}
				if body["model"] != model {
					t.Fatalf("wire model = %v, want %s", body["model"], model)
				}
				want := "medium"
				if model == "gpt-6-sol" || model == "gpt-6-luna" {
					want = "none"
				}
				reasoning, _ := body["reasoning"].(map[string]any)
				if reasoning["effort"] != want {
					t.Fatalf("reasoning = %v, want %s", reasoning, want)
				}
				text, _ := body["text"].(map[string]any)
				if text["verbosity"] != "high" || body["prompt_cache_key"] != "task-key" || body["prompt_cache_retention"] != "24h" {
					t.Fatalf("Responses options not preserved: %v", body)
				}
			})
		}
	}
}

func TestGPT61SolOmitsUnsupportedReasoningEfforts(t *testing.T) {
	t.Parallel()
	for _, effort := range []string{"none", "minimal"} {
		t.Run(effort, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if r.URL.Path != "/responses" || body["model"] != "gpt-6.1-sol" {
					t.Errorf("unexpected request: %s %v", r.URL.Path, body)
				}
				if reasoning, _ := body["reasoning"].(map[string]any); reasoning["effort"] != nil {
					t.Errorf("unsupported effort should be omitted: %v", reasoning)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{}}}\n\n"))
			}))
			t.Cleanup(server.Close)
			p := openai.NewProvider("test", openai.WithBaseURL(server.URL), openai.WithModel("gpt-6.1-sol"), openai.WithResponsesAPI(true))
			for _, err := range p.Complete(t.Context(), llm.CompletionRequest{
				Stream: true, Options: llm.ModelOptions{"reasoning_effort": effort},
			}) {
				if err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
