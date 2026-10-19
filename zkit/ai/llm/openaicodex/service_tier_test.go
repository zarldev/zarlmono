package openaicodex_test

import (
	"io"
	"net/http"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openaicodex"
)

func TestServiceTierWireOptions(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		value any
		want  string
	}{
		{"unset", nil, ""}, {"auto", "auto", ""},
		{"standard", "default", "default"}, {"fast", "fast", "priority"},
		{"priority", "priority", "priority"}, {"ultrafast", "ultrafast", "ultrafast"},
		{"invalid", "turbo", ""}, {"non-string", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			backend := newCodexBackend(t, func(w http.ResponseWriter) {
				_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{}}}\n\n")
			})
			t.Cleanup(backend.Close)
			p := openaicodex.NewProvider(
				openaicodex.StaticTokenSource{T: freshToken(t, "acct_test")},
				openaicodex.WithBaseURL(backend.srv.URL), openaicodex.WithNoRetry(),
				openaicodex.WithModel("gpt-6-astra"),
			)
			opts := llm.ModelOptions{"text_verbosity": "high", "reasoning_effort": "max"}
			if tc.value != nil {
				opts["service_tier"] = tc.value
			}
			before := opts.Clone()
			for _, err := range p.Complete(t.Context(), llm.CompletionRequest{Stream: true, Options: opts}) {
				if err != nil {
					t.Fatal(err)
				}
			}
			got, present := backend.lastBody["service_tier"]
			if tc.want == "" && present || tc.want != "" && got != tc.want {
				t.Errorf("service_tier = %v (present %v), want %q", got, present, tc.want)
			}
			text, _ := backend.lastBody["text"].(map[string]any)
			reasoning, _ := backend.lastBody["reasoning"].(map[string]any)
			if text["verbosity"] != "high" || reasoning["effort"] != "max" {
				t.Errorf("speed changed other options: %v", backend.lastBody)
			}
			if !reflect.DeepEqual(opts, before) {
				t.Fatalf("borrowed options mutated: %v", opts)
			}
		})
	}
}
