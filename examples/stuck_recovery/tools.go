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
	ToolSpawn     tools.ToolName = "agent_spawn"
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

// newGrepTool records searches so repeated misses can trigger DecomposeGuardrail.
func newGrepTool(fs *FileSystem, attempts *SearchAttempts) tools.Tool {
	return tools.New(tools.ToolSpec{
		Name:        ToolGrep,
		Description: "Search for a pattern in all files",
		Parameters:  tools.SchemaFor[grepArgs](),
		Mutates:     false,
	}, func(_ context.Context, args grepArgs) (grepResult, error) {
		if args.Pattern == "" {
			return grepResult{}, tools.Validation(string(ToolGrep), "pattern cannot be empty")
		}
		attempts.Record(args.Pattern)
		matches := fs.Grep(args.Pattern)
		if len(matches) == 0 {
			return grepResult{}, tools.NotFound(string(ToolGrep),
				fmt.Sprintf("pattern %q not found in any file", args.Pattern))
		}
		return grepResult{Pattern: args.Pattern, Matches: matches, Count: len(matches)}, nil
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

func newReadFileTool(fs *FileSystem) tools.Tool {
	return tools.New(tools.ToolSpec{
		Name:        ToolReadFile,
		Description: "Read the content of a file",
		Parameters:  tools.SchemaFor[readFileArgs](),
		Mutates:     false,
	}, func(_ context.Context, args readFileArgs) (readFileResult, error) {
		content, found := fs.Read(args.Path)
		if !found {
			return readFileResult{}, tools.NotFound(string(ToolReadFile),
				fmt.Sprintf("file not found: %s", args.Path))
		}
		return readFileResult{Path: args.Path, Content: content, Lines: strings.Count(content, "\n")}, nil
	})
}
