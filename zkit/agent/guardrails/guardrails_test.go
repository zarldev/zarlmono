package guardrails_test

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/guardrails"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// --- pre/post guardrail satisfiers used across tests ---

// preGuard satisfies PreCall only. Use in tests that need a
// pre-call check without an accompanying Inspect.
type preGuard struct {
	name string
	fn   func(call tools.ToolCall) error
}

func (p preGuard) Name() string { return p.name }
func (p preGuard) Before(_ context.Context, call tools.ToolCall) error {
	if p.fn == nil {
		return nil
	}
	return p.fn(call)
}

// dualGuard satisfies both PreCall and PostCall. Lets one test exercise
// both phases on the same guardrail.
type dualGuard struct {
	name string
	pre  func(call tools.ToolCall) error
	post func(call tools.ToolCall, result *tools.ToolResult, execErr error) error
}

func (d dualGuard) Name() string { return d.name }
func (d dualGuard) Before(_ context.Context, call tools.ToolCall) error {
	if d.pre == nil {
		return nil
	}
	return d.pre(call)
}
func (d dualGuard) Inspect(_ context.Context, call tools.ToolCall, result *tools.ToolResult, execErr error) error {
	if d.post == nil {
		return nil
	}
	return d.post(call, result, execErr)
}

// fakeSource is a minimal ToolSource: configurable Execute, no tools
// iteration. Tests don't exercise the iteration path through the
// wrapper — Tools is a pure delegation and is covered by reading
// guardrails.go.
type fakeSource struct {
	result *tools.ToolResult
	err    error
	calls  int
}

func (f *fakeSource) Tools(ctx context.Context) iter.Seq[tools.Tool] {
	_ = ctx
	return func(yield func(tools.Tool) bool) {}
}

func (f *fakeSource) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	f.calls++
	if f.result != nil {
		// Stamp the call ID so the test can verify pass-through.
		r := *f.result
		r.ToolCallID = call.ID
		return &r, f.err
	}
	return nil, f.err
}

func makeCall(name, id string) tools.ToolCall {
	return tools.ToolCall{
		ID:        tools.ToolCallID(id),
		ToolName:  tools.ToolName(name),
		Arguments: tools.ToolParameters{},
		CreatedAt: time.Now(),
	}
}

func TestGuardedSource_PassThroughWithNoGuardrails(t *testing.T) {
	inner := &fakeSource{result: &tools.ToolResult{Success: true, Data: "hello"}}
	guarded := guardrails.NewGuardedSource(inner)

	got, err := guarded.Execute(t.Context(), makeCall("test", "c1"))
	if err != nil {
		t.Fatalf("Execute err = %v, want nil", err)
	}
	if !got.Success || got.Data != "hello" {
		t.Errorf("Execute = %+v, want success with hello", got)
	}
	if got.ToolCallID != "c1" {
		t.Errorf("ToolCallID = %q, want c1", got.ToolCallID)
	}
}

func TestGuardedSource_RejectionBecomesFailedResult(t *testing.T) {
	inner := &fakeSource{result: &tools.ToolResult{Success: true, Data: "hello"}}
	reject := guardrails.GuardrailFunc{
		GuardName: "reject",
		Fn: func(_ context.Context, _ tools.ToolCall, _ *tools.ToolResult, _ error) error {
			return errors.New("nope")
		},
	}
	guarded := guardrails.NewGuardedSource(inner, reject)

	got, err := guarded.Execute(t.Context(), makeCall("test", "c2"))
	if err != nil {
		t.Fatalf("Execute err = %v, want nil (rejection is a failed result, not a hard error)", err)
	}
	if got.Success {
		t.Fatalf("got.Success = true, want false")
	}
	if !strings.Contains(got.Error, `guardrail "reject"`) {
		t.Errorf("got.Error = %q, want it to mention guardrail name", got.Error)
	}
	if !strings.Contains(got.Error, "nope") {
		t.Errorf("got.Error = %q, want it to contain the guardrail's message", got.Error)
	}
	if got.ToolCallID != "c2" {
		t.Errorf("ToolCallID = %q, want c2", got.ToolCallID)
	}
}

