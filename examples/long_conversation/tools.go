package main

import (
	"context"
	"fmt"
	"regexp"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// Tool names
const (
	ToolReadFile  tools.ToolName = "read_file"
	ToolListFiles tools.ToolName = "list_files"
	ToolPushDocs  tools.ToolName = "push_docs"
)

// readFileTool reads a file and tracks it for research context.
type readFileTool struct {
	fs *FileSystem
	rc *ResearchContext
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
					"description": "File path to read",
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

	// Record file was read
	lines := countLines(content)
	t.rc.RecordFile(path, lines)

	// Extract function names
	funcs := extractFunctions(content)
	for _, f := range funcs {
		t.rc.RecordFunction(f)
	}

	return tools.Success(call.ID, map[string]any{
		"path":      path,
		"content":   content,
		"lines":     lines,
		"functions": funcs,
	}), nil
}

// listFilesTool lists all files.
type listFilesTool struct {
	fs *FileSystem
	rc *ResearchContext
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

// pushDocsTool publishes research findings as documentation.
type pushDocsTool struct {
	rc          *ResearchContext
	docsWritten *[]string
}

func (t *pushDocsTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        ToolPushDocs,
		Description: "Publish research findings as documentation",
		Parameters: llm.SchemaFromMap(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"title": map[string]any{
					"type":        "string",
					"description": "Document title",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "Document body in markdown",
				},
			},
			"required":             []string{"title", "content"},
			"additionalProperties": false,
		}),
		Mutates: true,
	}
}

func (t *pushDocsTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	titleVal, _ := call.Arguments["title"]
	title, _ := titleVal.(string)
	contentVal, _ := call.Arguments["content"]
	content, _ := contentVal.(string)

	doc := fmt.Sprintf("# %s\n\n%s", title, content)
	*t.docsWritten = append(*t.docsWritten, doc)

	return tools.Success(call.ID, map[string]any{
		"title":  title,
		"length": len(doc),
		"index":  len(*t.docsWritten),
	}), nil
}

// Helper functions
func countLines(s string) int {
	count := 0
	for _, c := range s {
		if c == '\n' {
			count++
		}
	}
	return count + 1
}

var funcPattern = regexp.MustCompile(`func (\w+)\(`)

func extractFunctions(content string) []string {
	matches := funcPattern.FindAllStringSubmatch(content, -1)
	var names []string
	seen := map[string]bool{}
	for _, m := range matches {
		name := m[1]
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	return names
}
