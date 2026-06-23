# hnupvote

A worked example of a **deterministic harness wrapped around a non-deterministic LLM loop**. The model decides what to do; the harness decides when the job is actually done — by checking the world, not by trusting the model's claim of success.

The concrete task: drive a real Chrome (via chromedp) to upvote the top post on Hacker News, logging in if a login wall appears, and keep re-driving the model until a programmatic oracle confirms the vote registered against the live page.

It performs a real, authenticated action on a live site, so it is gated behind `-confirm`.

## The idea

An agent loop is non-deterministic: the model may upvote, may narrate, may claim success it didn't achieve, may take a wrong turn and recover. None of that is under your control. What *is* under your control is everything around the loop:

- **Tools** are the only way the model can affect the world. Make them dumb actuators that act and then verify their own effect.
- **Guardrails** are preconditions enforced *before* a tool runs. They are how you encode rules the model must not violate (here: "you cannot upvote until you are logged in").
- **The oracle** is a predicate over real-world state that decides whether the goal is met. It is the harness's definition of done, and it does not trust the model.
- **Re-drive** is the loop above the loop: if the oracle says "not done", feed the model corrective feedback and let it try again, up to a budget.

The model is free to be wrong inside any single run. The harness is what makes the *outcome* deterministic: either the world reaches the goal state (`Succeeded`), the budget is exhausted (`GaveUp`), or something broke (`Errored`).

## Layers

```
pursue.Drive          run-until-goal: oracle + corrective re-drive + attempt budget
  └── r.Run             one agent loop: stream model, dispatch tools, repeat
        ├── Client          the LLM (`zkit/ai/llm` provider behind runner.Client)
        ├── ToolSource      tools the model can call …
        │     └── guardrails.GuardedSource   … wrapped with preconditions
        └── EventSink       observability (per-tool progress here)
```

