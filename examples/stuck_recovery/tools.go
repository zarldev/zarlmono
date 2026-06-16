package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// Tool names
const (
	ToolGrep      tools.ToolName = "grep"
	ToolListFiles tools.ToolName = "list_files"
	ToolReadFile  tools.ToolName = "read_file"
	ToolSpawn     tools.ToolName = "spawn_agent"
)

// grepTool searches for patterns in files.
// This is the tool that will trigger DecomposeGuardrail when it repeatedly fails.
type grepTool struct {
	fs       *FileSystem
	attempts *SearchAttempts
}

func (t *grepTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        ToolGrep,
		Description: "Search for a pattern in all files",
		Parameters: llm.SchemaFromMap(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "Pattern to search for",
				},
			},
			"required":             []string{"pattern"},
			"additionalProperties": false,
		}),
		Mutates: false,
	}
}

func (t *grepTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	patternVal, ok := call.Arguments["pattern"]
	if !ok {
		return tools.Failure(call.ID, tools.Validation(string(ToolGrep), "pattern is required")), nil
	}
	pattern, _ := patternVal.(string)
	if pattern == "" {
		return tools.Failure(call.ID, tools.Validation(string(ToolGrep), "pattern cannot be empty")), nil
	}

	// Record this attempt
	t.attempts.Record(pattern)

	// Perform the search
	matches := t.fs.Grep(pattern)

	if len(matches) == 0 {
		// This is the "not found" error that DecomposeGuardrail tracks
		return tools.Failure(call.ID, tools.NotFound(string(ToolGrep),
			fmt.Sprintf("pattern %q not found in any file", pattern))), nil
	}

	return tools.Success(call.ID, map[string]any{
		"pattern": pattern,
		"matches": matches,
		"count":   len(matches),
	}), nil
}

// listFilesTool lists all files in the project.
type listFilesTool struct {
	fs *FileSystem
}

func (t *listFilesTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        ToolListFiles,
		Description: "List all files in the project",
		Parameters: llm.SchemaFromMap(map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"additionalProperties": false,
		}),
		Mutates: false,
	}
}

func (t *listFilesTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	files := t.fs.List()
	return tools.Success(call.ID, map[string]any{
		"files": files,
		"count": len(files),
	}), nil
}

// readFileTool reads a file's content.
type readFileTool struct {
	fs *FileSystem
}

func (t *readFileTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        ToolReadFile,
		Description: "Read the content of a file",
		Parameters: llm.SchemaFromMap(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "File path",
				},
			},
			"required":             []string{"path"},
			"additionalProperties": false,
		}),
		Mutates: false,
	}
}

func (t *readFileTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	pathVal, ok := call.Arguments["path"]
	if !ok {
		return tools.Failure(call.ID, tools.Validation(string(ToolReadFile), "path is required")), nil
	}
	path, _ := pathVal.(string)

	content, found := t.fs.Read(path)
	if !found {
		return tools.Failure(call.ID, tools.NotFound(string(ToolReadFile),
			fmt.Sprintf("file not found: %s", path))), nil
	}

	return tools.Success(call.ID, map[string]any{
		"path":    path,
		"content": content,
		"lines":   strings.Count(content, "\n"),
	}), nil
}
