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

// newReadFileTool tracks files and functions in the research context.
func newReadFileTool(fs *FileSystem, rc *ResearchContext) tools.Tool {
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
		lines := countLines(content)
		rc.RecordFile(args.Path, lines)
		funcs := extractFunctions(content)
		for _, f := range funcs {
			rc.RecordFunction(f)
		}
		return readFileResult{Path: args.Path, Content: content, Lines: lines, Functions: funcs}, nil
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

func newPushDocsTool(docsWritten *[]string) tools.Tool {
	return tools.New(tools.ToolSpec{
		Name:        ToolPushDocs,
		Description: "Publish research findings as documentation",
		Parameters:  tools.SchemaFor[pushDocsArgs](),
		Mutates:     true,
	}, func(_ context.Context, args pushDocsArgs) (pushDocsResult, error) {
		doc := fmt.Sprintf("# %s\n\n%s", args.Title, args.Content)
		*docsWritten = append(*docsWritten, doc)
		return pushDocsResult{Title: args.Title, Length: len(doc), Index: len(*docsWritten)}, nil
	})
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
