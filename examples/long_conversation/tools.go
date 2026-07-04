package main

import (
	"context"
	"fmt"
	"regexp"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// Tool names
const (
	ToolReadFile  tools.ToolName = "read_file"
	ToolListFiles tools.ToolName = "list_files"
	ToolPushDocs  tools.ToolName = "push_docs"
)

type readFileArgs struct {
	Path string `json:"path" doc:"File path to read."`
}

type pushDocsArgs struct {
	Title   string `json:"title" doc:"Document title."`
	Content string `json:"content" doc:"Document body in markdown."`
}

type readFileResult struct {
	Path      string   `json:"path"`
	Content   string   `json:"content"`
	Lines     int      `json:"lines"`
	Functions []string `json:"functions"`
}

type listFilesResult struct {
	Files []string `json:"files"`
	Count int      `json:"count"`
}

type pushDocsResult struct {
	Title  string `json:"title"`
	Length int    `json:"length"`
	Index  int    `json:"index"`
}

// readFileTool reads a file and tracks it for research context.
type readFileTool struct {
	fs *FileSystem
	rc *ResearchContext
}

func (t *readFileTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        ToolReadFile,
		Description: "Read the content of a file",
		Parameters:  tools.SchemaFor[readFileArgs](),
		Mutates:     false,
	}
}

func (t *readFileTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	args, err := tools.DecodeArgs[readFileArgs](call.Arguments)
	if err != nil {
		return tools.Failure(call.ID, err), nil
	}

	content, found := t.fs.Read(args.Path)
	if !found {
		return tools.Failure(call.ID, tools.NotFound(string(ToolReadFile),
			fmt.Sprintf("file not found: %s", args.Path))), nil
	}

	// Record file was read
	lines := countLines(content)
	t.rc.RecordFile(args.Path, lines)

	// Extract function names
	funcs := extractFunctions(content)
	for _, f := range funcs {
		t.rc.RecordFunction(f)
	}

	return tools.Success(call.ID, readFileResult{Path: args.Path, Content: content, Lines: lines, Functions: funcs}), nil
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
		Parameters:  tools.SchemaFor[struct{}](),
		Mutates:     false,
	}
}

func (t *listFilesTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	files := t.fs.List()
	return tools.Success(call.ID, listFilesResult{Files: files, Count: len(files)}), nil
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
		Parameters:  tools.SchemaFor[pushDocsArgs](),
		Mutates:     true,
	}
}

func (t *pushDocsTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	args, err := tools.DecodeArgs[pushDocsArgs](call.Arguments)
	if err != nil {
		return tools.Failure(call.ID, err), nil
	}
	doc := fmt.Sprintf("# %s\n\n%s", args.Title, args.Content)
	*t.docsWritten = append(*t.docsWritten, doc)

	return tools.Success(call.ID, pushDocsResult{Title: args.Title, Length: len(doc), Index: len(*t.docsWritten)}), nil
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