func TestGuardedSource_FirstRejectionShortCircuits(t *testing.T) {
	inner := &fakeSource{result: &tools.ToolResult{Success: true, Data: "hello"}}
	var secondRan bool
	first := guardrails.GuardrailFunc{
		GuardName: "first",
		Fn: func(_ context.Context, _ tools.ToolCall, _ *tools.ToolResult, _ error) error {
			return errors.New("stop here")
		},
	}
	second := guardrails.GuardrailFunc{
		GuardName: "second",
		Fn: func(_ context.Context, _ tools.ToolCall, _ *tools.ToolResult, _ error) error {
			secondRan = true
			return nil
		},
	}
	guarded := guardrails.NewGuardedSource(inner, first, second)

	_, _ = guarded.Execute(t.Context(), makeCall("test", "c3"))
	if secondRan {
		t.Errorf("second guardrail ran after first rejected; want short-circuit")
	}
}

func TestGuardedSource_AllPassThrough(t *testing.T) {
	inner := &fakeSource{result: &tools.ToolResult{Success: true, Data: "ok"}}
	count := 0
	pass := guardrails.GuardrailFunc{
		GuardName: "pass",
		Fn: func(_ context.Context, _ tools.ToolCall, _ *tools.ToolResult, _ error) error {
			count++
			return nil
		},
	}
	guarded := guardrails.NewGuardedSource(inner, pass, pass, pass)

	got, _ := guarded.Execute(t.Context(), makeCall("test", "c4"))
	if !got.Success {
		t.Errorf("Success = false, want true (all guardrails passed)")
	}
	if count != 3 {
		t.Errorf("guardrail call count = %d, want 3", count)
	}
}

func TestGuardedSource_GuardrailSeesExecError(t *testing.T) {
	wantErr := errors.New("boom")
	inner := &fakeSource{err: wantErr}
	var sawErr error
	probe := guardrails.GuardrailFunc{
		GuardName: "probe",
		Fn: func(_ context.Context, _ tools.ToolCall, _ *tools.ToolResult, execErr error) error {
			sawErr = execErr
			return nil
		},
	}
	guarded := guardrails.NewGuardedSource(inner, probe)

	_, err := guarded.Execute(t.Context(), makeCall("test", "c5"))
	if !errors.Is(err, wantErr) {
		t.Errorf("Execute err = %v, want %v passed through", err, wantErr)
	}
	if !errors.Is(sawErr, wantErr) {
		t.Errorf("guardrail saw execErr = %v, want %v", sawErr, wantErr)
	}
}

func TestNonEmptyResultGuardrail(t *testing.T) {
	for _, tt := range []struct {
		name       string
		result     *tools.ToolResult
		execErr    error
		wantReject bool
	}{
		{name: "nil_data_success_rejects", result: &tools.ToolResult{Success: true, Data: nil}, wantReject: true},
		{name: "empty_string_rejects", result: &tools.ToolResult{Success: true, Data: ""}, wantReject: true},
		{name: "whitespace_rejects", result: &tools.ToolResult{Success: true, Data: "   \n"}, wantReject: true},
		{name: "real_string_passes", result: &tools.ToolResult{Success: true, Data: "hello"}, wantReject: false},
		{name: "map_passes", result: &tools.ToolResult{Success: true, Data: map[string]any{"a": 1}}, wantReject: false},
		{name: "failed_result_passes", result: &tools.ToolResult{Success: false, Error: "x"}, wantReject: false},
		{name: "exec_error_passes", result: nil, execErr: errors.New("boom"), wantReject: false},
		{name: "nil_result_no_error_passes", result: nil, execErr: nil, wantReject: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			g := &guardrails.NonEmptyResultGuardrail{}
			err := g.Inspect(t.Context(), makeCall("any", "x"), tt.result, tt.execErr)
			if (err != nil) != tt.wantReject {
				t.Errorf("Inspect err = %v, wantReject = %v", err, tt.wantReject)
			}
		})
	}
}

