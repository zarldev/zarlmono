package program_test

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/tools/program"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestProgramOutputRecoveryExactJSONWithoutReplayOrAdmission(t *testing.T) {
	store := program.NewOutputStore()
	t.Cleanup(store.Close)
	executions := 0
	payload := strings.Repeat("🙂\nquoted \"value\" ", 5000)
	inner := &fakeSource{tools: map[tools.ToolName]fakeTool{
		"read": {spec: tools.ToolSpec{Name: "read"}, fn: func(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
			executions++
			result := tools.Success(call.ID, payload)
			result.AdmissionReferences = []tools.AdmissionReference{{Namespace: "completion", ID: "not-delivered"}}
			return result, nil
		}},
	}}
	source, err := program.NewSource(inner, program.WithPolicy(func(tools.ToolSpec) bool { return true }), program.WithOutputRetainer(store))
	if err != nil {
		t.Fatal(err)
	}
	result, err := source.Execute(t.Context(), tools.ToolCall{ID: "outer", ToolName: program.ToolName, Arguments: tools.ToolParameters{"script": `emit(call("read"))`}})
	if err != nil || result.Success || len(result.AdmissionReferences) != 0 {
		t.Fatalf("overflow = %+v, %v", result, err)
	}
	assertOutputErrorKind(t, result.Err, tools.Kinds.BUDGET)
	receipt := outputReceipt(t, result.Error)
	reader := program.NewOutputReadTool(store)
	var recovered strings.Builder
	offset := 0
	for {
		pageResult, err := reader.Execute(t.Context(), tools.ToolCall{ID: "page", Arguments: tools.ToolParameters{"receipt": receipt, "offset": offset}})
		if err != nil || !pageResult.Success || len(pageResult.AdmissionReferences) != 0 {
			t.Fatalf("page = %+v, %v", pageResult, err)
		}
		page := pageResult.Data.(program.OutputPage)
		recovered.WriteString(page.Content)
		offset = page.NextOffset
		if page.Done {
			break
		}
	}
	var got struct {
		OK   bool   `json:"ok"`
		Data string `json:"data"`
	}
	if err := json.Unmarshal([]byte(recovered.String()), &got); err != nil {
		t.Fatal(err)
	}
	if !got.OK || got.Data != payload || executions != 1 {
		t.Fatalf("recovery changed data or replayed: executions=%d", executions)
	}
	if strings.Contains(result.Error, "quoted") {
		t.Fatal("overflow diagnostic leaked payload")
	}
}

func TestProgramOutputRecoveryBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, script    string
		enabled, closed bool
		kind            tools.Kind
		receipt         bool
	}{
		{"disabled", `emit("x" * 70000)`, false, false, tools.Kinds.VALIDATION, false},
		{"string JSON", `emit("x" * 70000)`, true, false, tools.Kinds.BUDGET, true},
		{"over retention cap", `emit("x" * 1048576)`, true, false, tools.Kinds.BUDGET, false},
		{"closed", `emit("x" * 70000)`, true, true, tools.Kinds.BUDGET, false},
		{"invalid key", `emit({1: "x" * 70000})`, true, false, tools.Kinds.VALIDATION, false},
		{"invalid value", `emit({"x": range(10)})`, true, false, tools.Kinds.VALIDATION, false},
		{"deep", "x = \"x\" * 70000\nfor i in range(66):\n  x = [x]\nemit(x)", true, false, tools.Kinds.VALIDATION, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := program.NewOutputStore()
			t.Cleanup(store.Close)
			if tc.closed {
				store.Close()
			}
			source, err := program.NewSource(tools.NewRegistry())
			if err != nil {
				t.Fatal(err)
			}
			if tc.enabled {
				program.WithOutputRetainer(store)(source)
			}
			result, err := source.Execute(t.Context(), tools.ToolCall{ID: "output", ToolName: program.ToolName, Arguments: tools.ToolParameters{"script": tc.script}})
			if err != nil || result.Success {
				t.Fatalf("result = %+v, %v", result, err)
			}
			assertOutputErrorKind(t, result.Err, tc.kind)
			if strings.Contains(result.Error, "receipt=") != tc.receipt {
				t.Fatalf("receipt availability: %s", result.Error)
			}
			if tc.receipt {
				id := outputReceipt(t, result.Error)
				page, err := store.Read(t.Context(), program.OutputReadArgs{Receipt: id})
				if err != nil || !strings.HasPrefix(page.Content, `"xxx`) || page.TotalBytes != 70002 {
					t.Fatalf("retained string is not JSON: %+v, %v", page, err)
				}
			}
		})
	}
}

func TestProgramRecoveryPreservesNormalAndInnerLimits(t *testing.T) {
	store := program.NewOutputStore()
	t.Cleanup(store.Close)
	inner := &fakeSource{tools: map[tools.ToolName]fakeTool{"large": {spec: tools.ToolSpec{Name: "large"}, fn: func(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
		return tools.Success(call.ID, strings.Repeat("x", 300000)), nil
	}}}}
	source, err := program.NewSource(inner, program.WithPolicy(func(tools.ToolSpec) bool { return true }), program.WithOutputRetainer(store))
	if err != nil {
		t.Fatal(err)
	}
	for _, script := range []string{`emit("small")`, `emit(call("large"))`} {
		result, err := source.Execute(t.Context(), tools.ToolCall{ID: "output", ToolName: program.ToolName, Arguments: tools.ToolParameters{"script": script}})
		if err != nil {
			t.Fatal(err)
		}
		if script == `emit("small")` {
			if !result.Success || result.Data.(program.Result).String() != "small" {
				t.Fatalf("normal emission = %+v", result)
			}
		} else {
			var envelope struct {
				OK    bool   `json:"ok"`
				Data  any    `json:"data"`
				Error string `json:"error"`
			}
			if !result.Success {
				t.Fatalf("emitting an inner failure envelope must succeed: %+v", result)
			}
			if err := json.Unmarshal([]byte(result.Data.(program.Result).String()), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.OK || envelope.Data != nil || envelope.Error != "value exceeds 262144 bytes" || strings.Contains(result.Error, "receipt=") {
				t.Fatalf("inner limit bypassed: %+v", result)
			}
		}
	}
	reader := program.NewOutputReadTool(store)
	bad, err := reader.Execute(t.Context(), tools.ToolCall{ID: "bad", Arguments: tools.ToolParameters{"receipt": "unknown", "offset": "bad"}})
	if err != nil || bad.Success {
		t.Fatalf("invalid reader args = %+v, %v", bad, err)
	}
	assertOutputErrorKind(t, bad.Err, tools.Kinds.VALIDATION)
}

func outputReceipt(t *testing.T, message string) string {
	t.Helper()
	_, tail, ok := strings.Cut(message, "receipt=")
	if !ok {
		t.Fatalf("missing receipt: %s", message)
	}
	quoted, _, ok := strings.Cut(tail, ", offset=")
	if !ok {
		t.Fatalf("missing read recipe: %s", message)
	}
	receipt, err := strconv.Unquote(quoted)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}
