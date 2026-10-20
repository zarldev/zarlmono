package harness_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/zarldev/zarlmono/swebench-eval/harness"
	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

type evidenceEvent struct {
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data"`
}

func readEvidence(t *testing.T, dir string) ([]evidenceEvent, []byte) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "trial-*.jsonl"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("artifact paths = %v: %v", paths, err)
	}
	info, err := os.Stat(paths[0])
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("artifact permissions: %v %v", info, err)
	}
	data, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	var events []evidenceEvent
	for {
		var event evidenceEvent
		err := dec.Decode(&event)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	return events, data
}

type evidenceCompactor struct{ calls int }

func (c *evidenceCompactor) Compact(_ context.Context, messages []llm.Message, _ int) (compact.Result, error) {
	c.calls++
	for i := range messages {
		if messages[i].Role != llm.RoleSystem {
			messages[i].Content = "COMPACTED"
		}
	}
	return compact.Result{History: messages, BytesTrimmed: 1}, nil
}

func TestEvidencePreservesCanonicalHistoryAndExcludesGrading(t *testing.T) {
	dir := t.TempDir()
	experiment, err := harness.ParseExperiment(strings.NewReader(experimentInput))
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := harness.OpenTrialEvidence(dir, "run", experiment.Trials()[1], harness.Task{
		ID: "../../task", Problem: "original task", FailToPass: []string{"GOLD-TEST-CANARY"}, PassToPass: []string{"GRADER-CANARY"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = evidence.Close() })
	client := runnertest.NewClient([][]llm.CompletionChunk{
		{runnertest.ChunkToolCall("one", "echo", "{}")}, {runnertest.ChunkText("final answer")},
	})
	compactor := &evidenceCompactor{}
	r := runner.New(client, runner.WithHistorySink(evidence), runner.WithPromptText("canonical guidance"),
		runner.WithTools(tools.NewRegistry(runnertest.Tool{Name: "echo", Result: "full observation"})),
		runner.WithCompactor(compactor), runner.WithCompactKeepRecent(1), runner.WithMaxIterations(2))
	result := r.Run(t.Context(), runner.TaskSpec{Prompt: "original task"})
	if result.Err != nil || compactor.calls == 0 {
		t.Fatalf("runner: %v, compactions: %d", result.Err, compactor.calls)
	}
	// Invalid UTF-8 is preserved by the shared history format, not silently repaired.
	original := "byte-\xff"
	if err := evidence.Append(t.Context(), []runner.ReplayMessage{{Message: llm.Message{Role: llm.RoleAssistant, Content: original}}}); err != nil {
		t.Fatal(err)
	}
	if err := evidence.Request(t.Context(), llm.CompletionRequest{Options: llm.ModelOptions{"credential": "OPTION-SECRET-CANARY"}}); err != nil {
		t.Fatal(err)
	}
	if err := evidence.Finish(harness.Result{Diff: "patch", TerminalReason: "completed", Verified: true,
		AttemptVerdicts: []harness.AttemptVerdict{{Error: "VERDICT-CANARY"}}, HarnessLog: "LOG-SECRET-CANARY"}); err != nil {
		t.Fatal(err)
	}
	events, data := readEvidence(t, dir)
	for _, forbidden := range []string{"GOLD-TEST-CANARY", "GRADER-CANARY", "VERDICT-CANARY", "LOG-SECRET-CANARY", "OPTION-SECRET-CANARY"} {
		if bytes.Contains(data, []byte(forbidden)) {
			t.Fatalf("grading/private metadata leaked: %s", forbidden)
		}
	}
	var originalFound, observationFound, byteFound, requestFound bool
	for _, event := range events {
		switch event.Kind {
		case "history":
			var records []json.RawMessage
			if err := json.Unmarshal(event.Data, &records); err != nil {
				t.Fatal(err)
			}
			for _, raw := range records {
				record, err := runner.UnmarshalReplayMessage(raw)
				if err != nil {
					t.Fatal(err)
				}
				originalFound = originalFound || record.Message.Content == "original task"
				observationFound = observationFound || record.Message.Content == "full observation"
				byteFound = byteFound || record.Message.Content == original
				if record.Message.Content == "COMPACTED" {
					t.Fatal("compacted request contaminated canonical history")
				}
			}
		case "request":
			req, err := runner.UnmarshalHistoryRequest(event.Data)
			if err != nil {
				t.Fatal(err)
			}
			for _, msg := range req.Messages {
				requestFound = requestFound || strings.Contains(msg.Content, "canonical guidance")
			}
		}
	}
	if !originalFound || !observationFound || !byteFound || !requestFound || events[len(events)-1].Kind != "result" {
		t.Fatalf("missing evidence: original=%v observation=%v bytes=%v request=%v", originalFound, observationFound, byteFound, requestFound)
	}
}

func TestEvidenceConcurrentCaptureAndCancelledSettlement(t *testing.T) {
	dir := t.TempDir()
	e, err := harness.OpenTrialEvidence(dir, "run", harness.Trial{}, harness.Task{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if err := e.Append(ctx, []runner.ReplayMessage{{Message: llm.Message{Role: llm.RoleAssistant, Content: "settled"}}}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if err := e.Finish(harness.Result{Err: ctx.Err()}); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	events, _ := readEvidence(t, dir)
	if len(events) != 22 {
		t.Fatalf("events = %d", len(events))
	}
	client := runnertest.NewClient(nil)
	result := runner.New(client, runner.WithHistorySink(e)).Run(t.Context(), runner.TaskSpec{Prompt: "capture must fail"})
	if !errors.Is(result.Err, harness.ErrEvidence) || client.CallCount() != 0 {
		t.Fatalf("capture failure did not stop provider: %v, calls=%d", result.Err, client.CallCount())
	}
}

func TestEvidenceLimitFailsClosed(t *testing.T) {
	dir := t.TempDir()
	e, err := harness.OpenTrialEvidence(dir, "run", harness.Trial{}, harness.Task{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	records := []runner.ReplayMessage{{Message: llm.Message{Role: llm.RoleAssistant, Content: strings.Repeat("x", 1<<20)}}}
	for range 65 {
		err = e.Append(t.Context(), records)
		if err != nil {
			break
		}
	}
	if !errors.Is(err, harness.ErrEvidence) || !errors.Is(e.Finish(harness.Result{}), harness.ErrEvidence) {
		t.Fatalf("limit was not sticky: %v", err)
	}
	paths, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("artifacts: %v %v", paths, err)
	}
	info, err := os.Stat(paths[0])
	if err != nil || info.Size() > 64<<20 {
		t.Fatalf("unbounded artifact: %v %v", info, err)
	}
}
