package tui_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
)

type settlementTransport func(*http.Request) (*http.Response, error)

func (transport settlementTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestLiveTurnResponsesSettlementAllowsSecondSubmit(t *testing.T) {
	for _, rawReasoning := range []bool{false, true} {
		name := "summary"
		if rawReasoning {
			name = "raw reasoning differs from native summary"
		}
		t.Run(name, func(t *testing.T) {
			testLiveTurnResponsesSettlement(t, rawReasoning, "answer")
		})
	}
	t.Run("native text whitespace", func(t *testing.T) {
		testLiveTurnResponsesSettlement(t, false, "\n  answer\n\n")
	})
}

func testLiveTurnResponsesSettlement(t *testing.T, rawReasoning bool, text string) {
	t.Helper()
	f := newBeforeFixture(t)
	const reasoning = `{"type":"reasoning","id":"rs_1","encrypted_content":"opaque-private-canary","summary":[{"type":"summary_text","text":"Considering"}]}`
	answer := fmt.Sprintf(`{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":%q}]}`, text)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if !strings.HasSuffix(r.URL.Path, "/responses") {
			t.Errorf("expected Responses route, got %s", r.URL.Path)
		}
		id, err := f.store.GetSettingExact(r.Context(), f.ws.Root(), "active_session")
		if err != nil {
			t.Error(err)
		}
		checkpoints, err := f.store.ListSessionCheckpoints(r.Context(), id)
		if err != nil || len(checkpoints) != int(n) {
			t.Errorf("dispatch %d without durable BEFORE: count=%d err=%v", n, len(checkpoints), err)
		}
		var request struct {
			Input []json.RawMessage `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if n == 2 {
			var native []string
			for _, item := range request.Input {
				var probe struct {
					ID string `json:"id"`
				}
				if err := json.Unmarshal(item, &probe); err != nil {
					t.Error(err)
				}
				if probe.ID != "" {
					native = append(native, string(item))
				}
			}
			if !reflect.DeepEqual(native, []string{reasoning, answer}) {
				t.Error("second request lost, duplicated or reordered native continuation")
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		reasoningEvent := "data: {\"type\":\"response.reasoning_summary_text.delta\",\"output_index\":0,\"delta\":\"Considering\"}\n\n"
		if rawReasoning {
			reasoningEvent = "data: {\"type\":\"response.reasoning_text.delta\",\"output_index\":0,\"delta\":\"Detailed reasoning, not the summary\"}\n\n"
		}
		_, _ = w.Write([]byte(reasoningEvent +
			fmt.Sprintf("data: {\"type\":\"response.output_text.delta\",\"output_index\":1,\"delta\":%q}\n\n", text) +
			"data: {\"type\":\"response.completed\",\"response\":{\"output\":[" + reasoning + "," + answer + "],\"usage\":{}}}\n\n"))
	}))
	t.Cleanup(server.Close)
	localURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: settlementTransport(func(request *http.Request) (*http.Response, error) {
		request = request.Clone(request.Context())
		request.URL.Scheme, request.URL.Host = localURL.Scheme, localURL.Host
		return server.Client().Transport.RoundTrip(request)
	})}
	// The registry explicitly passes the stock URL; it must retain Responses.
	provider := openai.NewProvider("test-key", openai.WithHTTPClient(client), openai.WithBaseURL("https://api.openai.com/v1"), openai.WithModel("gpt-5.6"))
	f.live.SetProviderSpec(provider, engine.ProviderSpec{Name: "openai", Model: "gpt-5.6"})
	for _, prompt := range []string{"first prompt", "second prompt"} {
		settleRewindTurn(t, f, prompt)
		stored, err := f.store.GetSessionResumeState(t.Context(), f.ui.SessionIdentity())
		if err != nil {
			t.Fatal(err)
		}
		resume, err := rewind.DecodeResume(stored.Session.ContextJSON)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(resume.Context, f.live.ContextSnapshot()) {
			t.Fatalf("idle turn has no durable exact head; toast=%s", f.ui.ToastText())
		}
		if strings.Contains(f.ui.ToastText(), "settle and save") {
			t.Fatal("idle turn remains blocked")
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("provider calls=%d, want 2", calls.Load())
	}
}
