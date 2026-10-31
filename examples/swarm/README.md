# Three-agent swarm experiment

By default, a credential-free protocol experiment using the real
`zkit/agent/swarm`, runner input admission, and board tools with scripted model
responses. Explicit `-live` runs a bounded DeepSeek/Sol/Luna repair trial instead.
Neither mode exposes network, shell, or filesystem tools to the agents.

From the monorepo root, with the local `go.work` active:

```sh
go run ./examples/swarm
go test -C examples -race -count=30 -timeout=120s ./swarm
```

The default command prints a JSON evidence report and exits nonzero if the
experiment fails. Without `-live`, it never calls a live provider or needs credentials.

## Scenario

1. Researcher, implementer, and reviewer finish their initial assignments and go
   idle. The host then publishes a task, exercising actual idle reactivation.
2. The researcher lists the board, explicitly reads the task body, and posts a
   finding about empty-input handling in an average function.
3. The implementer reads that finding and posts a **proposal**, not a file edit.
4. The reviewer reads the proposal and posts a scripted review, not an independent
   correctness verdict.
5. The host waits for quiescence, checks member health, closes and joins the swarm,
   and evaluates the retained author/body/reply chain. Quiescence alone is not
   success.

Each script reacts to model-visible tool results, not direct access to the board.
A role publishes only once. Author identity comes from the member capability;
model arguments cannot choose an author. Peer notices remain typed low-trust
observations in each member's retained conversation.

## Reading the report

A successful run reports:

- `complete: true`, `initially_idle: true`, and `shutdown_joined: true`;
- three handoffs: host → researcher → implementer → reviewer;
- four retained findings with sequential reply links and no duplicate contributions;
- one read and one post call per member, one original assignment, and three admitted
  observations per member;
- reactivations, empty pending inboxes, and all members stopped after shutdown.

Board publication is broadcast, not directed delivery. Peers may wake for findings
that need no action. Activation and recorded assistant-response counts can vary
with scheduling; **the evidence chain is deterministic**, not the exact event
interleaving. Response counts come from retained history and do not count failed
or unrecorded provider attempts. Duplicate contributions count repeated posts for
the same role/topic, not every potentially redundant computation.

Scripted clients report no token usage, so the report explicitly says usage is
unavailable rather than presenting zero as a measured live cost.

## Bounds and tests

The default policy allows three members/workers, 24 total activations, 24 runner
iterations per activation, ten seconds of goal lifetime, and eight posts / 16 KiB
of board text. Reported-token admission is disabled because there is no provider
usage. All started work is joined on success, cancellation, or budget failure.

External-package tests exercise single-worker and concurrent execution, stable
findings across independent goals, continuation/admission, pre-cancelled callers,
cancellation after workers start, activation/runtime-budget exhaustion, rejected
posts at board capacity, and membership failure before startup. A forced ordering
test delivers a prerequisite after a peer's list but before that activation seals,
then verifies the peer refreshes and reads it within the same activation.

`RunExperiment` accepts a borrowed `runner.EventSink` for concurrent live telemetry;
the CLI supplies `runner.NopSink{}`. Tests use cancellation-aware event barriers and
`testing/synctest`, not sleeps or credentials. A custom sink must honor cancellation
when blocking and remain alive until `RunExperiment` returns.

## Opt-in live trial

```sh
# Check stored credential availability; this does not verify model access.
go run ./examples/swarm -live-preflight
# Makes real model requests and consumes provider quota.
go run ./examples/swarm -live
```

Routes are fixed: DeepSeek `deepseek-v4-flash` researches, Codex `gpt-6.1-sol`
implements, and Codex `gpt-6-luna` reviews. Existing settings-backed credentials
are used noninteractively; unavailable or locked credentials fail without model
substitution. The shared task is an isolated in-memory canonical IPv4 regex.
Only the implementer can submit a replacement, and only the reviewer can record
approval for the current edited version. Go RE2 compilation and a fixed 1,264-case
comparison against `net/netip` provide the independent oracle; no generated
program or shell command is executed.

The live limits are three minutes, 12 activations, 36 model requests, a 100,000
reported-token admission threshold, 45 seconds per request, and 64 KiB of streamed
output per request. A 2,048 output-token limit is requested where supported.
Already-admitted requests can overshoot the token threshold; this is not a hard
billing cap. Usage reports can be partial when a provider omits usage, including
when cancellation interrupts a request. No dollar cost is fabricated.

### Verified-success shutdown

After each successful board publication or version-bound approval, the host checks
for the full task → research → implementation → review chain, approval of the
current candidate, and a passing independent oracle. This check shares a lock
with local tool execution and model-request admission. On success, further tool
mutations and request admissions are rejected, the goal is canceled with a
specific verified-success cause, and **all active work is joined** before return.
It does not wait for models to acknowledge the final review or consume every
broadcast notification.

A clean result has `verified_success: true`, `complete: true`, and
`shutdown_joined: true`. Expected cancellation appears in member `last_error`
with `cancelled_for_success: true`; pending notification counts remain truthful
and may be nonzero. Genuine provider/budget failures remain failures, even when
joined with cancellation. `verified_success` can therefore be true while
`complete` is false if a real concurrent failure is discovered during shutdown.
Reviewer approval alone, stale approval, broken reply chains, failed tests, or
ordinary caller cancellation cannot trigger successful completion.

Offline tests exercise the same live harness with fixture providers, including
rejection of post-review requests, cancellation/join of an in-flight peer,
preservation of real failures, artifact freezing, invalid proof, and the 100K
admission boundary. Ordinary tests never load live credentials.

## What this does not prove

The default scripted scenario tests collaboration mechanics, not model reasoning.
The live regex trial tests a small programming task, not general coding quality;
passing its fixed cases is not an exhaustive proof over all possible inputs.
Neither mode provides restart durability, TUI integration, or repository file
edits. The example uses the shared swarm directly, not the engine adapter or its
workspace coordinator.
