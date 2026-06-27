package runner_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// A tool-call artifact too broken for even toolparse's balanced-bracket
// recovery to rebuild — the stream was truncated right after "arguments":, so
// there is no value to extract and no call to dispatch. The guardrail is the
// backstop for exactly this residue; transposed-but-complete delimiters are
// recovered silently upstream by toolparse and never reach here.
const unrecoverableToolCall = `{"tool_calls":[{"id":"call_1","name":"update_plan","arguments":`

func TestMalformedToolCallDetectorFiresOnLeakedArtifact(t *testing.T) {
	d := runner.MalformedToolCallDetector{MaxCorrections: 1}
	got := d.Inspect(unrecoverableToolCall, nil)
	if got.Correction == "" {
		t.Fatal("expected a correction for the malformed tool-call artifact, got none")
	}
	if got.MaxCorrections != 1 {
		t.Fatalf("MaxCorrections = %d, want 1", got.MaxCorrections)
	}
}

func TestMalformedToolCallDetectorIgnoresProse(t *testing.T) {
	d := runner.MalformedToolCallDetector{}
	if got := d.Inspect("All done — I rewrote the hero copy and tightened the quickstart.", nil); got.Correction != "" {
		t.Fatalf("prose final answer should not trip the guard: %q", got.Correction)
	}
}

func TestMalformedToolCallDetectorIgnoresWellFormedArtifact(t *testing.T) {
	// A valid {"tool_calls":...} object is recoverable, so the guard must not
	// fire on it — the recovery pipeline would already have produced calls.
	valid := `{"tool_calls":[{"id":"call_1","name":"read","arguments":{"path":"README.md"}}]}`
	d := runner.MalformedToolCallDetector{}
	if got := d.Inspect(valid, nil); got.Correction != "" {
		t.Fatalf("well-formed artifact should not trip the guard: %q", got.Correction)
	}
}

func TestMalformedToolCallDetectorIgnoresTurnsWithToolCalls(t *testing.T) {
	d := runner.MalformedToolCallDetector{}
	calls := []llm.ToolCall{{ID: "call_1", Function: llm.ToolCallFunction{Name: "read"}}}
	if got := d.Inspect(unrecoverableToolCall, calls); got.Correction != "" {
		t.Fatalf("a turn that produced tool calls must be left alone: %q", got.Correction)
	}
}

func TestChainTurnQualityReturnsFirstCorrection(t *testing.T) {
	chain := runner.ChainTurnQuality{
		runner.MalformedToolCallDetector{},
		runner.EmptyResponseDetector{Message: "be useful"},
	}
	// Malformed artifact: first detector wins.
	if got := chain.Inspect(unrecoverableToolCall, nil); !strings.Contains(got.Correction, "malformed") {
		t.Fatalf("chain should surface the malformed correction, got %q", got.Correction)
	}
	// Empty turn: malformed detector passes, empty detector fires.
	if got := chain.Inspect("", nil); got.Correction != "be useful" {
		t.Fatalf("chain should fall through to the empty detector, got %q", got.Correction)
	}
}
