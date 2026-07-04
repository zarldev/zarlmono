package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// Tool names
const (
	ToolGrep      tools.ToolName = "grep"
	ToolListFiles tools.ToolName = "list_files"
	ToolReadFile  tools.ToolName = "read_file"
	ToolSpawn     tools.ToolName = "spawn_agent"
)

type grepArgs struct {
	Pattern string `json:"pattern" doc:"Pattern to search for."`
}

type readFileArgs struct {
	Path string `json:"path" doc:"File path."`
}

type grepResult struct {
	Pattern string   `json:"pattern"`
	Matches []string `json:"matches"`
	Count   int      `json:"count"`
}

type listFilesResult struct {
	Files []string `json:"files"`
	Count int      `json:"count"`
}

type readFileResult struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Lines   int    `json:"lines"`
}

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
		Parameters:  tools.SchemaFor[grepArgs](),
		Mutates:     false,
	}
}

func (t *grepTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	args, err := tools.DecodeArgs[grepArgs](call.Arguments)
	if err != nil {
		return tools.Failure(call.ID, err), nil
	}
	if args.Pattern == "" {
		return tools.Failure(call.ID, tools.Validation(string(ToolGrep), "pattern cannot be empty")), nil
	}

	// Record this attempt
	t.attempts.Record(args.Pattern)

	// Perform the search
	matches := t.fs.Grep(args.Pattern)

	if len(matches) == 0 {
		// This is the "not found" error that DecomposeGuardrail tracks
		return tools.Failure(call.ID, tools.NotFound(string(ToolGrep),
			fmt.Sprintf("pattern %q not found in any file", args.Pattern))), nil
	}

	return tools.Success(call.ID, grepResult{Pattern: args.Pattern, Matches: matches, Count: len(matches)}), nil
}

// listFilesTool lists all files in the project.
type listFilesTool struct {
	fs *FileSystem
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

// readFileTool reads a file's content.
type readFileTool struct {
	fs *FileSystem
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

	return tools.Success(call.ID, readFileResult{Path: args.Path, Content: content, Lines: strings.Count(content, "\n")}), nil
}
