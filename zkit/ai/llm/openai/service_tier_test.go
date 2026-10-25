package openai_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
)

func TestServiceTierWireOptions(t *testing.T) {
	t.Parallel()
	for _, endpoint := range []struct {
		name, model, path string
		stream            bool
	}{
		{"responses", "gpt-6.1-sol", "/responses", true},
		{"chat stream", "gpt-4o", "/chat/completions", true},
		{"chat nonstream", "gpt-6.1-sol", "/chat/completions", false},
	} {
		for _, tc := range []struct {
			name  string
			value any
			want  string
		}{
			{"unset", nil, ""}, {"auto", "auto", ""},
			{"standard", "default", "default"}, {"fast", "fast", "fast"},
			{"priority alias", "priority", "priority"}, {"ultrafast", "ultrafast", "ultrafast"},
			{"invalid", "turbo", ""}, {"non-string", true, ""},
		} {
			t.Run(endpoint.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if r.URL.Path != endpoint.path || body["model"] != endpoint.model {
						t.Errorf("request = %s %v", r.URL.Path, body)
					}
					got, present := body["service_tier"]
					if tc.want == "" && present || tc.want != "" && got != tc.want {
						t.Errorf("service_tier = %v (present %v), want %q", got, present, tc.want)
					}
					switch {
					case endpoint.path == "/responses":
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{}}}\n\n"))
					case endpoint.stream:
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = w.Write([]byte("data: [DONE]\n\n"))
					default:
						w.Header().Set("Content-Type", "application/json")
						_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
					}
				}))
				t.Cleanup(server.Close)
				p := openai.NewProvider("test", openai.WithBaseURL(server.URL), openai.WithModel(endpoint.model), openai.WithResponsesAPI(true))
				opts := llm.ModelOptions{"text_verbosity": "high"}
				if tc.value != nil {
					opts["service_tier"] = tc.value
				}
				before := opts.Clone()
				for _, err := range p.Complete(t.Context(), llm.CompletionRequest{Stream: endpoint.stream, Options: opts}) {
					if err != nil {
						t.Fatal(err)
					}
				}
				if !reflect.DeepEqual(opts, before) {
					t.Fatalf("borrowed options mutated: %v", opts)
				}
			})
		}
	}
}
