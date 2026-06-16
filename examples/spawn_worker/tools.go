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
	ToolReadFile  tools.ToolName = "read_file"
	ToolWriteFile tools.ToolName = "write_file"
	ToolEditFile  tools.ToolName = "edit_file"
	ToolListFiles tools.ToolName = "list_files"
)

// readFileTool reads a file from the filesystem.
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
				"path": map[string]any{"type": "string", "description": "File path"},
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
	if path == "" {
		return tools.Failure(call.ID, tools.Validation(string(ToolReadFile), "path is required")), nil
	}
	content, found := t.fs.Read(path)
	if !found {
		return tools.Failure(call.ID, tools.NotFound(string(ToolReadFile), fmt.Sprintf("file not found: %s", path))), nil
	}
	return tools.Success(call.ID, map[string]any{"path": path, "content": content}), nil
}

// writeFileTool creates or overwrites a file.
type writeFileTool struct {
	fs *FileSystem
}

func (t *writeFileTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        ToolWriteFile,
		Description: "Create or overwrite a file with content",
		Parameters: llm.SchemaFromMap(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string", "description": "File path"},
				"content": map[string]any{"type": "string", "description": "File content"},
			},
			"required":             []string{"path", "content"},
			"additionalProperties": false,
		}),
		Mutates: true,
	}
}

func (t *writeFileTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	pathVal, _ := call.Arguments["path"]
	path, _ := pathVal.(string)
	contentVal, _ := call.Arguments["content"]
	content, _ := contentVal.(string)
	if path == "" {
		return tools.Failure(call.ID, tools.Validation(string(ToolWriteFile), "path is required")), nil
	}
	t.fs.Write(path, content)
	return tools.Success(call.ID, map[string]any{"path": path, "lines": countLines(content)}), nil
}

// editFileTool applies a string replacement.
type editFileTool struct {
	fs *FileSystem
}

func (t *editFileTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        ToolEditFile,
		Description: "Replace old string with new string in a file",
		Parameters: llm.SchemaFromMap(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "description": "File path"},
				"old":  map[string]any{"type": "string", "description": "String to find"},
				"new":  map[string]any{"type": "string", "description": "Replacement string"},
			},
			"required":             []string{"path", "old", "new"},
			"additionalProperties": false,
		}),
		Mutates: true,
	}
}

func (t *editFileTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	pathVal, _ := call.Arguments["path"]
	path, _ := pathVal.(string)
	oldVal, _ := call.Arguments["old"]
	old, _ := oldVal.(string)
	replacementVal, _ := call.Arguments["new"]
	replacement, _ := replacementVal.(string)

	content, ok := t.fs.Read(path)
	if !ok {
		return tools.Failure(call.ID, tools.NotFound(string(ToolEditFile), fmt.Sprintf("file not found: %s", path))), nil
	}
	if !strings.Contains(content, old) {
		return tools.Failure(call.ID, tools.Validation(string(ToolEditFile), fmt.Sprintf("old string not found in %s", path))), nil
	}
	t.fs.Write(path, strings.Replace(content, old, replacement, 1))
	return tools.Success(call.ID, map[string]any{"path": path, "replaced": true}), nil
}

// listFilesTool lists all files.
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
	return tools.Success(call.ID, map[string]any{"files": files, "count": len(files)}), nil
}

// Helper function
func countLines(s string) int {
	count := 0
	for _, c := range s {
		if c == '\n' {
			count++
		}
	}
	return count
}
