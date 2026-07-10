package guardrails_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/guardrails"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// recordingVerifier captures Verify calls and returns a configurable
// error. Lets the guardrail tests exercise routing + rejection
// without invoking a real toolchain.
type recordingVerifier struct {
	mu    sync.Mutex
	name  string
	exts  []string
	err   error
	calls [][]string // each entry: the paths slice passed to a single Verify call
}

func (v *recordingVerifier) Name() string         { return v.name }
func (v *recordingVerifier) Extensions() []string { return v.exts }
func (v *recordingVerifier) Verify(_ context.Context, _ string, paths []string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	cp := append([]string(nil), paths...)
	v.calls = append(v.calls, cp)
	return v.err
}

func writeCall(id, path string) tools.ToolCall {
	return tools.ToolCall{
		ID:        tools.ToolCallID(id),
		ToolName:  "write",
		Arguments: tools.ToolParameters{"path": path},
	}
}

func TestImprovementGuardrail_FiresOnWatchedToolSuccess(t *testing.T) {
	v := &recordingVerifier{name: "go_verifier", exts: []string{".go"}}
	g := guardrails.NewImprovementGuardrail("/ws", nil, v)
	result := &tools.ToolResult{Success: true, Data: "wrote"}

	if err := g.Inspect(t.Context(), writeCall("c1", "pkg/foo/foo.go"), result, nil); err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(v.calls) != 1 {
		t.Fatalf("verifier calls = %d, want 1", len(v.calls))
	}
	if got := v.calls[0]; len(got) != 1 || got[0] != "pkg/foo/foo.go" {
		t.Errorf("verifier got paths %v, want [pkg/foo/foo.go]", got)
	}
}

func TestImprovementGuardrail_UsesResultEffects(t *testing.T) {
	v := &recordingVerifier{name: "go_verifier", exts: []string{".go"}}
	g := guardrails.NewImprovementGuardrail("/ws", nil, v)
	result := &tools.ToolResult{Success: true}
	result.AddEffect(tools.NewFileEffect(tools.FileModify, "pkg/foo/foo.go"))

	call := tools.ToolCall{ID: "c1", ToolName: "apply_patch"}
	if err := g.Inspect(t.Context(), call, result, nil); err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(v.calls) != 1 {
		t.Fatalf("verifier calls = %d, want 1", len(v.calls))
	}
	if got := v.calls[0]; len(got) != 1 || got[0] != "pkg/foo/foo.go" {
		t.Errorf("verifier got paths %v, want [pkg/foo/foo.go]", got)
	}
}

func TestImprovementGuardrail_IgnoresDeleteEffects(t *testing.T) {
	v := &recordingVerifier{name: "go_verifier", exts: []string{".go"}}
	g := guardrails.NewImprovementGuardrail("/ws", nil, v)
	result := &tools.ToolResult{Success: true}
	result.AddEffect(tools.NewFileEffect(tools.FileDelete, "pkg/foo/foo.go"))

	call := tools.ToolCall{ID: "c1", ToolName: "apply_patch"}
	if err := g.Inspect(t.Context(), call, result, nil); err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(v.calls) != 0 {
		t.Fatalf("verifier calls = %d, want 0", len(v.calls))
	}
}

func TestImprovementGuardrail_AdvisesOnVerifierFailure(t *testing.T) {
	v := &recordingVerifier{name: "go_verifier", exts: []string{".go"}, err: errors.New("foo.go:3: unreachable code")}
	g := guardrails.NewImprovementGuardrail("/ws", nil, v)
	result := &tools.ToolResult{Success: true, Data: "wrote"}

	// A failing verifier is advisory: the call stays successful and is
	// never rejected; the diagnostic is annotated onto the result.
	if err := g.Inspect(t.Context(), writeCall("c2", "foo.go"), result, nil); err != nil {
		t.Fatalf("Inspect: want nil (advisory only), got %v", err)
	}
	if !result.Success {
		t.Error("result.Success = false; advisory must not fail the call")
	}
	data, ok := result.Data.(string)
	if !ok {
		t.Fatalf("result.Data is %T, want string", result.Data)
	}
	if !strings.Contains(data, "unreachable code") {
		t.Errorf("Data = %q, want it to include the diagnostic", data)
	}
	if !strings.Contains(data, "advisory") {
		t.Errorf("Data = %q, want it framed as an advisory", data)
	}
}

