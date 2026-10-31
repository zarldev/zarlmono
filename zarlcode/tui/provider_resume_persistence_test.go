package tui_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/llm/claudecode"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestOrdinaryProviderReasoningSurvivesUIStoreRestart(t *testing.T) {
	for _, route := range []string{"llamacpp", "ollama", "deepseek", "gemini", "claude-code"} {
		t.Run(route, func(t *testing.T) {
			var requests []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				requests = append(requests, string(raw))
				w.Header().Set("Content-Type", "text/event-stream")
				response := `{"choices":[{"index":0,"delta":{"reasoning_content":"retained private thinking","content":"endpoint answer"},"finish_reason":"stop"}]}`
				if route == "gemini" {
					response = `{"candidates":[{"content":{"role":"model","parts":[{"text":"retained private thinking","thought":true},{"text":"endpoint answer"}]},"finishReason":"STOP"}]}`
				}
				_, _ = io.WriteString(w, "data: "+response+"\n\n")
				if route != "gemini" {
					_, _ = io.WriteString(w, "data: [DONE]\n\n")
				}
			}))
			t.Cleanup(server.Close)
			dir := t.TempDir()
			promptPath := filepath.Join(dir, "prompt")
			binary := filepath.Join(dir, "claude")
			if route == "claude-code" {
				t.Setenv("CLAUDE_TEST_PROMPT", promptPath)
				script := "#!/bin/sh\ncat > \"$CLAUDE_TEST_PROMPT\"\nprintf '%s\\n' '" + `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"retained private thinking"},{"type":"text","text":"endpoint answer"}]}}` + "'\n"
				if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			ws, err := code.NewWorkspace(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = ws.Close() })
			path := filepath.Join(dir, "state.db")
			var sessionID string
			for phase := range 2 {
				func() {
					store, err := db.Open(t.Context(), path)
					if err != nil {
						t.Fatal(err)
					}
					defer func() { _ = store.Close() }()
					var provider llm.Provider
					if route == "claude-code" {
						provider = claudecode.NewProvider(claudecode.StaticTokenSource{T: claudecode.Token{Access: "fixture-token"}}, claudecode.WithBinaryPath(binary))
					} else {
						provider, err = backends.NewRegistry().BuildWithConfig(t.Context(), route, backends.BuildConfig{BaseURL: server.URL, Model: "fixture", APIKey: "fixture-key"})
						if err != nil {
							t.Fatal(err)
						}
					}
					f := &beforeFixture{store: store, ws: ws}
					f.sink = teasink.New(func(msg tea.Msg) { f.mu.Lock(); f.events = append(f.events, msg); f.mu.Unlock() })
					defer f.sink.Close()
					f.live = engine.NewLiveRunner(provider, ws, "fixture", engine.WithLiveSink(f.sink))
					defer func() {
						if err := f.live.Close(context.WithoutCancel(t.Context())); err != nil {
							t.Error(err)
						}
					}()
					spec := engine.ProviderSpec{Name: route, Model: "fixture", BaseURL: server.URL}
					f.live.SetProviderSpec(provider, spec)
					settings := engine.NewSettings(store, nil, nil, ws.Root())
					settings.Registry = nil // fixtures reconstruct real adapters without external credentials/network discovery
					f.ui = tui.New()
					f.ui.SetLiveRunner(f.live)
					f.ui.SetLiveEventSink(f.sink)
					f.ui.SetSettings(settings)
					f.ui.SetProviderContext(spec, spec)
					f.ui.SetStartupReady(true)
					if phase == 1 {
						record, err := store.GetSession(t.Context(), sessionID)
						if err != nil {
							t.Fatal(err)
						}
						if !strings.HasPrefix(string(record.ContextJSON), "[") || !strings.Contains(string(record.ContextJSON), "retained private thinking") {
							t.Fatal("ordinary context was promoted or reasoning erased")
						}
						// Corrupt a saved copy after reopening. Rejection must publish nothing,
						// change no active preference, and expose no payload in diagnostics.
						corrupt := record
						corrupt.ID = "corrupt"
						corrupt.ContextJSON = []byte(`[{"role":"assistant","content":"PRIVATE-canary","continuation_items":[{"provider":"foreign","format":"bad","kind":"bad","data":"e30="}]}]`)
						if err := store.SaveSession(t.Context(), corrupt); err != nil {
							t.Fatal(err)
						}
						activeBefore, err := store.GetSettingExact(t.Context(), ws.Root(), "active_session")
						if err != nil {
							t.Fatal(err)
						}
						before := f.live.ContextSnapshot()
						if err := f.ui.ResumeSavedSession(t.Context(), "corrupt"); !errors.Is(err, rewind.ErrInvalid) || strings.Contains(err.Error(), "PRIVATE-canary") {
							t.Fatalf("unsafe corrupt resume error: %v", err)
						}
						activeAfter, err := store.GetSettingExact(t.Context(), ws.Root(), "active_session")
						if err != nil || activeBefore != activeAfter || !reflect.DeepEqual(before, f.live.ContextSnapshot()) || !f.ui.CanonicalThread().IsEmpty() {
							t.Fatal("rejection mutated active state")
						}
						if err := f.ui.ResumeSavedSession(t.Context(), sessionID); err != nil {
							t.Fatal(err)
						}
						foundReasoning := false
						for _, entry := range f.ui.CanonicalThread().Entries() {
							if entry.Kind == transcript.EntryKinds.ENTRYREASONING && entry.Payload.Text == "retained private thinking" {
								foundReasoning = true
							}
						}
						if !foundReasoning {
							t.Fatal("reasoning not restored to transcript")
						}
					}
					prompt := "first persisted prompt"
					if phase == 1 {
						prompt = "second prompt after restart"
					}
					settleRewindTurn(t, f, prompt)
					sessionID = f.ui.SessionIdentity()
					if sessionID == "" {
						t.Fatalf("session not saved: %s", f.ui.ToastText())
					}
				}()
			}
			var outbound string
			if route == "claude-code" {
				raw, err := os.ReadFile(promptPath)
				if err != nil {
					t.Fatal(err)
				}
				outbound = string(raw)
			} else {
				if len(requests) != 2 {
					t.Fatalf("requests=%d, want 2", len(requests))
				}
				outbound = requests[1]
			}
			for _, text := range []string{"first persisted prompt", "endpoint answer", "second prompt after restart"} {
				if strings.Count(outbound, text) != 1 {
					t.Fatalf("lost or duplicated %q in continued request", text)
				}
			}
			if (route == "gemini" || route == "claude-code") && strings.Contains(outbound, "retained private thinking") {
				t.Fatal("display-only reasoning sent outbound")
			}
		})
	}
}
