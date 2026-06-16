---
title: Guardrails
description: Middleware on tool dispatch — each one targets a failure mode observed in real agent runs, and most of them nudge rather than block.
---

The runner consumes tools through a `ToolSource`.
`guardrails.NewGuardedSource(inner, guards...)` wraps the source so
every dispatch flows through a middleware chain. A guardrail can
repair arguments before dispatch, reject a call with a corrective
message, or annotate the result with an advisory the model sees next
turn.

Two principles:

- **Advisory over prescriptive.** Most guardrails append soft hints
  rather than block — the model keeps its agency, the original error
  stays primary, and escalation to rejection happens only after
  repeated failure.
- **Per-task state, no globals.** Stateful guardrails key their
  buckets by task ID from ctx and drop them when the task ends.

Guardrails run in registration order; later ones see the output of
earlier ones. Seven guardrails ship in the production chain, in a
fixed order assembled by `coderunner`. Each one either inspects and
rejects *before* dispatch, annotates the result *after*, or both.

## The roster
### Schema — repair, then name the field

Validates tool-call args against the tool's JSON Schema. On
mismatch it tries cheap repairs first (snake_case↔camelCase key
renames, string→int coercion); what can't be repaired comes back as
a validation error naming the failing field — "expected `path`, got
`file_path`" beats "args invalid" by a full retry.

### Shell policy — don't erase the world

Filters `bash` invocations: known-destructive patterns are rejected,
non-portable shell features draw a warning, absolute paths escaping
the workspace get flagged. Not a sandbox and not exhaustive — that's
what [kernel confinement](/zarlmono/code-tools/#sandboxing) is for —
just the cheap first line.

### Skill hint — discover then read

When the conversation touches a capability the workspace has a
[skill](https://github.com/zarldev/zarlmono/tree/main/zkit/skills)
for, inject a hint to go read it. Purely a nudge.

### Decompose — break repetition

Catches the stuck-agent signature: the **same call signature three
times** draws an advisory ("you've tried this exact call 3 times —
consider a different approach or delegate"), the **same tool four
times** regardless of args draws a tool-level hint, and continued
identical failures escalate to rejection. With an optional judge
model wired, the advisory includes a recovery recommendation.
Signature canonicalisation is shared with the memo cache, so "same
call" means semantically identical arguments, not byte equality.

### Fan-out — exploration budgets

Per-tool call budgets per task. At the limit, a validation nudge
names the better move ("you've read 30 files — if you're mapping a
directory, delegate to spawn_agent"); past it, continued rejection
so the cap can't be brute-forced. The spawn budget exists because
large models discover fan-out and immediately rate-limit themselves
into the ground.

### Test edit — a note, not a wall

Editing a test file appends an advisory to the result. Interactive
sessions treat it as information (you might *want* the test
changed); headless eval runs pair it with a system-prompt rule
against test edits, making the guardrail a safety belt. Pick the
slot at construction: `NewTestEditAdvisory` only annotates;
`NewTestEditStrict` rejects the edit outright.

### Improvement — verifiers after every edit

After a code-modifying call (`edit`, `write`, `apply_patch`), run
language verifiers against the **packages the agent actually
touched** — never the whole module:

- a vet-level check, always on: sub-second, catches syntax and
  obvious semantic errors before the model builds on top of them
- a test verifier, opt-in (auto-enabled headless): slower, catches
  regressions

Failures surface as validation results the model reacts to on its
next iteration — the feedback loop that turns "compiles in the
model's imagination" into "compiles".

Scoping to touched packages is load-bearing: full-module verification
on a big repo sends the model chasing pre-existing, unrelated
failures it didn't cause and can't fix.

The improvement guardrail doesn't know any language itself — it
delegates to whatever `Verifier` values you hand it. Two ship in the
box, and both are plain struct values you pass into the constructor:

- **`&guardrails.GoVerifier{}`** — runs `go vet` against only the
  packages the agent touched. Sub-second, catches syntax and obvious
  semantic errors.
- **`&guardrails.GoTestVerifier{}`** — runs `go test` against the
  touched packages. Slower but catches regressions. Auto-enabled in
  headless eval runs; opt-in interactively.

Both surface failures as validation results the model reacts to on
its next iteration — the feedback loop that turns "compiles in the
model's imagination" into "compiles on disk." Implement the
`Verifier` interface (`Extensions`, `Verify`) to add a language.

## The chain, assembled

The production chain in its canonical order. `coderunner` builds this
for you — `guardrails.PostSchemaGuardrails(deps)` returns the
post-schema set, and `coderunner.GuardedSource` wires it over a tool
source. The explicit form is shown here so the order and arguments
are visible:

```go
import "github.com/zarldev/zarlmono/zkit/ai/tools/code"

source := guardrails.NewGuardedSource(registry,
    guardrails.NewSchemaGuardrail(registry),               // repair malformed args
    guardrails.NewShellGuardrail(code.ToolNameBash),       // reject destructive patterns
    guardrails.NewSkillHintGuardrail(skillLookup),         // nudge to read skills
    guardrails.NewDecomposeGuardrail(0),                   // break repetition loops
    guardrails.NewFanoutGuardrail(limits),                 // cap exploration budgets
    guardrails.NewTestEditAdvisory(),                      // note test-file edits (or …Strict)
    guardrails.NewImprovementGuardrail(workspaceRoot, nil, // verify after every edit
        &guardrails.GoVerifier{},                          // go vet on touched packages
        &guardrails.GoTestVerifier{},                      // go test on touched packages
    ),
)
r := runner.New(client, runner.WithTools(source))
```

To merge several tool domains — built-in tools, `new_tool`-authored
dynamic tools, MCP-connected tools — under one guarded surface, the
runner reads them through
[`sourcechain`](/zarlmono/foundation/#agent-infrastructure), which
resolves a tool name across sources at dispatch time. The guardrail
chain wraps whatever source it's given, composite or not.

The chain is policy only — telemetry belongs in the runner's
`EventSink`, which sees every call and result at the edge regardless
of what the chain decided. Production consumers assemble the
standard chain through `zkit/agent/coderunner`, so the interactive
TUI and the eval harness can't drift apart.