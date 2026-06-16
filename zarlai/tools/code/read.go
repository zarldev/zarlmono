package code

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/zarldev/zarlmono/zarlai/service"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
)

const (
	readDefaultLimit = 2000
	readMaxBytes     = 10 * 1024 * 1024 // 10 MB
	binarySniffBytes = 8 * 1024
)

// ReadTool reads a file from the workspace and returns line-numbered content.
type ReadTool struct{ ws Workspace }

func NewReadTool(ws Workspace) *ReadTool { return &ReadTool{ws: ws} }

func (t *ReadTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "read",
		Description: "Read a text file from the workspace. Returns line-numbered content. Refuses binary files and files larger than 10 MB.",
		Parameters: service.Parameters{
			{Name: "path", Type: service.ParamString, Description: "Path relative to the workspace root (or absolute, must be inside root).", Required: true},
			{Name: "offset", Type: service.ParamInteger, Description: "Zero-based line offset to start reading from.", Required: false},
			{Name: "limit", Type: service.ParamInteger, Description: "Maximum number of lines to return (default 2000).", Required: false},
		}.ToJSONSchema(),
	}
}

func (t *ReadTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	path := call.Arguments.String("path", "")
	abs, err := t.ws.Resolve(path)
	if err != nil {
		return tools.Failure(call.ID, tools.Validation("read", err.Error())), nil
	}

	info, err := t.ws.StatInRoot(abs)
	if err != nil {
		return tools.Failure(call.ID, tools.Transient("read", fmt.Errorf("read stat %q: %w", path, err))), nil
	}
	if info.Size() > readMaxBytes {
		return tools.Failure(call.ID, tools.Validation("read", fmt.Sprintf("read %q: file too large (%d bytes, max %d)", path, info.Size(), readMaxBytes))), nil
	}

	data, err := t.ws.ReadFileInRoot(abs)
	if err != nil {
		return tools.Failure(call.ID, tools.Transient("read", fmt.Errorf("read %q: %w", path, err))), nil
	}

	sniffEnd := min(len(data), binarySniffBytes)
	if bytes.IndexByte(data[:sniffEnd], 0) >= 0 {
		return tools.Failure(call.ID, tools.Validation("read", fmt.Sprintf("read %q: binary file refused", path))), nil
	}

	offset := call.Arguments.Int("offset", 0)
	limit := call.Arguments.Int("limit", readDefaultLimit)
	if limit <= 0 {
		limit = readDefaultLimit
	}
	// A model can emit a negative offset (relative-line math gone wrong);
	// clamp it so the slice index below can't panic the whole dispatch.
	if offset < 0 {
		offset = 0
	}

	lines := strings.Split(string(data), "\n")
	if offset >= len(lines) {
		return tools.Success(call.ID, ""), nil
	}
	wantedEnd := offset + limit
	end := min(wantedEnd, len(lines))
	truncated := wantedEnd < len(lines)

	var b strings.Builder
	for i := offset; i < end; i++ {
		fmt.Fprintf(&b, "%d\t%s\n", i+1, lines[i])
	}
	if truncated {
		fmt.Fprintf(&b, "... (truncated at line %d of %d)\n", end, len(lines))
	}
	return tools.Success(call.ID, b.String()), nil
}
