package main

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/pursue"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// TestRunLongConversation_FiresCompactionEvent is the integration check
// behind the README's "context pressure triggers compaction" claim:
// driving the scripted multi-file research run through the production
// harness must emit at least one CompactionApplied event. A sink observes
// the event; a small keep-recent makes the verbose tool outputs reliably
// cross the compaction floor.
func TestRunLongConversation_FiresCompactionEvent(t *testing.T) {
	fs := NewFileSystem()
	rc := NewResearchContext()
	sink := runnertest.NewSink()

	out := RunLongConversation(t.Context(), NewScriptedClient(), fs, rc, 3,
		runner.WithCompactKeepRecent(2),
		runner.WithSink(sink),
	)

	if sink.CompactionCount() == 0 {
		t.Fatal("no CompactionApplied event fired; the long-conversation compaction claim is unproven")
	}
	if out.Status() != pursue.Statuses.SUCCEEDED {
		t.Errorf("research goal not reached: status=%v reason=%v", out.Status(), out.Result.Reason)
	}
}

// TestResearchContext_TracksProgress records research milestones.
func TestResearchContext_TracksProgress(t *testing.T) {
	rc := NewResearchContext()

	rc.RecordFile("handlers.go", 50)
	rc.RecordFile("utils.go", 30)
	rc.RecordFunction("GetUserHandler")
	rc.RecordFunction("CreateUserHandler")

	summary := rc.Summary()
	if summary == "" {
		t.Error("expected non-empty summary")
	}

	funcs := rc.ListFunctions()
	if len(funcs) != 2 {
		t.Errorf("expected 2 functions, got %d", len(funcs))
	}
}

// TestResearchContext_CompactionCounts tracks compaction events.
func TestResearchContext_CompactionCounts(t *testing.T) {
	rc := NewResearchContext()
	if rc.compactions != 0 {
		t.Error("expected 0 compactions initially")
	}

	rc.RecordCompaction()
	rc.RecordCompaction()

	if rc.compactions != 2 {
		t.Errorf("expected 2 compactions, got %d", rc.compactions)
	}
}

// TestReadFileTool_ExtractsFunctions identifies functions in Go source.
func TestReadFileTool_ExtractsFunctions(t *testing.T) {
	fs := NewFileSystem()
	rc := NewResearchContext()
	tool := &readFileTool{fs: fs, rc: rc}

	result, err := tool.Execute(t.Context(), tools.ToolCall{
		ID:        "test-1",
		ToolName:  ToolReadFile,
		Arguments: tools.ToolParameters{"path": "handlers.go"},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	// Verify that the five handler functions were found
	funcs, _ := result.Data.(map[string]any)["functions"]
	fnList, ok := funcs.([]string)
	if !ok {
		t.Fatal("expected functions in result")
	}
	if len(fnList) < 3 { // At least GetUserHandler, CreateUserHandler, UpdateUserHandler
		t.Errorf("expected at least 3 functions, got %d: %v", len(fnList), fnList)
	}
}

// TestReadFileTool_TracksResearch updates research context.
func TestReadFileTool_TracksResearch(t *testing.T) {
	fs := NewFileSystem()
	rc := NewResearchContext()
	tool := &readFileTool{fs: fs, rc: rc}

	_, err := tool.Execute(t.Context(), tools.ToolCall{
		ID:        "test-1",
		ToolName:  ToolReadFile,
		Arguments: tools.ToolParameters{"path": "handlers.go"},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	functions := rc.ListFunctions()
	if len(functions) == 0 {
		t.Error("expected functions to be tracked after reading handlers.go")
	}
}

// TestListFilesTool_ReturnsAllFiles returns all files in the project.
func TestListFilesTool_ReturnsAllFiles(t *testing.T) {
	fs := NewFileSystem()
	rc := NewResearchContext()
	tool := &listFilesTool{fs: fs, rc: rc}

	result, err := tool.Execute(t.Context(), tools.ToolCall{
		ID:        "test-1",
		ToolName:  ToolListFiles,
		Arguments: tools.ToolParameters{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	files, _ := result.Data.(map[string]any)["files"]
	fileList, ok := files.([]string)
	if !ok {
		t.Fatal("expected files list in result")
	}
	if len(fileList) != 3 {
		t.Errorf("expected 3 files, got %d: %v", len(fileList), fileList)
	}
}

// TestPushDocsTool_PublishesDocumentation stores documents.
func TestPushDocsTool_PublishesDocumentation(t *testing.T) {
	rc := NewResearchContext()
	var docs []string
	tool := &pushDocsTool{rc: rc, docsWritten: &docs}

	_, err := tool.Execute(t.Context(), tools.ToolCall{
		ID:       "test-1",
		ToolName: ToolPushDocs,
		Arguments: tools.ToolParameters{
			"title":   "Test Doc",
			"content": "Some content",
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	if len(docs) != 1 {
		t.Errorf("expected 1 doc, got %d", len(docs))
	}
}

// TestFileSystem_Read returns content for existing files.
func TestFileSystem_Read(t *testing.T) {
	fs := NewFileSystem()

	content, found := fs.Read("handlers.go")
	if !found {
		t.Error("expected handlers.go to exist")
	}
	if len(content) < 100 {
		t.Errorf("expected handlers.go to have at least 100 chars, got %d", len(content))
	}

	_, found = fs.Read("nonexistent.go")
	if found {
		t.Error("expected nonexistent.go to not exist")
	}
}
