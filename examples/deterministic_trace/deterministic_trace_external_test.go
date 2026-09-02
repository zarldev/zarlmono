package main_test

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type traceEvent struct {
	Sequence int    `json:"sequence"`
	Source   string `json:"source"`
	Kind     string `json:"kind"`
	TaskID   string `json:"task_id"`
	Name     string `json:"name"`
}

func TestDeterministicTraceWritesAndReadsJSONL(t *testing.T) {
	artifact := filepath.Join(t.TempDir(), "trace.jsonl")
	cmd := exec.CommandContext(t.Context(), "go", "run", ".", "-out", artifact)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run deterministic_trace: %v\n%s", err, out)
	}

	events := readTrace(t, artifact)
	got := make([]string, 0, len(events))
	for i, event := range events {
		if event.Sequence != i+1 {
			t.Fatalf("event %d sequence = %d, want %d", i, event.Sequence, i+1)
		}
		got = append(got, event.Source+"."+event.Kind+":"+event.Name)
	}
	want := []string{
		"runner.conversation_started:",
		"runner.tool_started:lookup",
		"runner.tool_completed:lookup",
		"runner.iteration_completed:",
		"runner.content:",
		"runner.iteration_completed:",
		"runner.conversation_ended:",
		"workflow.workflow_started:",
		"workflow.workflow_node_started:double",
		"workflow.workflow_node_completed:double",
		"workflow.workflow_completed:",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("event sequence = %#v, want %#v", got, want)
	}
	for _, event := range events[:7] {
		if event.TaskID != "trace-example" {
			t.Fatalf("runner task ID = %q, want trace-example", event.TaskID)
		}
	}
	if text := string(out); !strings.Contains(text, "events=11") || !strings.Contains(text, "11 workflow.workflow_completed") {
		t.Fatalf("summary did not read artifact back:\n%s", text)
	}
}

func TestDeterministicTraceArtifactIsStable(t *testing.T) {
	first := runTrace(t, filepath.Join(t.TempDir(), "first.jsonl"))
	second := runTrace(t, filepath.Join(t.TempDir(), "second.jsonl"))
	if string(first) != string(second) {
		t.Fatalf("artifacts differ\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func runTrace(t *testing.T, artifact string) []byte {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "go", "run", ".", "-out", artifact)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run deterministic_trace: %v\n%s", err, out)
	}
	body, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	return body
}

func readTrace(t *testing.T, artifact string) []traceEvent {
	t.Helper()
	file, err := os.Open(artifact)
	if err != nil {
		t.Fatalf("open artifact: %v", err)
	}
	defer file.Close()

	var events []traceEvent
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var event traceEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("decode JSONL row: %v", err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan artifact: %v", err)
	}
	return events
}
