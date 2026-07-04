package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// Tool names
const (
	ToolReadFile  tools.ToolName = "read_file"
	ToolWriteFile tools.ToolName = "write_file"
	ToolEditFile  tools.ToolName = "edit_file"
	ToolListFiles tools.ToolName = "list_files"
)

type filePathArgs struct {
	Path string `json:"path" doc:"File path."`
}

type writeFileArgs struct {
	Path    string `json:"path" doc:"File path."`
	Content string `json:"content" doc:"File content."`
}

type editFileArgs struct {
	Path string `json:"path" doc:"File path."`
	Old  string `json:"old" doc:"String to find."`
	New  string `json:"new" doc:"Replacement string."`
}

type readFileResult struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type writeFileResult struct {
	Path  string `json:"path"`
	Lines int    `json:"lines"`
}

type editFileResult struct {
	Path     string `json:"path"`
	Replaced bool   `json:"replaced"`
}

type listFilesResult struct {
	Files []string `json:"files"`
	Count int      `json:"count"`
}

// readFileTool reads a file from the filesystem.
type readFileTool struct {
	fs *FileSystem
}

func (t *readFileTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        ToolReadFile,
		Description: "Read the content of a file",
		Parameters:  tools.SchemaFor[filePathArgs](),
		Mutates:     false,
	}
}

func (t *readFileTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	args, err := tools.DecodeArgs[filePathArgs](call.Arguments)
	if err != nil {
		return tools.Failure(call.ID, err), nil
	}
	if args.Path == "" {
		return tools.Failure(call.ID, tools.Validation(string(ToolReadFile), "path is required")), nil
	}
	content, found := t.fs.Read(args.Path)
	if !found {
		return tools.Failure(call.ID, tools.NotFound(string(ToolReadFile), fmt.Sprintf("file not found: %s", args.Path))), nil
	}
	return tools.Success(call.ID, readFileResult{Path: args.Path, Content: content}), nil
}

// writeFileTool creates or overwrites a file.
type writeFileTool struct {
	fs *FileSystem
}

func (t *writeFileTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        ToolWriteFile,
		Description: "Create or overwrite a file with content",
		Parameters:  tools.SchemaFor[writeFileArgs](),
		Mutates:     true,
	}
}

func (t *writeFileTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	args, err := tools.DecodeArgs[writeFileArgs](call.Arguments)
	if err != nil {
		return tools.Failure(call.ID, err), nil
	}
	if args.Path == "" {
		return tools.Failure(call.ID, tools.Validation(string(ToolWriteFile), "path is required")), nil
	}
	t.fs.Write(args.Path, args.Content)
	return tools.Success(call.ID, writeFileResult{Path: args.Path, Lines: countLines(args.Content)}), nil
}

// editFileTool applies a string replacement.
type editFileTool struct {
	fs *FileSystem
}

func (t *editFileTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        ToolEditFile,
		Description: "Replace old string with new string in a file",
		Parameters:  tools.SchemaFor[editFileArgs](),
		Mutates:     true,
	}
}

func (t *editFileTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	args, err := tools.DecodeArgs[editFileArgs](call.Arguments)
	if err != nil {
		return tools.Failure(call.ID, err), nil
	}
	content, ok := t.fs.Read(args.Path)
	if !ok {
		return tools.Failure(call.ID, tools.NotFound(string(ToolEditFile), fmt.Sprintf("file not found: %s", args.Path))), nil
	}
	if !strings.Contains(content, args.Old) {
		return tools.Failure(call.ID, tools.Validation(string(ToolEditFile), fmt.Sprintf("old string not found in %s", args.Path))), nil
	}
	t.fs.Write(args.Path, strings.Replace(content, args.Old, args.New, 1))
	return tools.Success(call.ID, editFileResult{Path: args.Path, Replaced: true}), nil
}

// listFilesTool lists all files.
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
