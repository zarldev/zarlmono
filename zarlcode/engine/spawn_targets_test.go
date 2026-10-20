package engine_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestSpawnTargetsPreserveNestedDepthCeiling(t *testing.T) {
	for _, namedFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("named-first=%t", namedFirst), func(t *testing.T) {
			settings := newJudgeTestSettings(t)
			root := settings.WorkspaceRoot()
			agentDir := filepath.Join(root, ".zarlcode", "agents")
			if err := os.MkdirAll(agentDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(agentDir, "nested-fixture.md"), []byte("---\nname: nested-fixture\ndescription: Nested depth fixture\nmode: implement\n---\nReport the supplied fixture evidence.\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			for key, value := range map[string]string{
				prefs.KeySpawnDefaultExploreProvider:   prefs.ActiveModelTarget,
				prefs.KeySpawnDefaultVerifyProvider:    prefs.ActiveModelTarget,
				prefs.KeySpawnDefaultImplementProvider: prefs.ActiveModelTarget,
				prefs.KeySpawnDefaultExploreModel:      "fixture-model",
				prefs.KeySpawnDefaultVerifyModel:       "fixture-model",
				prefs.KeySpawnDefaultImplementModel:    "fixture-model",
				prefs.KeyCompactProvider:               prefs.ActiveModelTarget,
				prefs.KeyDecomposeJudge:                "off",
				prefs.KeySpawnFallback:                 "parent",
			} {
				if err := settings.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, key, value); err != nil {
					t.Fatal(err)
				}
			}
			var mu sync.Mutex
			seen := make(map[string]bool)
			var refused atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Messages []struct {
						Role    string          `json:"role"`
						Content json.RawMessage `json:"content"`
					} `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				prompt := "root"
				hasTool := false
				for _, msg := range body.Messages {
					text := string(msg.Content)
					if msg.Role == "user" {
						for _, label := range []string{"child-one", "child-two", "beyond-limit"} {
							if strings.Contains(text, label) {
								prompt = label
							}
						}
					}
					if msg.Role == "tool" {
						hasTool = true
					}
					if strings.Contains(text, "max recursion depth 2 reached") {
						refused.Store(true)
					}
				}
				mu.Lock()
				seen[prompt] = true
				mu.Unlock()
				if hasTool {
					completionFixture(w, "The fixture evidence is complete.", "")
					return
				}
				next := map[string]string{"root": "child-one", "child-one": "child-two", "child-two": "beyond-limit"}[prompt]
				if next == "" {
					t.Error("provider ran beyond the depth ceiling")
					completionFixture(w, "Stopped.", "")
					return
				}
				agent := ""
				if (prompt == "root") == namedFirst {
					agent = "nested-fixture"
				}
				arguments, _ := json.Marshal(map[string]string{"prompt": next, "mode": "implement", "agent": agent})
				completionFixture(w, "", string(arguments))
			}))
			t.Cleanup(server.Close)
			ws, err := code.NewWorkspace(root)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = ws.Close() })
			// Keep the parent spec unnamed so model-only child routes reuse this
			// local adapter; no credential store or external provider is involved.
			live := engine.NewLiveRunner(openai.NewProvider("fixture-key", openai.WithBaseURL(server.URL)), ws, "fixture-model", engine.WithSettings(settings))
			live.SetLimits(1024, 8, 8, 2)
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
				defer cancel()
				if err := live.Close(ctx); err != nil {
					t.Error(err)
				}
			})
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			if err := live.RunTurn(ctx, "Summarize the fixture evidence; do not change files."); err != nil {
				t.Fatal(err)
			}
			if !refused.Load() {
				t.Error("nested spawn never reported the depth ceiling")
			}
			mu.Lock()
			defer mu.Unlock()
			if !seen["root"] || !seen["child-one"] || !seen["child-two"] || seen["beyond-limit"] {
				t.Errorf("executed task prompts = %v", seen)
			}
		})
	}
}

// Both children must reach the provider before either completes, and each must
// retain its own history despite using the same configured mode target.
func TestSpawnTargetsShareModeRunnerAcrossOverlappingTasks(t *testing.T) {
	settings := newJudgeTestSettings(t)
	for key, value := range map[string]string{
		prefs.KeySpawnDefaultImplementProvider: prefs.ActiveModelTarget,
		prefs.KeySpawnDefaultImplementModel:    "fixture-model",
		prefs.KeyCompactProvider:               prefs.ActiveModelTarget,
		prefs.KeyDecomposeJudge:                "off",
	} {
		if err := settings.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, key, value); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	bothStarted := make(chan struct{})
	var children, rootCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		first, second := false, false
		for _, message := range body.Messages {
			if message.Role == "user" {
				first = first || strings.Contains(string(message.Content), "first-sibling")
				second = second || strings.Contains(string(message.Content), "second-sibling")
			}
		}
		if first || second {
			if first && second {
				t.Error("sibling prompts leaked into a shared history")
			}
			if children.Add(1) == 2 {
				close(bothStarted)
			}
			select {
			case <-bothStarted:
			case <-ctx.Done():
				return
			case <-r.Context().Done():
				return
			}
			completionFixture(w, "Independent evidence is complete.", "")
			return
		}
		switch rootCalls.Add(1) {
		case 1:
			completionFixture(w, "", `{"prompt":"first-sibling","mode":"implement"}`)
		case 2:
			completionFixture(w, "", `{"prompt":"second-sibling","mode":"implement"}`)
		default:
			completionFixture(w, "The evidence is summarized.", "")
		}
	}))
	t.Cleanup(server.Close)
	ws, err := code.NewWorkspace(settings.WorkspaceRoot())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	live := engine.NewLiveRunner(openai.NewProvider("fixture-key", openai.WithBaseURL(server.URL)), ws, "fixture-model", engine.WithSettings(settings))
	live.SetLimits(1024, 8, 8, 2)
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer closeCancel()
		if err := live.Close(closeCtx); err != nil {
			t.Error(err)
		}
	})
	if err := live.RunTurn(ctx, "Summarize the fixture evidence; do not change files."); err != nil {
		t.Fatal(err)
	}
	if got := children.Load(); got != 2 {
		t.Errorf("independent overlapping child requests = %d, want 2", got)
	}
}
