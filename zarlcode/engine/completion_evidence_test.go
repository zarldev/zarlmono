package engine_test

import (
	"context"
	"iter"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

type evidenceFakeSource struct {
	results map[tools.ToolName]*tools.ToolResult
}

func (s evidenceFakeSource) Tools(context.Context) iter.Seq[tools.Tool] {
	return func(func(tools.Tool) bool) {}
}

func (s evidenceFakeSource) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	result := s.results[call.ToolName]
	if result == nil {
		return tools.Success(call.ID, "ok"), nil
	}
	cloned := *result
	cloned.ToolCallID = call.ID
	return &cloned, nil
}

type evidenceMutationTool struct{}

func (evidenceMutationTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{Name: code.ToolNameEdit, Mutates: true}
}

func (evidenceMutationTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	return tools.Success(call.ID, "edited", tools.NewFileEffect(tools.FileModify, "pkg/file.go")), nil
}

func TestCompletionEvidenceTracksMutationAndVerificationOrder(t *testing.T) {
	t.Parallel()
	evidence := engine.NewCompletionEvidence()
	src := engine.WithCompletionEvidence(evidenceFakeSource{results: map[tools.ToolName]*tools.ToolResult{
		code.ToolNameEdit: tools.Success("", "edited", tools.NewFileEffect(tools.FileModify, "pkg/b.go"), tools.NewFileEffect(tools.FileModify, "pkg/a.go")),
		code.ToolNameBash: tools.Success("", "ok", tools.NewProcessEffect("go test ./pkg", 0)),
	}}, evidence)

	if _, err := src.Execute(t.Context(), tools.ToolCall{ToolName: code.ToolNameEdit}); err != nil {
		t.Fatalf("edit: %v", err)
	}
	before := evidence.Snapshot()
	if before.LastMutation == 0 || before.LastVerification != 0 {
		t.Fatalf("before verification = %+v", before)
	}
	if got, want := before.MutatedPaths, []string{"pkg/a.go", "pkg/b.go"}; !equalStrings(got, want) {
		t.Fatalf("mutated paths = %v, want %v", got, want)
	}

	if _, err := src.Execute(t.Context(), tools.ToolCall{
		ToolName:  code.ToolNameBash,
		Arguments: tools.ToolParameters{"command": "go test ./pkg"},
	}); err != nil {
		t.Fatalf("bash: %v", err)
	}
	after := evidence.Snapshot()
	if after.LastVerification <= after.LastMutation || !after.VerificationPassed {
		t.Fatalf("after verification = %+v", after)
	}
}

func TestCompletionEvidenceRecordsFailedVerification(t *testing.T) {
	t.Parallel()
	evidence := engine.NewCompletionEvidence()
	src := engine.WithCompletionEvidence(evidenceFakeSource{results: map[tools.ToolName]*tools.ToolResult{
		code.ToolNameEdit: tools.Success("", "edited", tools.NewFileEffect(tools.FileModify, "pkg/a.go")),
		code.ToolNameBash: tools.Success("", "failed", tools.NewProcessEffect("go test ./pkg", 1)),
	}}, evidence)

	_, _ = src.Execute(t.Context(), tools.ToolCall{ToolName: code.ToolNameEdit})
	_, _ = src.Execute(t.Context(), tools.ToolCall{
		ToolName:  code.ToolNameBash,
		Arguments: tools.ToolParameters{"command": "go test ./pkg"},
	})
	snapshot := evidence.Snapshot()
	if snapshot.VerificationPassed || snapshot.VerificationCommand != "go test ./pkg" {
		t.Fatalf("failed verification snapshot = %+v", snapshot)
	}
}

func TestCompletionEvidenceIgnoresBackgroundAndNonVerificationCommands(t *testing.T) {
	t.Parallel()
	evidence := engine.NewCompletionEvidence()
	background := tools.NewProcessEffect("go test ./...", 0)
	background.Process.Background = true
	src := engine.WithCompletionEvidence(evidenceFakeSource{results: map[tools.ToolName]*tools.ToolResult{
		code.ToolNameBash: tools.Success("", "started", background),
	}}, evidence)

	_, _ = src.Execute(t.Context(), tools.ToolCall{
		ToolName:  code.ToolNameBash,
		Arguments: tools.ToolParameters{"command": "go test ./..."},
	})
	if got := evidence.Snapshot().LastVerification; got != 0 {
		t.Fatalf("background verification sequence = %d, want 0", got)
	}

	src = engine.WithCompletionEvidence(evidenceFakeSource{results: map[tools.ToolName]*tools.ToolResult{
		code.ToolNameBash: tools.Success("", "ok", tools.NewProcessEffect("git diff", 0)),
	}}, evidence)
	_, _ = src.Execute(t.Context(), tools.ToolCall{
		ToolName:  code.ToolNameBash,
		Arguments: tools.ToolParameters{"command": "git diff"},
	})
	if got := evidence.Snapshot().LastVerification; got != 0 {
		t.Fatalf("non-verification sequence = %d, want 0", got)
	}
}

func TestVerificationCommandClassifier(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"go test ./pkg":             true,
		"go test -C zarlcode ./...": true,
		"go vet ./...":              true,
		"go build ./cmd":            true,
		"go tool task check":        true,
		"go tool task lint":         true,
		"golangci-lint run":         true,
		"git diff":                  false,
		"grep TODO file.go":         false,
		"go mod tidy":               false,
	}
	for command, want := range cases {
		if got := engine.IsVerificationCommand(command); got != want {
			t.Errorf("engine.IsVerificationCommand(%q) = %v, want %v", command, got, want)
		}
	}
}

func TestEvidenceAwareCompletionCorrectsOnceInsideRunner(t *testing.T) {
	t.Parallel()
	evidence := engine.NewCompletionEvidence()
	source := engine.WithCompletionEvidence(tools.NewRegistry(evidenceMutationTool{}), evidence)
	quality := engine.NewPlanAwareTurnQuality(func() bool { return false }, evidence)
	client := runnertest.NewClient([][]llm.CompletionChunk{
		{runnertest.ChunkToolCall("edit-1", "edit", `{}`)},
		{runnertest.ChunkText("done")},
		{runnertest.ChunkText("verified manually")},
	})
	r := runner.New(client,
		runner.WithTools(source),
		runner.WithTurnQuality(quality),
		runner.WithMaxIterations(5),
	)

	result := r.Run(t.Context(), runner.TaskSpec{ID: "evidence-run", Prompt: "change it"})
	if result.Reason != runner.TerminalCompleted {
		t.Fatalf("reason = %s, want completed; err=%v", result.Reason, result.Err)
	}
	if client.CallCount() != 3 {
		t.Fatalf("provider calls = %d, want 3", client.CallCount())
	}
	if result.FinalContent != "verified manually" {
		t.Fatalf("final content = %q", result.FinalContent)
	}
	foundCorrection := false
	for _, message := range result.Messages {
		if message.Role == llm.RoleUser && message.Content == engine.VerifyAfterChangeCorrection {
			foundCorrection = true
			break
		}
	}
	if !foundCorrection {
		t.Fatalf("result history missing evidence correction: %+v", result.Messages)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