func TestNonEmptyResultGuardrail_ScopedByToolName(t *testing.T) {
	g := &guardrails.NonEmptyResultGuardrail{Tools: []tools.ToolName{"watched"}}
	empty := &tools.ToolResult{Success: true, Data: ""}

	if err := g.Inspect(t.Context(), makeCall("watched", "x"), empty, nil); err == nil {
		t.Error("watched tool with empty result: want rejection, got nil")
	}
	if err := g.Inspect(t.Context(), makeCall("other", "x"), empty, nil); err != nil {
		t.Errorf("unwatched tool with empty result: want pass, got %v", err)
	}
}

func TestGuardrailFunc_NilFnIsPassThrough(t *testing.T) {
	g := guardrails.GuardrailFunc{GuardName: "noop"}
	if err := g.Inspect(t.Context(), makeCall("t", "x"), nil, nil); err != nil {
		t.Errorf("nil Fn returned %v, want nil", err)
	}
	if g.Name() != "noop" {
		t.Errorf("Name = %q, want noop", g.Name())
	}
}

func TestGuardrailFunc_AnonymousName(t *testing.T) {
	g := guardrails.GuardrailFunc{}
	if g.Name() != "anonymous" {
		t.Errorf("Name = %q, want anonymous", g.Name())
	}
}

// --- PreCall behaviour ---

func TestGuardedSource_PreCallRejectionShortCircuitsDispatch(t *testing.T) {
	inner := &fakeSource{result: &tools.ToolResult{Success: true, Data: "shouldn't get here"}}
	guard := preGuard{name: "block", fn: func(_ tools.ToolCall) error {
		return errors.New("denied")
	}}
	guarded := guardrails.NewGuardedSource(inner, guard)

	got, err := guarded.Execute(t.Context(), makeCall("test", "p1"))
	if err != nil {
		t.Fatalf("Execute err = %v, want nil (pre-call rejection → failed result, not hard err)", err)
	}
	if inner.calls != 0 {
		t.Errorf("inner.calls = %d, want 0 (dispatch should be short-circuited)", inner.calls)
	}
	if got.Success {
		t.Errorf("Success = true, want false")
	}
	if !strings.Contains(got.Error, `guardrail "block"`) {
		t.Errorf("Error = %q, want guardrail name", got.Error)
	}
}

func TestGuardedSource_PreCallRejectionClassifiesKind(t *testing.T) {
	inner := &fakeSource{result: &tools.ToolResult{Success: true}}
	guard := preGuard{name: "schema", fn: func(_ tools.ToolCall) error {
		return tools.Validation("test", "bad input")
	}}
	guarded := guardrails.NewGuardedSource(inner, guard)

	got, _ := guarded.Execute(t.Context(), makeCall("test", "p2"))
	if got.Err == nil || got.Err.Kind != tools.Kinds.VALIDATION {
		t.Errorf(
			"Err.Kind = %v, want %v (rejection carrying *tools.Error should set Err)",
			got.Err,
			tools.Kinds.VALIDATION,
		)
	}
}

func TestGuardedSource_PreCallPassesThenPostRuns(t *testing.T) {
	inner := &fakeSource{result: &tools.ToolResult{Success: true, Data: "ok"}}
	var postSaw bool
	guard := dualGuard{
		name: "dual",
		pre:  func(_ tools.ToolCall) error { return nil },
		post: func(_ tools.ToolCall, result *tools.ToolResult, _ error) error {
			postSaw = result != nil && result.Success
			return nil
		},
	}
	guarded := guardrails.NewGuardedSource(inner, guard)

	if _, err := guarded.Execute(t.Context(), makeCall("test", "p3")); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if inner.calls != 1 {
		t.Errorf("inner.calls = %d, want 1", inner.calls)
	}
	if !postSaw {
		t.Errorf("post guardrail didn't observe success")
	}
}

// --- SchemaGuardrail ---

// specTool is a minimal tools.Tool yielding a fixed spec — used to
// back the Iterable that SchemaGuardrail consults.
type specTool struct {
	spec tools.ToolSpec
}

func (s specTool) Definition() tools.ToolSpec { return s.spec }
func (s specTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	return &tools.ToolResult{ToolCallID: call.ID, Success: true, Data: "ok"}, nil
}

// stubIter satisfies Iterable from a slice of tools.
type stubIter struct {
	all []tools.Tool
}

