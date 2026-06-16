# Common Patterns

Patterns for building systems on the zarlcode harness, runner, tool, and guardrail packages. Each pattern includes a short description, the problem it solves, and a code sketch.

---

## Pattern: Tool Effect Verification

Tools should verify their own effects against the world, not just report success.

```go
func (t *upvoteTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
    // 1. Perform the action
    t.page.Click(".upvote-button")

    // 2. Verify the effect
    count := t.page.QuerySelector(".vote-count").TextContent()
    t.session.RecordUpvote(count)

    // 3. Return verified result
    return tools.Success(call.ID, map[string]any{
        "vote_count": count,
        "verified":   true,
    }), nil
}
```

**Why**: The model might claim success even when the tool silently failed. Verification against the world catches these cases.

**Examples**: `hnupvote` (browser verification), `healthcheck` (endpoint health check)

---

## Pattern: Guardrail as Feedback

Guardrails aren't just blocks — they're corrective hints that guide the model.

```go
func (g *authGuardrail) Before(ctx context.Context, call tools.ToolCall) error {
    if call.ToolName == "upvote" && !g.user.IsAuthenticated {
        return tools.Validation(string(call.ToolName),
            "You are not logged in. Call the login tool first.")
    }
    return nil
}
```

**Why**: A validation error tells the model *why* it was blocked and *what to do next*, rather than just failing silently.

**Examples**: `releasegate` (pre-call build verification), `hnupvote` (auth gate)

---

## Pattern: Oracle Over World State

Always verify task completion against world state, never trust the model's claim.

```go
goal := pursue.GoalFunc(func(_ context.Context, attempt pursue.Attempt) pursue.Decision {
    if fs.RefactorComplete() {
        return pursue.Done()
    }
    return pursue.Retry("JWT refactor not complete. Continue.")
})
```

**Why**: Models hallucinate success. The harness should re-drive until the world matches the goal, not until the model says "done".

**Examples**: `spawn_worker` (filesystem check), `healthcheck` (all endpoints healthy)

---

## Pattern: Scripted Testing

Use `runnertest.NewClient` for deterministic, fast tests that don't need an LLM.

```go
func TestFoo(t *testing.T) {
    client := runnertest.NewClient([][]llm.CompletionChunk{
        // Turn 1: The model calls tool A
        {runnertest.ChunkToolCall("c1", string(ToolA), `{}`), runnertest.ChunkDone()},
        // Turn 2: The model calls tool B
        {runnertest.ChunkToolCall("c2", string(ToolB), `{"arg": "value"}`), runnertest.ChunkDone()},
    })
    // ... run harness with client
}
```

**Why**: Tests that call real LLMs are slow, expensive, and non-deterministic. Scripted clients let you test the full stack (runner, tools, guardrails, harness) without an LLM.

**Examples**: All examples have scripted test variants.

---

## Pattern: Graduated Degradation

Don't hard-stop on first failure — escalate gradually from pass-through → advise → fatal.

```go
// DecomposeGuardrail built-in thresholds:
//   - 1-2 failures: pass through (model self-corrects)
//   - 3 failures: advisory (actionable hint)
//   - 4 failures: fatal (hard stop)

decompose := guardrails.NewDecomposeGuardrail(3) // max 3 advisories
```

**Why**: Models often self-correct in 1-2 retries. Guardrail should stay out of the way until the model is demonstrably stuck.

**Examples**: `stuck_recovery`

---

## Pattern: Capability-Based Tool Gating

Block tools by what they *do*, not what they're *named*.

```go
policy := func(mode spawn.SpawnMode, spec tools.ToolSpec) bool {
    if mode == spawn.SpawnModeExplore && spec.Mutates {
        return false
    }
    return true
}
```

**Why**: Dynamic tools, MCP tools, and renamed tools don't appear in hardcoded lists. Capability-based gating (`Mutates`, `Instrumental`) catches all of them.

**Examples**: `spawn_worker`, `coderunner.SpawnModePolicy`

---

## Pattern: Named Agent Dispatch

When work varies by domain, dispatch to specialized agents with different prompts and tools.

```go
resolver := func(name string) (*runner.Runner, error) {
    switch name {
    case "researcher":
        return runner.New(client,
            runner.WithTools(readOnlyTools),
            runner.WithPromptText("You are a researcher. Explore code, never modify."),
        ), nil
    case "coder":
        return runner.New(client,
            runner.WithTools(fullTools),
            runner.WithPromptText("You are a coder. Write and modify files as needed."),
        ), nil
    }
    return nil, nil
}
```

**Why**: Specialized prompts + limited tool surfaces produce better results than one monolithic agent doing everything.

**Examples**: `spawn_worker`

---

## Pattern: Compactor Integration

Wire a compactor to let the runner handle context pressure automatically.

```go
r := runner.New(client,
    runner.WithTools(reg),
    runner.WithCompactor(compact.NewStructural()), // no-model trimming
)

// Or with model-based summarization:
r := runner.New(client,
    runner.WithTools(reg),
    runner.WithCompactor(compact.NewSummary(summaryClient)), // LLM summarization
)
```

**Why**: Context windows fill up during research-heavy tasks. The compactor trims stale content while preserving key findings.

**Examples**: `long_conversation`

---

## Pattern: Fanout Guardrail

Prevent the model from reading file-by-file when it should delegate to `spawn_agent`.

```go
import "github.com/zarldev/zarlmono/zkit/ai/tools/code"

fanout := guardrails.NewFanoutGuardrail(map[tools.ToolName]int{
    code.ToolNameRead: 10, // max 10 file reads per task
    code.ToolNameGrep: 5,  // max 5 searches per task
})
```

**Why**: A model exploring a 200-file project via individual `read` calls burns context. The fanout guardrail nudges it toward `spawn_agent` for bulk exploration.

**Examples**: `stuck_recovery`, `healthcheck`

---

## Pattern: Verdict Judge

Use an LLM-based judge to shape advisory messages when the model is stuck.

```go
type myJudge struct{}

func (j *myJudge) Judge(ctx context.Context, in guardrails.VerdictInput) (guardrails.Verdict, error) {
    // LLM analyzes the failure pattern and returns a tailored verdict
    return guardrails.Verdict{
        Action:    guardrails.ActionSpawnSubagent,
        Rationale: "The target file is large and complex. Delegating to researcher...",
    }, nil
}
```

**Why**: A rule-based default advisory works for common cases, but an LLM judge can produce context-aware suggestions.

**Examples**: `stuck_recovery`

---

## Combining Patterns

Most real systems combine multiple patterns:

```go
func BuildHarness(fs *FileSystem) pursue.Outcome {
    // 1. Capability-based tool gating
    spawnTool := spawn.New(parent,
        spawn.WithModeToolPolicy(capabilityPolicy),
    )

    // 2. Fanout guardrail + graduated degradation
    rails := []guardrails.Guardrail{
        guardrails.NewDecomposeGuardrail(3).WithJudge(myJudge),
        guardrails.NewFanoutGuardrail(fanoutLimits),
    }

    // 3. Guarded tool source
    source := guardrails.NewGuardedSource(reg, rails...)

    // 4. Compactor for long tasks
    r := runner.New(client,
        runner.WithTools(source),
        runner.WithCompactor(compact.NewStructural()),
    )

    // 5. World-state oracle
    return pursue.Drive(ctx, pursue.NewRequest(r.Run, spec,
        pursue.WithGoal(worldOracle),
    ))
}
```
