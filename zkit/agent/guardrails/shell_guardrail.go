package guardrails

import (
	"context"

	"github.com/zarldev/zarlmono/zkit/agent/shellpolicy"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/options"
)

// ShellGuardrail is a PreCall guardrail that parses the `command`
// argument of the bash tool, runs it through a PolicyEngine, and
// rejects Block decisions before dispatch. The agent sees the
// rejection as a Validation error pointing at a better tool — `cd`
// → use workspace tools; `>` → use write_file — which is the
// "make the right thing easier than the wrong thing" half of the
// harness thesis.
//
// Other tools (read, edit, write…) pass through untouched: the
// guardrail only fires on a configurable shell-tool name. zarlcode
// wires it for ToolNameBash; other consumers can build their own
// ShellGuardrail bound to a different name (e.g. a sandboxed shell
// in zarlai).
type ShellGuardrail struct {
	parser shellpolicy.Parser
	engine *shellpolicy.PolicyEngine
	tool   tools.ToolName
}

// NewShellGuardrail builds a guardrail bound to the given tool
// name. The parser and engine default to the Unix parser and a
// zero-value PolicyEngine; override with WithShellParser and
// WithShellPolicyEngine.
func NewShellGuardrail(tool tools.ToolName, opts ...options.Option[ShellGuardrail]) *ShellGuardrail {
	g := &ShellGuardrail{
		tool:   tool,
		parser: shellpolicy.NewUnixParser(),
		engine: shellpolicy.NewPolicyEngine(),
	}
	for _, o := range opts {
		o(g)
	}
	return g
}

// WithShellParser overrides the default Unix shell parser.
func WithShellParser(p shellpolicy.Parser) options.Option[ShellGuardrail] {
	return func(g *ShellGuardrail) { g.parser = p }
}

// WithShellPolicyEngine overrides the default policy engine.
func WithShellPolicyEngine(e *shellpolicy.PolicyEngine) options.Option[ShellGuardrail] {
	return func(g *ShellGuardrail) { g.engine = e }
}

// Name returns the guardrail's identifier — surfaces in the failed
// ToolResult's Error string as `guardrail "shell_policy": …`.
func (g *ShellGuardrail) Name() string { return "shell_policy" }

// Before parses call.Arguments["command"] (when the call targets
// the configured shell tool) and returns a tools.Validation error
// for any Block decision. Unbound tools, missing commands, and
// pass decisions all return nil so dispatch proceeds.
//
// When ctx carries the verify work mode (planted by the spawn tool on a
// verify-mode sub-agent's Run), the stricter verify profile applies: the
// standard rules plus write-target and mutating-command blocks, so a
// verify sub-agent can run tests and builds but not modify the workspace
// through the shell. See [shellpolicy.PolicyEngine.DecideVerify].
func (g *ShellGuardrail) Before(ctx context.Context, call tools.ToolCall) error {
	if call.ToolName != g.tool {
		return nil
	}
	cmd := call.Arguments.String("command", "")
	if cmd == "" {
		// Let the tool's own validation surface the empty-command
		// error — that path already produces a clean message.
		return nil
	}
	ir, _ := g.parser.Parse(cmd)
	// Parse errors are reflected in the IR's RiskFlags; ignore the
	// returned error and let Decide drive policy uniformly.
	var decision shellpolicy.Decision
	if taskscope.WorkModeFrom(ctx) == taskscope.WorkModes.VERIFY {
		// An unparseable command yields no targets AND a syntax risk
		// flag, which the standard rules inside DecideVerify block on —
		// so the dropped error here can't admit an unanalysed command.
		targets, _ := shellpolicy.WriteTargets(cmd)
		decision = g.engine.DecideVerify(ir, targets)
	} else {
		decision = g.engine.Decide(ir)
	}
	if !decision.IsBlocked {
		return nil
	}
	return tools.Validation(call.ToolName.String(), decision.BlockReason)
}
