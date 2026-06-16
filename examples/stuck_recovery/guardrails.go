package main

import (
	"context"

	"github.com/zarldev/zarlmono/zkit/agent/guardrails"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// BuildGuardrails creates the guardrail chain with DecomposeGuardrail.
// This demonstrates the graduated degradation pattern:
//   - 1-2 failures: pass through (silent)
//   - 3 failures: advisory (suggests spawn_agent)
//   - 4 failures: fatal (blocks execution)
func BuildGuardrails(fs *FileSystem, client runner.Client) []guardrails.Guardrail {
	_, _ = fs, client // reserved for future use
	// Decompose guardrail with custom max decompositions
	// maxDecompositions=3 means after 3 advisories, we stop advising and escalate
	decompose := guardrails.NewDecomposeGuardrail(3)

	// Wire up our custom verdict judge
	decompose = decompose.WithJudge(
		&searchVerdictJudge{},
	)

	// Also add fanout guardrail to limit total tool calls
	// Cap grep at 5 calls to prevent fan-out pattern
	fanout := guardrails.NewFanoutGuardrail(map[tools.ToolName]int{
		ToolGrep: 5,
	})

	return []guardrails.Guardrail{
		decompose,
		fanout,
	}
}

// searchVerdictJudge is a custom judge that shapes the advisory for search failures.
// In production this might use an LLM; here we use rule-based logic for determinism.
type searchVerdictJudge struct{}

// Judge implements guardrails.VerdictJudge.
func (j *searchVerdictJudge) Judge(ctx context.Context, in guardrails.VerdictInput) (guardrails.Verdict, error) {
	// For grep/search tools that keep failing with "not found",
	// recommend spawning a researcher to do broader exploration
	if in.Tool == ToolGrep {
		return guardrails.Verdict{
			Action:    guardrails.ActionSpawnSubagent,
			Rationale: "The pattern was not found. A researcher agent can do a broader codebase exploration to find related code or confirm the function doesn't exist.",
		}, nil
	}

	// Default: suggest smaller scope
	return guardrails.Verdict{
		Action:    guardrails.ActionSmallerScope,
		Rationale: "Try narrowing the search to a specific file or function.",
	}, nil
}
