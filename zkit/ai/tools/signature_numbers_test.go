package tools_test

import (
	"encoding/json"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestCallSignaturePreservesExactNumberLeaves(t *testing.T) {
	t.Parallel()
	call := func(number string) tools.ToolCall {
		return tools.ToolCall{ToolName: "exact", Arguments: tools.ToolParameters{"nested": []any{map[string]any{"n": json.Number(number)}}}}
	}
	a, b := call("9007199254740992"), call("9007199254740993")
	if tools.CallSignature(a) == tools.CallSignature(b) {
		t.Fatal("adjacent large numbers collided")
	}
	if tools.CallSignature(a) != tools.CallSignature(call("9007199254740992")) {
		t.Fatal("same exact number unstable")
	}
	// Fidelity is not a promise of mathematical spelling normalization.
	if tools.CallSignature(call("42.0")) == tools.CallSignature(call("42e0")) {
		t.Fatal("numeric spelling unexpectedly normalized")
	}
	raw, err := json.Marshal(b.Arguments)
	if err != nil || string(raw) != `{"nested":[{"n":9007199254740993}]}` {
		t.Fatalf("raw=%s err=%v", raw, err)
	}
}
