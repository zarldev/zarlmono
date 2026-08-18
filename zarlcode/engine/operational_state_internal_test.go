package engine

import (
	"context"
	"fmt"
	"iter"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

type operationalFakeSource struct{}

func (operationalFakeSource) Tools(context.Context) iter.Seq[tools.Tool] {
	return func(func(tools.Tool) bool) {}
}

func (operationalFakeSource) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	switch call.ToolName {
	case code.ToolNameEdit:
		return tools.Success(call.ID, "edited", tools.NewFileEffect(tools.FileModify, call.Arguments.String("path", ""))), nil
	case code.ToolNameRead:
		return tools.Success(call.ID, "read"), nil
	default:
		return tools.Success(call.ID, "ok"), nil
	}
}

func TestOperationalStateTracksLatestFilesAndToolCounts(t *testing.T) {
	t.Parallel()
	state := newOperationalState()
	src := newOperationalSource(operationalFakeSource{}, state)

	calls := []tools.ToolCall{
		{ToolName: code.ToolNameRead, Arguments: tools.ToolParameters{"path": "pkg/a.go"}},
		{ToolName: code.ToolNameEdit, Arguments: tools.ToolParameters{"path": "pkg/b.go"}},
		{ToolName: code.ToolNameEdit, Arguments: tools.ToolParameters{"path": "pkg/a.go"}},
		{ToolName: code.ToolNameBash},
	}
	for _, call := range calls {
		if _, err := src.Execute(t.Context(), call); err != nil {
			t.Fatalf("execute %s: %v", call.ToolName, err)
		}
	}

	files := state.workingFiles()
	wantFiles := []compact.FileTouch{{Path: "pkg/b.go", Action: fileActionEdit}, {Path: "pkg/a.go", Action: fileActionEdit}}
	if fmt.Sprint(files) != fmt.Sprint(wantFiles) {
		t.Fatalf("files = %+v, want %+v", files, wantFiles)
	}
	usage := state.topTools()
	wantUsage := []compact.ToolUsage{{Name: "edit", Count: 2}, {Name: "bash", Count: 1}, {Name: "read", Count: 1}}
	if fmt.Sprint(usage) != fmt.Sprint(wantUsage) {
		t.Fatalf("usage = %+v, want %+v", usage, wantUsage)
	}
}

func TestOperationalStateTracksVerificationAndFailures(t *testing.T) {
	t.Parallel()
	state := newOperationalState()
	state.record(
		tools.ToolCall{ToolName: code.ToolNameBash, Arguments: tools.ToolParameters{"command": "go test ./pkg"}},
		tools.Success("", "failed", tools.NewProcessEffect("go test ./pkg", 1)),
		nil,
	)
	verification := state.verificationState()
	if verification == nil || verification.Passed || verification.Command != "go test ./pkg" {
		t.Fatalf("failed verification = %+v", verification)
	}

	failureResult := tools.Failure("", tools.Stale("edit", "anchors changed"))
	state.record(tools.ToolCall{ToolName: code.ToolNameEdit}, failureResult, nil)
	failures := state.unresolvedFailures()
	if len(failures) != 1 || failures[0].Tool != "edit" || failures[0].Kind != "stale" {
		t.Fatalf("failures = %+v", failures)
	}

	state.record(
		tools.ToolCall{ToolName: code.ToolNameBash, Arguments: tools.ToolParameters{"command": "go test ./pkg"}},
		tools.Success("", "passed", tools.NewProcessEffect("go test ./pkg", 0)),
		nil,
	)
	verification = state.verificationState()
	if verification == nil || !verification.Passed {
		t.Fatalf("passing verification = %+v", verification)
	}
	state.record(tools.ToolCall{ToolName: code.ToolNameEdit}, tools.Success("", "edited", tools.NewFileEffect(tools.FileModify, "pkg/file.go")), nil)
	if verification := state.verificationState(); verification == nil || !verification.Stale {
		t.Fatalf("verification after later edit = %+v, want stale", verification)
	}
}

func TestOperationalStateBoundsWorkingFiles(t *testing.T) {
	t.Parallel()
	state := newOperationalState()
	for i := range maxOperationalFiles + 5 {
		state.recordFile(compact.FileTouch{Path: fmt.Sprintf("file-%02d.go", i), Action: fileActionRead})
	}
	files := state.workingFiles()
	if len(files) != maxOperationalFiles {
		t.Fatalf("files = %d, want %d", len(files), maxOperationalFiles)
	}
	if files[0].Path != "file-05.go" || files[len(files)-1].Path != "file-36.go" {
		t.Fatalf("bounded file order = first %q last %q", files[0].Path, files[len(files)-1].Path)
	}
}

func TestLiveRunnerOperationalStateFeedsExecutiveBriefing(t *testing.T) {
	t.Parallel()
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	live := NewLiveRunner(nil, ws, nil, "local")
	live.operational.record(
		tools.ToolCall{ToolName: code.ToolNameEdit},
		tools.Success("", "edited", tools.NewFileEffect(tools.FileModify, "pkg/file.go")),
		nil,
	)
	live.operational.record(tools.ToolCall{ToolName: code.ToolNameBash}, tools.Success("", "ok"), nil)
	live.operational.record(
		tools.ToolCall{ToolName: code.ToolNameBash, Arguments: tools.ToolParameters{"command": "go test ./pkg"}},
		tools.Success("", "failed", tools.NewProcessEffect("go test ./pkg", 1)),
		nil,
	)
	live.operational.record(tools.ToolCall{ToolName: code.ToolNameEdit}, tools.Failure("", tools.Stale("edit", "anchors changed")), nil)

	compactor := compact.NewExecutive(operationalNarrativeProvider{}, "model", live)
	history := []llm.Message{
		{Role: llm.RoleSystem, Content: "system"},
		{Role: llm.RoleUser, Content: "old request"},
		{Role: llm.RoleAssistant, Content: "old response"},
		{Role: llm.RoleUser, Content: "recent request"},
	}
	result, err := compactor.Compact(t.Context(), history, 1)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	briefing := result.History[1].Content
	for _, want := range []string{"WORKING FILES", "pkg/file.go", "TOOL USAGE", "bash × 2", "edit × 2", "VERIFICATION", "go test ./pkg", "failed", "UNRESOLVED FAILURES", "anchors changed"} {
		if !strings.Contains(briefing, want) {
			t.Fatalf("briefing missing %q:\n%s", want, briefing)
		}
	}
}

type operationalNarrativeProvider struct{ llm.Provider }

func (operationalNarrativeProvider) Complete(context.Context, llm.CompletionRequest) (iter.Seq2[llm.CompletionChunk, error], error) {
	return func(yield func(llm.CompletionChunk, error) bool) {
		yield(llm.CompletionChunk{Content: "narrative", Done: true}, nil)
	}, nil
}
