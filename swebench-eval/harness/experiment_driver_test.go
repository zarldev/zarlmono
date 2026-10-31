package harness_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/swebench-eval/harness"
	"github.com/zarldev/zarlmono/zkit/agent/coderunner"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestExperimentDriverLoopback(t *testing.T) {
	// The scripted tool only creates a text file in a temporary repository.
	// Do not require a host sandbox implementation for this offline fixture.
	t.Setenv("ZARLCODE_SANDBOX", "0")
	experiment, err := harness.ParseExperiment(strings.NewReader(experimentInput))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"default", "baseline", "candidate", "cancelled", "no-test-edit", "label-only", "custom-disabled"} {
		t.Run(name, func(t *testing.T) {
			var trial harness.Trial
			switch name {
			case "baseline":
				trial = experiment.Trials()[0]
			case "candidate", "cancelled":
				trial = experiment.Trials()[1]
			}
			var ablation harness.Ablation
			switch name {
			case "no-test-edit", "custom-disabled":
				arms, err := harness.AblationArms("no-test-edit")
				if err != nil {
					t.Fatal(err)
				}
				ablation = arms[0]
				if name == "custom-disabled" {
					ablation.Name = "custom"
				}
			case "label-only":
				ablation.Name = "no-test-edit"
			}
			root := trialRepository(t)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			var mu sync.Mutex
			var prompts []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Model    string `json:"model"`
					Messages []struct {
						Role    string          `json:"role"`
						Content json.RawMessage `json:"content"`
					} `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					http.Error(w, "bad request", http.StatusBadRequest)
					return
				}
				if request.Model != "fixture-model" {
					t.Errorf("model = %q", request.Model)
				}
				var prompt string
				for _, message := range request.Messages {
					if message.Role == "system" {
						if err := json.Unmarshal(message.Content, &prompt); err != nil {
							t.Error(err)
						}
					}
				}
				mu.Lock()
				prompts = append(prompts, prompt)
				first := len(prompts) == 1
				mu.Unlock()
				if name == "cancelled" {
					cancel()
					<-r.Context().Done()
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if first {
					fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"write-one","type":"function","function":{"name":"write","arguments":"{\"path\":\"answer.txt\",\"content\":\"fixture answer\\n\"}"}}]},"finish_reason":"tool_calls"}]}`+"\n\n")
				} else {
					fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"Created answer.txt."},"finish_reason":"stop"}]}`+"\n\n")
				}
				fmt.Fprint(w, "data: [DONE]\n\n")
			}))
			t.Cleanup(server.Close)
			state := trialProviderState(t, server.URL)
			evidenceDir := t.TempDir()
			driver := &harness.ZarlcodeDriver{Provider: "loopback-fixture", Model: "fixture-model", StateDB: state,
				Trial: trial, Ablation: ablation, EvidenceDir: evidenceDir, ExperimentRunID: "fixture-run", MaxIter: 3}
			t.Cleanup(driver.Close)
			result := driver.Run(ctx, harness.Task{ID: "fixture-task", RepoPath: root, BaseCommit: "HEAD", Problem: "Create answer.txt."})
			if result.Err != nil {
				t.Fatal(result.Err)
			}
			wantReason := "completed"
			if name == "cancelled" {
				wantReason = "cancelled"
			} else if !strings.Contains(result.Diff, "+fixture answer") {
				t.Fatalf("tool did not create a patch: %+v", result)
			}
			if result.TerminalReason != wantReason {
				t.Fatalf("terminal reason = %q, want %q", result.TerminalReason, wantReason)
			}
			mu.Lock()
			captured := append([]string{}, prompts...)
			mu.Unlock()
			if len(captured) == 0 {
				t.Fatal("driver did not reach loopback provider")
			}
			canonical := trialCanonicalPrompt(t, root)
			if name != "no-test-edit" && name != "custom-disabled" {
				canonical += evaluationPolicyAppendix
			}
			for _, prompt := range captured {
				if prompt != trial.Prompt(canonical) {
					t.Fatal("driver prompt differs from exact canonical + trial guidance")
				}
			}
			if trial.DriverName() == "" {
				entries, err := os.ReadDir(evidenceDir)
				if err != nil || len(entries) != 0 {
					t.Fatalf("ordinary evaluation unexpectedly captured trial evidence: %v %v", entries, err)
				}
				return
			}
			view, projectionErr := harness.ReadTrialEvidence(t.Context(), evidenceDir, result.Evidence)
			if name == "cancelled" {
				if !errors.Is(projectionErr, harness.ErrEvidence) {
					t.Fatalf("cancelled evidence admitted: %v", projectionErr)
				}
			} else if projectionErr != nil || view.ID != result.Evidence.SHA256 || len(view.Observations) != 1 {
				t.Fatalf("driver receipt/projection: %+v %v", view, projectionErr)
			}
			events, _ := readEvidence(t, evidenceDir)
			if events[0].Kind != "start" || events[len(events)-1].Kind != "result" {
				t.Fatal("driver did not settle evidence")
			}
			var terminal struct {
				Reason string `json:"terminal_reason"`
			}
			if err := json.Unmarshal(events[len(events)-1].Data, &terminal); err != nil || terminal.Reason != wantReason {
				t.Fatalf("recorded terminal = %q: %v", terminal.Reason, err)
			}
		})
	}
}

func trialRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-qm", "base"}} {
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fixture repository: %v\n%s", err, out)
		}
	}
	return root
}

func trialProviderState(t *testing.T, url string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.db")
	store, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.UpsertProvider(t.Context(), backends.StoredProvider{
		Name: "loopback-fixture", AdapterType: "openAICompatible", BaseURL: url,
		DefaultModel: "fixture-model", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	return path
}

func trialCanonicalPrompt(t *testing.T, root string) string {
	t.Helper()
	workspace, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	defer workspace.Close()
	registry := tools.NewRegistry()
	coderunner.RegisterStandardTools(registry, workspace, nil)
	prompt, err := harness.RenderSystemPrompt(workspace.Root(), registry)
	if err != nil {
		t.Fatal(err)
	}
	return prompt
}

func TestExperimentDriverRejectsUnwritableEvidenceBeforeProvider(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(path, []byte("occupied"), 0o600); err != nil {
		t.Fatal(err)
	}
	experiment, err := harness.ParseExperiment(strings.NewReader(experimentInput))
	if err != nil {
		t.Fatal(err)
	}
	driver := &harness.ZarlcodeDriver{Provider: "must-not-construct", Trial: experiment.Trials()[0], EvidenceDir: path}
	t.Cleanup(driver.Close)
	if result := driver.Run(t.Context(), harness.Task{}); !errors.Is(result.Err, harness.ErrEvidence) {
		t.Fatalf("capture failure lost: %v", result.Err)
	}
}
