package code

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

const (
	readDefaultLimit = 2000
	readMaxBytes     = 10 * 1024 * 1024 // 10 MB
	binarySniffBytes = 8 * 1024
)

// ReadTool reads a file from the workspace and returns line-numbered content.
type ReadTool struct{ ws Workspace }

// ReadArgs is the typed argument struct ReadTool.Execute decodes
// into via tools.DecodeArgs. Field tags drive both JSON decoding
// and SchemaFor schema generation.
type ReadArgs struct {
	Path   string `json:"path" doc:"Path relative to the workspace root (or absolute, must be inside root)."`
	Offset int    `json:"offset,omitempty" doc:"Zero-based line offset to start reading from."`
	Limit  int    `json:"limit,omitempty" doc:"Maximum number of lines to return (default 2000)."`
}

// NewReadTool returns the file-reading tool bound to ws.
func NewReadTool(ws Workspace) *ReadTool { return &ReadTool{ws: ws} }

// Definition advertises read with path (required), offset, and limit
// parameters; reads never mutate, so Mutates stays false.
func (t *ReadTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        ToolNameRead,
		Description: "Read a text file from the workspace. Returns line-numbered content. Refuses binary files and files larger than 10 MB.",
		Parameters:  tools.SchemaFor[ReadArgs](),
	}
}

// Execute resolves the path inside the workspace, refuses files over
// readMaxBytes (10 MB) before reading, rejects content with a NUL byte
// in the first 8 KB as binary, and returns 1-based line-numbered output
// starting at offset (negative offsets clamp to 0; default limit
// readDefaultLimit = 2000 lines, with a truncation footer when more
// lines remain).
func (t *ReadTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	var args ReadArgs
	if derr := tools.DecodeArgs(call.Arguments, &args); derr != nil {
		return tools.Failure(call.ID, derr), nil
	}
	if args.Path == "" {
		return tools.Failure(call.ID, tools.Validation("read", "path required")), nil
	}
	abs, err := t.ws.Resolve(args.Path)
	if err != nil {
		return tools.Failure(call.ID, tools.Permission("read", err.Error())), nil
	}

	info, err := t.ws.StatInRoot(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return tools.Failure(call.ID, tools.NotFound("read", fmt.Sprintf("%q does not exist", args.Path))), nil
		}
		return tools.Failure(call.ID, tools.Fatal("read", fmt.Errorf("stat %q: %w", args.Path, err))), nil
	}
	if info.Size() > readMaxBytes {
		return tools.Failure(
			call.ID,
			tools.Validation(
				"read",
				fmt.Sprintf("%q: file too large (%d bytes, max %d)", args.Path, info.Size(), readMaxBytes),
			),
		), nil
	}

	data, err := t.ws.ReadFileInRoot(abs)
	if err != nil {
		return tools.Failure(call.ID, tools.Fatal("read", fmt.Errorf("%q: %w", args.Path, err))), nil
	}

	sniffEnd := min(len(data), binarySniffBytes)
	if bytes.IndexByte(data[:sniffEnd], 0) >= 0 {
		return tools.Failure(call.ID, tools.Validation("read", fmt.Sprintf("%q: binary file refused", args.Path))), nil
	}

	limit := args.Limit
	if limit <= 0 {
		limit = readDefaultLimit
	}
	// A model can emit a negative offset (relative-line math gone wrong);
	// clamp it so the slice index below can't panic the whole dispatch.
	if args.Offset < 0 {
		args.Offset = 0
	}

	lines := strings.Split(string(data), "\n")
	if args.Offset >= len(lines) {
		return tools.Success(call.ID, ""), nil
	}
	wantedEnd := args.Offset + limit
	end := min(wantedEnd, len(lines))
	truncated := wantedEnd < len(lines)

	var b strings.Builder
	for i := args.Offset; i < end; i++ {
		fmt.Fprintf(&b, "%d\t%s\n", i+1, lines[i])
	}
	if truncated {
		fmt.Fprintf(&b, "... (truncated at line %d of %d)\n", end, len(lines))
	}
	return tools.Success(call.ID, b.String()), nil
}
