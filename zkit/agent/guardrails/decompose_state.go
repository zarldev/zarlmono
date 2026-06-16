package guardrails

import (
	"sync"

	"github.com/zarldev/zarlmono/zkit/agent/taskscope"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// decomposeBucket holds the per-task failure counters. The outer
// DecomposeGuardrail.mu guards bucket creation; this inner mutex
// guards the maps inside one bucket so concurrent tool dispatches
// within one task don't race.
type decomposeBucket struct {
	mu           sync.Mutex
	failures     map[string]int
	toolFailures map[tools.ToolName]int
	// toolValidationFailures counts, per tool, how many of that tool's
	// failures were Kind=Validation — i.e. the tool rejecting malformed
	// input rather than failing on its own. The tool-wide "this tool may
	// be unreliable, switch" nudge is wrong when every failure was the
	// model handing the tool bad input, so the advisory branches on this.
	toolValidationFailures map[tools.ToolName]int
	// toolStaleFailures counts, per tool, how many of that tool's failures
	// were Kind=Stale — the args were well-formed but the target moved
	// under them (e.g. a line/hash anchor went stale because the file
	// changed since it was read). The fix is to re-read the target, not to
	// fix the input format or switch tools, so the advisory branches on this.
	toolStaleFailures map[tools.ToolName]int
	toolNudged        map[tools.ToolName]bool
	triggered         int
}

func (g *DecomposeGuardrail) bucketFor(id taskscope.ID) *decomposeBucket {
	g.mu.Lock()
	defer g.mu.Unlock()
	b, ok := g.buckets[id]
	if !ok {
		b = &decomposeBucket{
			failures:               make(map[string]int),
			toolFailures:           make(map[tools.ToolName]int),
			toolValidationFailures: make(map[tools.ToolName]int),
			toolStaleFailures:      make(map[tools.ToolName]int),
			toolNudged:             make(map[tools.ToolName]bool),
		}
		g.buckets[id] = b
	}
	return b
}

// isFailure reports whether the (result, execErr) tuple represents
// a failed dispatch. Hard exec errors (ctx cancellation, panic) and
// tool-level Success=false both count; the guardrail treats them
// identically because the model's recovery options are the same.
func isFailure(result *tools.ToolResult, execErr error) bool {
	if execErr != nil {
		return true
	}
	if result != nil && !result.Success {
		return true
	}
	return false
}

// tupleFailureKind extracts the failure classification from whichever
// side of the (result, execErr) tuple carries it. Used to tell a
// malformed-input rejection (Kind=Validation — the tool worked, the
// args didn't) apart from a tool/environment failure, so the advisory
// can point at the right fix. A hard exec error wins; otherwise it
// defers to failureKind's typed-then-enum read of the result. Returns
// Kinds.UNKNOWN when neither side is typed.
func tupleFailureKind(result *tools.ToolResult, execErr error) tools.Kind {
	if execErr != nil {
		return tools.KindOf(execErr)
	}
	if result != nil {
		return failureKind(result)
	}
	return tools.Kinds.UNKNOWN
}

// originalErrorMessage extracts the human-readable failure description
// from whichever side of the (result, execErr) tuple carries it. Used
// to lead the advisory message so the model retains the real failure
// context instead of being handed pure prescription.
func originalErrorMessage(result *tools.ToolResult, execErr error) string {
	if execErr != nil {
		return execErr.Error()
	}
	if result != nil && result.Error != "" {
		return result.Error
	}
	return "(no error message)"
}
