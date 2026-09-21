package tui_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openaicodex"
)

func TestLiveTurnCodexReplaySettlement(t *testing.T) {
	for _, tc := range []struct {
		name         string
		nativeText   bool
		paragraphs   bool
		rawReasoning bool
		firstIndex   int
	}{
		{name: "native text", nativeText: true},
		{name: "summary paragraphs", paragraphs: true},
		{name: "sparse positions", firstIndex: 3},
		{name: "combined", nativeText: true, paragraphs: true, firstIndex: 3},
		{name: "raw reasoning differs from native summary", nativeText: true, rawReasoning: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newBeforeFixture(t)
			summary := `[{"type":"summary_text","text":"Considering"}]`
			if tc.paragraphs {
				summary = `[{"type":"summary_text","text":"Considering"},{"type":"summary_text","text":"Checking"}]`
			}
			reasoning := `{"type":"reasoning","id":"rs_1","encrypted_content":"opaque","summary":` + summary + `}`
			const answer = `{"type":"message","id":"msg_1","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"answer"}]}`
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
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
					want := []string{reasoning}
					if tc.nativeText {
						want = append(want, answer)
					}
					if !reflect.DeepEqual(native, want) {
						t.Error("replay lost, duplicated or reordered native items")
					}
					// Two user prompts plus reasoning and exactly one answer.
					if len(request.Input) != 4 {
						t.Errorf("replay input count=%d, want 4", len(request.Input))
					}
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if tc.rawReasoning {
					_, _ = fmt.Fprint(w, "data: {\"type\":\"response.reasoning_text.delta\",\"delta\":\"Detailed reasoning, not the summary\"}\n\n")
				} else {
					_, _ = fmt.Fprint(w, "data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"Considering\"}\n\n")
				}
				if tc.paragraphs {
					_, _ = fmt.Fprint(w, "data: {\"type\":\"response.reasoning_summary_part.added\"}\n\ndata: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"Checking\"}\n\n")
				}
				_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.output_item.done\",\"output_index\":%d,\"item\":%s}\n\n", tc.firstIndex, reasoning)
				_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.output_text.delta\",\"output_index\":%d,\"delta\":\"answer\"}\n\n", tc.firstIndex+1)
				if tc.nativeText {
					_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.output_item.done\",\"output_index\":%d,\"item\":%s}\n\n", tc.firstIndex+1, answer)
				}
				_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{}}}\n\n")
			}))
			t.Cleanup(server.Close)
			provider := openaicodex.NewProvider(
				openaicodex.StaticTokenSource{T: openaicodex.Token{Access: "test", AccountID: "test", Expires: time.Now().Add(time.Hour)}},
				openaicodex.WithBaseURL(server.URL), openaicodex.WithNoRetry(),
			)
			f.live.SetProviderSpec(provider, engine.ProviderSpec{Name: "openai-codex", Model: "gpt-5.6"})
			for _, prompt := range []string{"first", "second"} {
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
					t.Fatalf("completed turn was not saved exactly: %s", f.ui.ToastText())
				}
				if strings.Contains(f.ui.ToastText(), "turn not saved") {
					t.Fatal(f.ui.ToastText())
				}
			}
			if calls.Load() != 2 {
				t.Fatalf("provider calls=%d, want 2", calls.Load())
			}
		})
	}
}
