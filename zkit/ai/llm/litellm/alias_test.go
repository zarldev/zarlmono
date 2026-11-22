package litellm_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/litellm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/providertest"
)

func TestOpaqueAliasesDoNotSelectProtocols(t *testing.T) {
	t.Parallel()
	for _, fixture := range fixtures() {
		for _, model := range []string{"gpt-5.6", "claude-opus-4-7", "Gemini/Case-Sensitive Alias", "models/Team-A", "tunedModels/Team-B", "Company/x?y&z#v%1", "Company/../Other", "Company/v1beta/Other"} {
			t.Run(fixture.protocol.String()+"/"+model, func(t *testing.T) {
				t.Parallel()
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					wantPath := "/proxy" + fixture.path
					if fixture.protocol == llm.ModelProtocols.PROTOCOLGENERATECONTENT {
						wantPath = "/proxy/v1beta/models/" + model + ":streamGenerateContent"
					}
					if r.URL.Path != wantPath {
						t.Errorf("alias path = %q, want %q", r.URL.Path, wantPath)
					}
					if fixture.protocol != llm.ModelProtocols.PROTOCOLGENERATECONTENT {
						var body map[string]any
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Error(err)
						}
						if body["model"] != model {
							t.Errorf("alias = %#v, want %q", body["model"], model)
						}
					} else if r.URL.Query().Get("alt") != "sse" || len(r.URL.Query()) != 1 {
						t.Error("alias escaped into query")
					}
					fixture.serve(w, true, true)
				}))
				t.Cleanup(server.Close)
				p := litellm.NewProvider("ordinary-test-key", litellm.WithBaseURL(server.URL+"/proxy"), litellm.WithModel(model), litellm.WithProtocol(fixture.protocol))
				assistant, _ := collect(t, p.Complete(t.Context(), providertest.SimpleRequest("hi")))
				if assistant.Content != "done" {
					t.Errorf("content = %q", assistant.Content)
				}
			})
		}
	}
}
