package engine_test

import (
	"context"
	"fmt"
	"iter"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"

	agentcompact "github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

type operationalFakeSource struct{ live *engine.LiveRunner }

func (operationalFakeSource) Tools(context.Context) iter.Seq[tools.Tool] {
	return func(func(tools.Tool) bool) {}
}

func (s operationalFakeSource) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	var result *tools.ToolResult
	switch call.ToolName {
	case code.ToolNameEdit:
		result = tools.Success(call.ID, "edited", tools.NewFileEffect(tools.FileModify, call.Arguments.String("path", "")))
	case code.ToolNameRead:
		result = tools.Success(call.ID, "read")
	default:
		result = tools.Success(call.ID, "ok")
	}
	s.live.RecordOperationalResult(call, result, nil)
	return result, nil
}

func newOperationalRunner(t *testing.T) *engine.LiveRunner {
	t.Helper()
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	return engine.NewLiveRunner(nil, ws, "local")
}

func TestOperationalStateTracksLatestFilesAndToolCounts(t *testing.T) {
	t.Parallel()
	live := newOperationalRunner(t)
	src := operationalFakeSource{live: live}

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

	files := live.WorkingFiles()
	wantFiles := []agentcompact.FileTouch{{Path: "pkg/b.go", Action: "edit"}, {Path: "pkg/a.go", Action: "edit"}}
	if fmt.Sprint(files) != fmt.Sprint(wantFiles) {
		t.Fatalf("files = %+v, want %+v", files, wantFiles)
	}
	usage := live.TopTools()
	wantUsage := []agentcompact.ToolUsage{{Name: "edit", Count: 2}, {Name: "bash", Count: 1}, {Name: "read", Count: 1}}
	if fmt.Sprint(usage) != fmt.Sprint(wantUsage) {
		t.Fatalf("usage = %+v, want %+v", usage, wantUsage)
	}
}

func TestOperationalStateTracksVerificationAndFailures(t *testing.T) {
	t.Parallel()
	live := newOperationalRunner(t)
	live.RecordOperationalResult(
		tools.ToolCall{ToolName: code.ToolNameBash, Arguments: tools.ToolParameters{"command": "go test ./pkg"}},
		tools.Success("", "failed", tools.NewProcessEffect("go test ./pkg", 1)),
		nil,
	)
	verification := live.Verification()
	if verification == nil || verification.Passed || verification.Command != "go test ./pkg" {
		t.Fatalf("failed verification = %+v", verification)
	}

	failureResult := tools.Failure("", tools.Stale("edit", "anchors changed"))
	live.RecordOperationalResult(tools.ToolCall{ToolName: code.ToolNameEdit}, failureResult, nil)
	failures := live.UnresolvedFailures()
	if len(failures) != 1 || failures[0].Tool != "edit" || failures[0].Kind != "stale" {
		t.Fatalf("failures = %+v", failures)
	}

	live.RecordOperationalResult(
		tools.ToolCall{ToolName: code.ToolNameBash, Arguments: tools.ToolParameters{"command": "go test ./pkg"}},
		tools.Success("", "passed", tools.NewProcessEffect("go test ./pkg", 0)),
		nil,
	)
	verification = live.Verification()
	if verification == nil || !verification.Passed {
		t.Fatalf("passing verification = %+v", verification)
	}
	live.RecordOperationalResult(tools.ToolCall{ToolName: code.ToolNameEdit}, tools.Success("", "edited", tools.NewFileEffect(tools.FileModify, "pkg/file.go")), nil)
	if verification := live.Verification(); verification == nil || !verification.Stale {
		t.Fatalf("verification after later edit = %+v, want stale", verification)
	}
}

func TestOperationalStateBoundsWorkingFiles(t *testing.T) {
	t.Parallel()
	live := newOperationalRunner(t)
	for i := range 37 {
		live.RecordOperationalResult(tools.ToolCall{ToolName: code.ToolNameRead, Arguments: tools.ToolParameters{"path": fmt.Sprintf("file-%02d.go", i)}}, tools.Success("", "read"), nil)
	}
	files := live.WorkingFiles()
	if len(files) != 32 {
		t.Fatalf("files = %d, want %d", len(files), 32)
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
	live := engine.NewLiveRunner(nil, ws, "local")
	live.RecordOperationalResult(
		tools.ToolCall{ToolName: code.ToolNameEdit},
		tools.Success("", "edited", tools.NewFileEffect(tools.FileModify, "pkg/file.go")),
		nil,
	)
	live.RecordOperationalResult(tools.ToolCall{ToolName: code.ToolNameBash}, tools.Success("", "ok"), nil)
	live.RecordOperationalResult(
		tools.ToolCall{ToolName: code.ToolNameBash, Arguments: tools.ToolParameters{"command": "go test ./pkg"}},
		tools.Success("", "failed", tools.NewProcessEffect("go test ./pkg", 1)),
		nil,
	)
	live.RecordOperationalResult(tools.ToolCall{ToolName: code.ToolNameEdit}, tools.Failure("", tools.Stale("edit", "anchors changed")), nil)

	compactor := agentcompact.NewExecutive(operationalNarrativeProvider{}, "model", live)
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

func (operationalNarrativeProvider) Complete(context.Context, llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		yield(llm.CompletionChunk{Content: "narrative"}, nil)
	}
}
