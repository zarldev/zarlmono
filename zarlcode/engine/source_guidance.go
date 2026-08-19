package engine

import (
	"context"
	"fmt"
	"iter"
	"strings"

	"github.com/zarldev/zarlmono/zarlcode/instructions"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// guidanceSource annotates successful workspace reads with nested instruction
// paths governing the target file. It keeps instruction bodies lazy: the model
// must still call instruction_load before changing files in that subtree.
type guidanceSource struct {
	inner tools.Source
	index instructions.Index
}

func newGuidanceSource(inner tools.Source, docs []instructions.NestedDoc) tools.Source {
	if inner == nil || len(docs) == 0 {
		return inner
	}
	return &guidanceSource{inner: inner, index: instructions.NewIndex(docs)}
}

func (s *guidanceSource) Tools(ctx context.Context) iter.Seq[tools.Tool] {
	return s.inner.Tools(ctx)
}

func (s *guidanceSource) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	result, err := s.inner.Execute(ctx, call)
	if err != nil || result == nil || !result.Success {
		return result, err
	}
	paths := s.index.Applicable(guidanceResultPaths(call, result)...)
	if len(paths) == 0 {
		return result, err
	}
	body, ok := guidanceResultBody(result.Data)
	if !ok {
		return result, err
	}

	var hint strings.Builder
	hint.WriteString("Applicable guidance (load before changing files in this subtree): ")
	for i, path := range paths {
		if i > 0 {
			hint.WriteString(", ")
		}
		fmt.Fprintf(&hint, "`%s`", path)
	}
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	result.Data = body + "\n" + hint.String()
	return result, err
}

// ForgetTask forwards task lifecycle cleanup through guidance annotation.
func (s *guidanceSource) ForgetTask(id taskscope.ID) {
	if forgetter, ok := s.inner.(interface{ ForgetTask(taskscope.ID) }); ok {
		forgetter.ForgetTask(id)
	}
}

func guidanceResultPaths(call tools.ToolCall, result *tools.ToolResult) []string {
	switch call.ToolName {
	case code.ToolNameRead:
		return []string{call.Arguments.String("path", "")}
	case code.ToolNameRetrieveCode:
		if retrieved, ok := result.Data.(code.RetrieveCodeResult); ok {
			return retrieved.Paths()
		}
	}
	return nil
}

func guidanceResultBody(data any) (string, bool) {
	switch value := data.(type) {
	case string:
		return value, true
	case fmt.Stringer:
		return value.String(), true
	default:
		return "", false
	}
}
