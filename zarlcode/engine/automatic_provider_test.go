package engine_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/llm/claudecode"
	"github.com/zarldev/zarlmono/zkit/ai/llm/deepseek"
	"github.com/zarldev/zarlmono/zkit/ai/llm/llamacpp"
	"github.com/zarldev/zarlmono/zkit/ai/llm/ollama"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestAutomaticCompletionPromptForConstructedAdaptersAndChildren(t *testing.T) {
	for _, route := range []string{"registry-llamacpp", "registry-ollama", "registry-deepseek", "direct-llamacpp", "direct-ollama", "direct-deepseek"} {
		t.Run(route, func(t *testing.T) {
			var rootCalls atomic.Int32
			var childSeen atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				if !strings.Contains(string(raw), "Completed child results arrive automatically") {
					t.Error("constructed provider omitted automatic-completion prompt")
				}
				if strings.Contains(string(raw), "child fixture work") && !strings.Contains(string(raw), `\"prompt\"`) {
					childSeen.Store(true)
					completionFixture(w, "Child evidence is ready.", "")
					return
				}
				if rootCalls.Add(1) == 1 {
					completionFixture(w, "", `{"prompt":"child fixture work"}`)
				} else {
					completionFixture(w, "The evidence is incorporated.", "")
				}
			}))
			t.Cleanup(server.Close)
			var provider llm.Provider
			if strings.HasPrefix(route, "registry-") {
				var err error
				provider, err = backends.NewRegistry().BuildWithConfig(t.Context(), strings.TrimPrefix(route, "registry-"), backends.BuildConfig{BaseURL: server.URL, Model: "fixture-model", APIKey: "fixture-key"})
				if err != nil {
					t.Fatal(err)
				}
			} else {
				switch route {
				case "direct-llamacpp":
					provider = llamacpp.NewProvider(llamacpp.WithBaseURL(server.URL))
				case "direct-ollama":
					provider = ollama.NewProvider(ollama.WithBaseURL(server.URL))
				case "direct-deepseek":
					provider = deepseek.NewProvider("fixture-key", deepseek.WithBaseURL(server.URL))
				}
			}
			ws, err := code.NewWorkspace(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = ws.Close() })
			live := engine.NewLiveRunner(provider, ws, "fixture-model")
			t.Cleanup(func() {
				if err := live.Close(context.WithoutCancel(t.Context())); err != nil {
					t.Error(err)
				}
			})
			live.SetLimits(1024, 6, 2, 2)
			if err := live.RunTurn(t.Context(), "Delegate this read-only investigation to a child and summarize the evidence."); err != nil {
				t.Fatal(err)
			}
			if !childSeen.Load() {
				t.Fatal("child runner was not exercised")
			}
		})
	}
}

func TestAutomaticCompletionPromptForDirectClaudeCode(t *testing.T) {
	dir := t.TempDir()
	prompt := filepath.Join(dir, "prompt")
	t.Setenv("CLAUDE_TEST_PROMPT", prompt)
	binary := filepath.Join(dir, "claude")
	script := "#!/bin/sh\ncat > \"$CLAUDE_TEST_PROMPT\"\nprintf '%s\\n' '" + `{"type":"assistant","message":{"content":[{"type":"text","text":"The evidence is available."}]}}` + "'\n"
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	provider := claudecode.NewProvider(claudecode.StaticTokenSource{T: claudecode.Token{Access: "fixture-token"}}, claudecode.WithBinaryPath(binary))
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	live := engine.NewLiveRunner(provider, ws, "sonnet")
	t.Cleanup(func() {
		if err := live.Close(context.WithoutCancel(t.Context())); err != nil {
			t.Error(err)
		}
	})
	if err := live.RunTurn(t.Context(), "Describe the available information."); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(prompt)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "Completed child results arrive automatically") {
		t.Fatal("Claude Code prompt omitted automatic delivery")
	}
}