The dependency points one way: `pursue` → `runner`. The runner knows nothing about pursue. "Headless" (run once, trust the model's terminal state, no verification) is just the degenerate case: one attempt with the `AcceptCompleted` oracle.

Browser access sits behind a small `Page` port (`port.go`), so the tools depend on an interface, not on chromedp. The chromedp adapter (`chromedp.go`) is the only file that imports chromedp; the tests swap in a fake page and exercise the entire flow with no browser and no LLM.

## Running it

Prerequisites: Go, Chrome installed, and an LLM backend.

```sh
cp examples/hnupvote/.env.example examples/hnupvote/.env
# edit .env: set HN_USER / HN_PASS and pick an LLM backend
go run ./examples/hnupvote -confirm
```

`.env` is gitignored. By default the LLM backend is `openai-codex`, which reuses zarlcode's encrypted vault — the ChatGPT OAuth credential you logged in with via `zarlcode keys oauth openai-codex` drives the agent and auto-refreshes. No LLM secret goes in `.env`. Set `LLM_PROVIDER` to anything else to use an OpenAI-compatible endpoint via `OPENAI_API_KEY` / `LLM_BASE_URL`. See `.env.example` and `provider.go`.

Flags:

| flag | default | meaning |
|------|---------|---------|
| `-confirm` | `false` | required; without it the program refuses to run (real action) |
| `-headless` | `true` | run Chrome headless; `-headless=false` opens a visible window |
| `-attempts` | `4` | maximum harness re-drive attempts |

Output is a per-tool progress trace on stderr followed by a final status line:

```
  → hn_upvote_top
  ✗ hn_upvote_top: guardrail "require_auth": ... you are not logged in ...
  → hn_login
  ✓ hn_login
  → hn_upvote_top
  ✓ hn_upvote_top
attempt 1/4: goal met (state="upvoted")
status=succeeded attempts=1 verified_upvoted=true title="..." last_state="upvoted"
```

The first upvote is blocked by the guardrail (not logged in), the model logs in, the second upvote registers and is verified, and the harness stops. Exit code is non-zero unless `status=succeeded`.

Note on headless: Hacker News refuses logins from a browser advertising `HeadlessChrome` / `navigator.webdriver`, so the headless path presents a normal User-Agent and disables the automation flag (`chromedp.go`). If a login still bounces back to `/login`, run with `-headless=false`.

## How this one works

`RunUpvote` (`harness.go`) is the whole wiring:

1. Register the two tools (`hn_upvote_top`, `hn_login`) in a `tools.Registry`.
2. Wrap the registry in a `GuardedSource` with a `RequireAuth` rail that gates `hn_upvote_top` on `Session.LoggedIn`. The model cannot upvote before login; the rail returns a validation error the model reads as feedback.
3. Build the runner with the system prompt, an iteration cap, and the progress sink.
4. Define the oracle as a closure over the session: done iff `Session.VerifiedUpvoted()`. Otherwise it returns corrective feedback naming the current state.
5. Hand all of it to `pursue.Drive` with an attempt budget.

The tools are dumb actuators. `hn_login` fills the form and confirms success by waiting for the top-bar username link (`user?id=<account>`), which only exists when authenticated — that distinguishes a real login from a bad-credentials/captcha page. `hn_upvote_top` clicks the top arrow and confirms the vote by reading computed visibility in the page (HN hides the arrow with `visibility:hidden`, which the usual visibility waits miss), then records the post title. Neither tool reports success it can't see.

**Early termination.** Once the vote is verified there is nothing left to do, but the model might keep acting (re-clicking a now-hidden arrow, narrating). A `WithProgressUpdater` hook checks the oracle predicate at each iteration boundary and cancels the run the instant it's satisfied. Because that fires *after* the tool's completion is booked, the runner returns cleanly (`TerminalCancelled`, nil error, cause on `TaskResult.Err`) with no spurious failed-tool event. The oracle then reports success against the world. `attemptLabel` interprets the typed terminal reason against world state, so the success path reads `goal met` rather than leaking the `cancelled` mechanism.

## Composing your own harness

The pattern is reusable for any "drive an agent until a verifiable goal" task. Five pieces.

### 1. A state object: the world-facts the harness cares about

Hold the shared mutable state the tools write and the guardrails/oracle read. Keep it small — only the facts that decide guardrails and the goal.

```go
type Session struct {
    mu              sync.Mutex
    loggedIn        bool   // read by the auth guardrail
    verifiedUpvoted bool   // read by the oracle
}
func (s *Session) LoggedIn(context.Context) bool { /* guarded read */ }
func (s *Session) VerifiedUpvoted() bool         { /* oracle predicate */ }
```

Tools set these only after confirming the corresponding state against the world, so everyone downstream trusts reality, not the model.

### 2. Tools: dumb actuators that verify their own effect

Implement `tools.Tool` (`Definition() tools.ToolSpec` + `Execute(ctx, tools.ToolCall) (*tools.ToolResult, error)`). Act, then verify, then record into the state object. Return a success result only when the effect is observable.

```go
func (t *upvote) Execute(ctx context.Context, _ tools.ToolCall) (*tools.ToolResult, error) {
    if err := t.page.Click(ctx, sel); err != nil {
        return &tools.ToolResult{Success: false, Error: "click: " + err.Error()}, nil
    }
    if !t.verifiedAgainstPage(ctx) {
        return &tools.ToolResult{Success: false, Error: "did not register"}, nil
    }
    t.session.markDone()
    return &tools.ToolResult{Success: true, Data: "done"}, nil
}
```

Put I/O (browser, HTTP, filesystem) behind a narrow port interface so tools stay testable with a fake.

### 3. Guardrails: preconditions the model cannot bypass

A guardrail runs before a tool executes. `RequireAuth` is built in; the general shape is a `PreCall` returning an error when a precondition fails. Wrap the registry:

```go
reg := tools.NewRegistry()
reg.Register(&upvote{...})
reg.Register(&login{...})

source := guardrails.NewGuardedSource(reg,
    guardrails.NewRequireAuth(session.LoggedIn,
        "you are not logged in — call login first", ToolUpvote))
```

A blocked call returns a validation error in the tool result. The model reads it as feedback and corrects course — the guardrail is both a safety rail and a hint.

### 4. The oracle: a predicate over real state

`pursue.Goal` evaluates a finished `pursue.Attempt` and returns a `pursue.Decision`. For predicate-backed world state, use `pursue.Until` / `pursue.UntilFunc`.

```go
goal, watcher := pursue.Until(session.VerifiedUpvoted,
    "not done yet; if a login screen appeared, log in then retry")
```

Verify the world, not `TaskResult` content. The result's terminal reason tells you why the *run* stopped, not whether the *goal* is met — those are different questions, which is the entire point.

### 5. Wire the runner and run

```go
client := runner.ClientFromProvider(provider) // any `zkit/ai/llm` provider

r := runner.New(client,
    runner.WithTools(source),
    runner.WithPromptText(systemPrompt),
    runner.WithToolTimeout(45*time.Second),
)

out, err := pursue.Drive(ctx, pursue.NewRequest(r.Run,
    runner.TaskSpec{ID: "task", Prompt: goalPrompt},
    pursue.WithGoal(goal), pursue.WithWatcher(watcher),
), pursue.WithMaxAttempts(4))
// out.Status is Succeeded | GaveUp | Errored
```

`r.Run` satisfies `pursue.AttemptFunc` directly. When the oracle returns not-done and budget remains, the harness re-drives: the prior conversation (`res.Messages`) becomes the next attempt's `Context` and the feedback becomes its `Prompt`, so the model sees its own history plus the correction.

### Optional: stop the instant the goal is met

Use `pursue.Until` / `pursue.UntilFunc` to cancel the in-flight attempt the moment a
world-state predicate becomes true:

```go
goal, watcher := pursue.Until(session.VerifiedUpvoted, "not done yet; retry")
out, err := pursue.Drive(ctx, pursue.NewRequest(r.Run, spec,
    pursue.WithGoal(goal), pursue.WithWatcher(watcher)),
    pursue.WithMaxAttempts(4))
```

The harness polls the predicate (default every 100 ms; for a custom cadence
build the watcher with `pursue.PollWatcher`) and cancels the attempt context the instant
it reports true. The runner returns `TerminalCancelled`, and the harness
reports `Succeeded` without waiting for the model to stop on its own. The
oracle still gets the final say on success.

### Degenerate case: headless is one attempt

If you don't need verification — run once and trust the model stopping on its own — that's `pursue.AcceptCompleted()` with a single attempt:

```go
out, err := pursue.Drive(ctx, pursue.NewRequest(r.Run, spec), pursue.WithMaxAttempts(1))
```

The same harness spans "fire and forget" and "verify against the world and keep trying" by varying the oracle and the budget.

## Testing

`hnupvote_test.go` runs the full stack with no browser and no LLM: a fake `Page` and a scripted `runnertest.Client` that plays the model (upvote → login → upvote → settle). The guardrail blocks the first upvote, the login tool flips the session, the second upvote verifies, and the oracle confirms success against the fake world.

```sh
go test ./examples/hnupvote/
```

Because correctness lives in the harness contract — guardrails, oracle, re-drive — and not in the model, it is fully testable deterministically.

## Files

| file | responsibility |
|------|----------------|
| `main.go` | flags, `.env`, confirm gate, wiring, final status line |
| `harness.go` | `RunUpvote`: tools + guardrail + runner + oracle + harness |
| `tools.go` | the `hn_upvote_top` / `hn_login` actuators and HN selectors |
| `session.go` | shared world-state read by guardrail and oracle |
| `port.go` | the `Page` interface the tools drive |
| `chromedp.go` | the only chromedp-importing file: `Page` over a real Chrome |
| `provider.go` | `buildClient`: select the LLM backend (codex vault or OpenAI-compatible) |
| `hnupvote_test.go` | deterministic end-to-end test, no browser, no LLM |
