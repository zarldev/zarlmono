package harness_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/swebench-eval/harness"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func projectionFixture(t *testing.T) (string, harness.EvidenceReceipt) {
	t.Helper()
	dir := t.TempDir()
	trial := harness.BaselineTrial()
	e, err := harness.OpenTrialEvidence(dir, "run", trial, harness.Task{ID: "task", Problem: "allowed task", FailToPass: []string{"CANARY_GRADER"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	if err = e.Request(t.Context(), llm.CompletionRequest{
		Messages: []llm.Message{{Role: llm.RoleSystem, Content: "CANARY_SYSTEM"}},
		Tools:    []llm.Tool{{Type: "function", Function: llm.ToolFunction{Name: "echo", Description: "CANARY_DESCRIPTION"}}},
		Options:  llm.ModelOptions{"secret": "CANARY_OPTIONS"},
	}); err != nil {
		t.Fatal(err)
	}
	tool := runner.ToolOutput{ExecutionID: "CANARY_EXECUTION", ToolName: "echo", Args: `{"secret":"CANARY_ARGS"}`, Output: "CANARY_OUTPUT", Error: "CANARY_ERROR", Success: false, Dispatched: new(false)}
	messages := []runner.ReplayMessage{{Message: llm.Message{Role: llm.RoleAssistant, Content: "CANARY_ASSISTANT", ReasoningContent: "CANARY_REASONING", ContinuationItems: []llm.ContinuationItem{{Provider: "fixture", Format: "opaque", Data: []byte("CANARY_NATIVE")}}}}, {Tool: &tool}, {Tool: &tool}}
	if err = e.Append(t.Context(), messages); err != nil {
		t.Fatal(err)
	}
	if err = e.Finish(harness.Result{TerminalReason: "completed", Diff: "CANARY_PATCH", HarnessLog: "CANARY_LOG", Verified: true, AttemptVerdicts: []harness.AttemptVerdict{{Error: "CANARY_VERDICT"}}, TokensIn: 12, TokensOut: 3}); err != nil {
		t.Fatal(err)
	}
	return dir, receiptFromFixture(t, dir)
}

func receiptFromFixture(t *testing.T, dir string) harness.EvidenceReceipt {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "trial-*.jsonl"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("artifacts %v: %v", paths, err)
	}
	data, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	return harness.EvidenceReceipt{File: filepath.Base(paths[0]), SHA256: fmt.Sprintf("%x", sha256.Sum256(data)), RunID: "run", TaskID: "task", Driver: harness.BaselineTrial().DriverName()}
}

func TestEvidenceProjectionExcludesPrivateAndGradingChannels(t *testing.T) {
	dir, receipt := projectionFixture(t)
	view, err := harness.ReadTrialEvidence(t.Context(), dir, receipt)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "CANARY") {
		t.Fatalf("private data reached projection: %s", data)
	}
	if view.TaskPrompt != "allowed task" || view.ID != receipt.SHA256 || view.PromptRevision != harness.EvaluationPromptRevision || len(view.Observations) != 1 {
		t.Fatalf("projection identity/dedup: %+v", view)
	}
	observation := view.Observations[0]
	if observation.Tool != "echo" || observation.Success || observation.Dispatched == nil || *observation.Dispatched || observation.ID != view.ID+"/tool/1" {
		t.Fatalf("tool metadata: %+v", observation)
	}
	if view.TokensIn != 12 || view.TokensOut != 3 {
		t.Fatal("usage lost")
	}
}

func TestEvidenceReceiptBoundaries(t *testing.T) {
	for _, name := range []string{"digest", "identity", "traversal", "symlink", "truncated", "after-result", "unsupported-version", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			dir, receipt := projectionFixture(t)
			path := filepath.Join(dir, receipt.File)
			ctx := t.Context()
			want := harness.ErrEvidence
			switch name {
			case "digest":
				receipt.SHA256 = strings.Repeat("0", 64)
			case "identity":
				receipt.TaskID = "another-task"
			case "traversal":
				receipt.File = "../" + receipt.File
			case "symlink":
				outside := t.TempDir()
				if err := os.Rename(path, filepath.Join(outside, "evidence")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(outside, "evidence"), path); err != nil {
					t.Fatal(err)
				}
			case "truncated", "after-result", "unsupported-version":
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				lines := strings.Split(strings.TrimSpace(string(data)), "\n")
				if name == "truncated" {
					data = []byte(strings.Join(lines[:len(lines)-1], "\n"))
				}
				if name == "after-result" {
					data = append(data, []byte(`{"kind":"history","data":[]}`)...)
				}
				if name == "unsupported-version" {
					data = []byte(strings.Replace(string(data), `"version":1`, `"version":99`, 1))
				}
				if err = os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
				receipt.SHA256 = fmt.Sprintf("%x", sha256.Sum256(data))
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				want = context.Canceled
			}
			if _, err := harness.ReadTrialEvidence(ctx, dir, receipt); !errors.Is(err, want) {
				t.Fatalf("error=%v want %v", err, want)
			}
		})
	}
}
