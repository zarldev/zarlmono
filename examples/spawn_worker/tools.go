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

func newReadFileTool(fs *FileSystem) tools.Tool {
	return tools.New(tools.ToolSpec{
		Name:        ToolReadFile,
		Description: "Read the content of a file",
		Parameters:  tools.SchemaFor[filePathArgs](),
		Mutates:     false,
	}, func(_ context.Context, args filePathArgs) (readFileResult, error) {
		if args.Path == "" {
			return readFileResult{}, tools.Validation(string(ToolReadFile), "path is required")
		}
		content, found := fs.Read(args.Path)
		if !found {
			return readFileResult{}, tools.NotFound(string(ToolReadFile), fmt.Sprintf("file not found: %s", args.Path))
		}
		return readFileResult{Path: args.Path, Content: content}, nil
	})
}

func newWriteFileTool(fs *FileSystem) tools.Tool {
	return tools.New(tools.ToolSpec{
		Name:        ToolWriteFile,
		Description: "Create or overwrite a file with content",
		Parameters:  tools.SchemaFor[writeFileArgs](),
		Mutates:     true,
	}, func(_ context.Context, args writeFileArgs) (writeFileResult, error) {
		if args.Path == "" {
			return writeFileResult{}, tools.Validation(string(ToolWriteFile), "path is required")
		}
		fs.Write(args.Path, args.Content)
		return writeFileResult{Path: args.Path, Lines: countLines(args.Content)}, nil
	})
}

func newEditFileTool(fs *FileSystem) tools.Tool {
	return tools.New(tools.ToolSpec{
		Name:        ToolEditFile,
		Description: "Replace old string with new string in a file",
		Parameters:  tools.SchemaFor[editFileArgs](),
		Mutates:     true,
	}, func(_ context.Context, args editFileArgs) (editFileResult, error) {
		content, ok := fs.Read(args.Path)
		if !ok {
			return editFileResult{}, tools.NotFound(string(ToolEditFile), fmt.Sprintf("file not found: %s", args.Path))
		}
		if !strings.Contains(content, args.Old) {
			return editFileResult{}, tools.Validation(string(ToolEditFile), fmt.Sprintf("old string not found in %s", args.Path))
		}
		fs.Write(args.Path, strings.Replace(content, args.Old, args.New, 1))
		return editFileResult{Path: args.Path, Replaced: true}, nil
	})
}

func newListFilesTool(fs *FileSystem) tools.Tool {
	return tools.New(tools.ToolSpec{
		Name:        ToolListFiles,
		Description: "List all files in the project",
		Parameters:  tools.SchemaFor[struct{}](),
		Mutates:     false,
	}, func(_ context.Context, _ struct{}) (listFilesResult, error) {
		files := fs.List()
		return listFilesResult{Files: files, Count: len(files)}, nil
	})
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
