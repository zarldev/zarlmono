package toolparse_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm/toolparse"
)

// TestParseArtifactRecoversTransposedDelimiters covers the exact leak: an
// update_plan protocol object the model closed with "}]}}" instead of the valid
// "}}]}". Strict json.Unmarshal rejects it, but each call's name/arguments pair
// is intact and must be recovered.
func TestParseArtifactRecoversTransposedDelimiters(t *testing.T) {
	malformed := `{"tool_calls":[{"id":"call_1","name":"update_plan","arguments":{"plan":[{"step":"Rewrite hero/index — one concrete differentiator","status":"in_progress"},{"step":"Fix the quickstart payoff","status":"pending"}],"explanation":"Starting step 1."}]}}`
	res := toolparse.ParseArtifact(malformed)
	if len(res.Calls) != 1 {
		t.Fatalf("recovered %d calls, want 1 (shape=%q)", len(res.Calls), res.Shape)
	}
	if res.Shape != toolparse.ShapeRecoveredToolCalls {
		t.Fatalf("shape = %q, want %q", res.Shape, toolparse.ShapeRecoveredToolCalls)
	}
	got := res.Calls[0]
	if got.Function.Name != "update_plan" || got.ID != "call_1" {
		t.Fatalf("unexpected call id/name: %q/%q", got.ID, got.Function.Name)
	}
	if want := `"explanation":"Starting step 1."`; !strings.Contains(got.Function.Arguments, want) {
		t.Fatalf("arguments %q missing %q", got.Function.Arguments, want)
	}
	if !strings.HasPrefix(got.Function.Arguments, `{"plan":`) {
		t.Fatalf("arguments not the balanced object: %q", got.Function.Arguments)
	}
}

// TestParseArtifactRecoversMissingCloseBrace covers a truncated/missing trailing
// brace, the other common malformation.
func TestParseArtifactRecoversMissingCloseBrace(t *testing.T) {
	malformed := `{"tool_calls":[{"id":"call_9","name":"bash","arguments":{"command":"echo hi"}` // missing }]}
	res := toolparse.ParseArtifact(malformed)
	if len(res.Calls) != 1 {
		t.Fatalf("recovered %d calls, want 1", len(res.Calls))
	}
	if res.Calls[0].Function.Name != "bash" ||
		res.Calls[0].Function.Arguments != `{"command":"echo hi"}` {
		t.Fatalf("unexpected call: %#v", res.Calls[0])
	}
}

// TestParseArtifactRecoversMultipleMalformedCalls confirms the scan rebuilds
// every call, not just the first, and that "name" keys inside arguments don't
// spawn phantom calls.
func TestParseArtifactRecoversMultipleMalformedCalls(t *testing.T) {
	malformed := `{"tool_calls":[{"id":"a","name":"read","arguments":{"path":"x","name":"ignored"}},{"id":"b","name":"grep","arguments":{"pattern":"TODO"}}]}}`
	res := toolparse.ParseArtifact(malformed)
	if len(res.Calls) != 2 {
		t.Fatalf("recovered %d calls, want 2: %#v", len(res.Calls), res.Calls)
	}
	if res.Calls[0].Function.Name != "read" || res.Calls[1].Function.Name != "grep" {
		t.Fatalf("unexpected names: %q, %q", res.Calls[0].Function.Name, res.Calls[1].Function.Name)
	}
	if res.Calls[0].Function.Arguments != `{"path":"x","name":"ignored"}` {
		t.Fatalf("nested name corrupted args: %q", res.Calls[0].Function.Arguments)
	}
}

// TestParseArtifactWellFormedStillTakesStrictPath guards that the loose
// recovery is a true fallback: a valid object is matched by the strict path and
// reports its shape, never the recovered shape.
func TestParseArtifactWellFormedStillTakesStrictPath(t *testing.T) {
	valid := `{"tool_calls":[{"id":"call_1","name":"read","arguments":{"path":"README.md"}}]}`
	res := toolparse.ParseArtifact(valid)
	if res.Shape != toolparse.ShapeProtocolObject {
		t.Fatalf("shape = %q, want %q (strict path)", res.Shape, toolparse.ShapeProtocolObject)
	}
}

// TestParseArtifactNoToolCallsMarkerNoRecovery confirms the recovery refuses to
// engage on text that never claimed to be a tool call.
func TestParseArtifactNoToolCallsMarkerNoRecovery(t *testing.T) {
	prose := `Here is some JSON I am discussing: {"name":"foo","arguments":{"x":1}` // malformed, but no tool_calls marker
	res := toolparse.ParseArtifact(prose)
	if len(res.Calls) != 0 {
		t.Fatalf("recovered %d calls from non-tool-call prose, want 0", len(res.Calls))
	}
}