func TestImprovementGuardrail_SkipsUnwatchedTool(t *testing.T) {
	v := &recordingVerifier{name: "go_verifier", exts: []string{".go"}}
	g := guardrails.NewImprovementGuardrail("/ws", nil, v)

	call := tools.ToolCall{ID: "c", ToolName: "read", Arguments: tools.ToolParameters{"path": "foo.go"}}
	if err := g.Inspect(t.Context(), call, &tools.ToolResult{Success: true}, nil); err != nil {
		t.Errorf("read call: want pass, got %v", err)
	}
	if len(v.calls) != 0 {
		t.Errorf("verifier ran %d times on unwatched tool; want 0", len(v.calls))
	}
}

func TestImprovementGuardrail_SkipsOnFailedResult(t *testing.T) {
	v := &recordingVerifier{name: "go_verifier", exts: []string{".go"}}
	g := guardrails.NewImprovementGuardrail("/ws", nil, v)
	result := &tools.ToolResult{Success: false, Error: "tool errored"}

	if err := g.Inspect(t.Context(), writeCall("c", "foo.go"), result, nil); err != nil {
		t.Errorf("failed tool: want pass, got %v", err)
	}
	if len(v.calls) != 0 {
		t.Errorf("verifier ran %d times after tool failure; want 0", len(v.calls))
	}
}

func TestImprovementGuardrail_SkipsOnExecError(t *testing.T) {
	v := &recordingVerifier{name: "go_verifier", exts: []string{".go"}}
	g := guardrails.NewImprovementGuardrail("/ws", nil, v)

	if err := g.Inspect(t.Context(), writeCall("c", "foo.go"), nil, errors.New("boom")); err != nil {
		t.Errorf("exec error: want pass, got %v", err)
	}
}

func TestImprovementGuardrail_SkipsUnhandledExtension(t *testing.T) {
	// Only a .go verifier is wired; touching README.md should pass
	// silently rather than failing the call.
	v := &recordingVerifier{name: "go_verifier", exts: []string{".go"}}
	g := guardrails.NewImprovementGuardrail("/ws", nil, v)

	err := g.Inspect(t.Context(), writeCall("c", "README.md"), &tools.ToolResult{Success: true}, nil)
	if err != nil {
		t.Errorf("README touch: want pass, got %v", err)
	}
	if len(v.calls) != 0 {
		t.Errorf("verifier ran %d times for .md; want 0", len(v.calls))
	}
}

func TestImprovementGuardrail_RoutesByExtension(t *testing.T) {
	goV := &recordingVerifier{name: "go", exts: []string{".go"}}
	tsV := &recordingVerifier{name: "ts", exts: []string{".ts"}}
	g := guardrails.NewImprovementGuardrail("/ws", nil, goV, tsV)

	_ = g.Inspect(t.Context(), writeCall("c1", "foo.go"), &tools.ToolResult{Success: true}, nil)
	_ = g.Inspect(t.Context(), writeCall("c2", "foo.ts"), &tools.ToolResult{Success: true}, nil)

	if len(goV.calls) != 1 {
		t.Errorf("go verifier calls = %d, want 1", len(goV.calls))
	}
	if len(tsV.calls) != 1 {
		t.Errorf("ts verifier calls = %d, want 1", len(tsV.calls))
	}
}

func TestImprovementGuardrail_CustomWatchList(t *testing.T) {
	// Wire only "edit" — write should be ignored.
	v := &recordingVerifier{name: "go_verifier", exts: []string{".go"}}
	g := guardrails.NewImprovementGuardrail("/ws", []tools.ToolName{"edit"}, v)

	_ = g.Inspect(t.Context(), writeCall("c", "foo.go"), &tools.ToolResult{Success: true}, nil)
	if len(v.calls) != 0 {
		t.Errorf("write was watched despite custom list; calls = %d", len(v.calls))
	}

	editCall := tools.ToolCall{ID: "c2", ToolName: "edit", Arguments: tools.ToolParameters{"path": "foo.go"}}
	_ = g.Inspect(t.Context(), editCall, &tools.ToolResult{Success: true}, nil)
	if len(v.calls) != 1 {
		t.Errorf("edit was not watched; calls = %d", len(v.calls))
	}
}