func (s stubIter) Tools(ctx context.Context) iter.Seq[tools.Tool] {
	_ = ctx
	return func(yield func(tools.Tool) bool) {
		for _, t := range s.all {
			if !yield(t) {
				return
			}
		}
	}
}

func bashSpec() tools.ToolSpec {
	return tools.ToolSpec{
		Name: "bash",
		Parameters: llm.SchemaFromMap(map[string]any{
			"type":     "object",
			"required": []any{"command"},
			"properties": map[string]any{
				"command":         map[string]any{"type": "string"},
				"timeout_seconds": map[string]any{"type": "integer"},
			},
			"additionalProperties": false,
		}),
	}
}

func TestSchemaGuardrail_RejectsMissingRequired(t *testing.T) {
	iter := stubIter{all: []tools.Tool{specTool{spec: bashSpec()}}}
	g := guardrails.NewSchemaGuardrail(iter)

	call := tools.ToolCall{
		ID:        "x",
		ToolName:  "bash",
		Arguments: tools.ToolParameters{}, // missing "command"
	}
	err := g.Before(t.Context(), call)
	if err == nil {
		t.Fatal("missing required field: want rejection")
	}
	e, ok := errors.AsType[*tools.Error](err)
	if !ok {
		t.Fatalf("err is %T, want *tools.Error", err)
	}
	if e.Kind != tools.Kinds.VALIDATION {
		t.Errorf("Kind = %v, want Validation", e.Kind)
	}
}

func TestSchemaGuardrail_RejectsWrongType(t *testing.T) {
	iter := stubIter{all: []tools.Tool{specTool{spec: bashSpec()}}}
	g := guardrails.NewSchemaGuardrail(iter)

	call := tools.ToolCall{
		ID:        "x",
		ToolName:  "bash",
		Arguments: tools.ToolParameters{"command": 42},
	}
	if err := g.Before(t.Context(), call); err == nil {
		t.Fatal("wrong type: want rejection")
	}
}

func TestSchemaGuardrail_PassesValidArgs(t *testing.T) {
	iter := stubIter{all: []tools.Tool{specTool{spec: bashSpec()}}}
	g := guardrails.NewSchemaGuardrail(iter)

	call := tools.ToolCall{
		ID:       "x",
		ToolName: "bash",
		Arguments: tools.ToolParameters{
			"command":         "ls -la",
			"timeout_seconds": float64(30), // JSON-decoded numbers arrive as float64
		},
	}
	if err := g.Before(t.Context(), call); err != nil {
		t.Errorf("valid args rejected: %v", err)
	}
}

func TestSchemaGuardrail_UnknownToolPasses(t *testing.T) {
	// Schema can't validate a tool it doesn't know — the inner source
	// will surface its own "tool not found" error.
	iter := stubIter{all: []tools.Tool{specTool{spec: bashSpec()}}}
	g := guardrails.NewSchemaGuardrail(iter)

	call := tools.ToolCall{ID: "x", ToolName: "mystery"}
	if err := g.Before(t.Context(), call); err != nil {
		t.Errorf("unknown tool: want pass, got %v", err)
	}
}

func TestSchemaGuardrail_NoSchemaPasses(t *testing.T) {
	// Tool with empty Parameters — nothing to check against.
	spec := tools.ToolSpec{Name: "freeform"}
	iter := stubIter{all: []tools.Tool{specTool{spec: spec}}}
	g := guardrails.NewSchemaGuardrail(iter)

	call := tools.ToolCall{ID: "x", ToolName: "freeform", Arguments: tools.ToolParameters{"anything": "goes"}}
	if err := g.Before(t.Context(), call); err != nil {
		t.Errorf("no-schema tool: want pass, got %v", err)
	}
}

func TestSchemaGuardrail_RejectsAdditionalProperty(t *testing.T) {
	iter := stubIter{all: []tools.Tool{specTool{spec: bashSpec()}}}
	g := guardrails.NewSchemaGuardrail(iter)

	call := tools.ToolCall{
		ID:       "x",
		ToolName: "bash",
		Arguments: tools.ToolParameters{
			"command":    "ls",
			"undeclared": "x",
		},
	}
	if err := g.Before(t.Context(), call); err == nil {
		t.Error("undeclared field with additionalProperties:false: want rejection")
	}
}
