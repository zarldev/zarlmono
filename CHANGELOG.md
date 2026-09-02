# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [zkit/v0.18.0] — 2026-09-02

### Added

- Added canonical session transcript persistence with ordered typed entries, monotonic revisions, checksums, atomic SQLite reads and writes, strict integrity validation, interrupted-lifecycle recovery, and cascade deletion.
- Added repository-managed SQLC `v1.31.1`; regenerate committed database code with `cd zkit/db && go tool sqlc generate` after query or migration changes.

### Changed

- Separated renderer-independent transcript history from compactable provider context, and propagated task/turn ownership through plan and diff events.
- Tightened context, plan, and Tea sink ownership: compactor and event handoffs are cloned, and the sink now owns and joins its pump goroutine on shutdown.
- Normalized session storage from `history_json` to `context_json`; session queries explicitly omit the retired `tool_trace_json` column so normalized and mixed historical schemas can be read safely.

### Fixed

- Fixed session resume after migration when the local database no longer contains `tool_trace_json`.
- Fixed transcript payload and revision validation so malformed or contradictory updates fail atomically.

## [zarlcode/v0.18.0] — 2026-09-02

### Added

- Added a canonical, renderer-independent transcript for user/assistant text, reasoning, tools, diffs, plans, skills, compaction notices, and nested sub-agent lifecycle events.
- Added durable composer drafts, transcript-backed Markdown export, richer saved-session search/rename/pin/delete controls, command-palette and clipboard actions, terminal notifications, and best-effort sleep inhibition.

### Changed

- Resume verifies transcript revision, checksum, ordering, parent links, and lifecycle states, recovers interrupted entries, then rebuilds the TUI from canonical records. Provider context remains separately compactable.
- Transcript persistence uses immediate semantic-boundary writes, debounced streaming updates, and save/delete barriers.

### Fixed

- Fixed resumed sessions losing tool activity and other rich timeline state by restoring directly from canonical transcript entries.
- Fixed session creation followed by resume against normalized databases that do not expose `tool_trace_json`.

### Removed

- Removed the legacy history/tool-trace reconstruction path. Sessions with missing, empty, or corrupt canonical transcripts are rejected rather than resumed with partial reconstructed history.

### Migration notes

- Opening an existing state database applies migrations `00025` and `00026`. Back up `~/.zarlcode/state.db` before first launch if binary rollback may be required; older binaries cannot open the newer schema without restoring that backup.

## [swebench-eval/v0.4.0] — 2026-09-02

### Added

- Added verified re-drive controls with optional prior-thread carryover, official verifier feedback, and persisted `verified`, `attempts`, and ordered `attempt_verdicts` telemetry.
- Added `sweeval -version` with release ldflags, module metadata, and VCS fallbacks.

### Changed

- Reports distinguish final grader resolution from in-run verified success and label evaluator errors, resolved/unresolved scoring, verified attempts, and unverified attempts deterministically.
- Historical rows with zero/empty verification fields mean telemetry was not recorded; they do not represent a newly verified failure.

### Fixed

- Fixed result loading so persisted verification telemetry round-trips through `ListResultsForRun`.

### Migration notes

- Migration `00004` adds verification telemetry with backward-compatible defaults. Downgrading removes those columns and their recorded data.

## [examples/v0.5.0] — 2026-09-02

### Added

- Added deterministic runnable examples for application-owned HITL/workflow checkpoint-resume, local MCP discovery/calls/notifications, dynamic tool lifecycle, skill discovery, notification drain, Linux Landlock sandboxing, and JSONL runner/workflow tracing.
- Added a feature-to-example coverage matrix linking every advertised major capability to runnable code and deeper documentation.

### Changed

- Corrected example commands and clarified which examples need providers, browsers, external services, or Linux Landlock.

## [zkit/v0.17.0] — 2026-09-01

### Changed

- Published the provider, runner, tool, workspace-coordination, preferences, cache, MCP, and lifecycle contracts required by the zarlcode conformance release.
- Migrated zkit-owned tests to external black-box packages and tightened cancellation, redaction, deterministic concurrency, and process ownership coverage.

## [zarlcode/v0.17.0] — 2026-09-01

### Changed

- Pins the standalone zarlcode module to zkit/v0.17.0 so published builds include the conformance, lifecycle, persistence, provider, and black-box testing changes.
- Supersedes the unpublished zarlcode/v0.16.0 tag, whose standalone build correctly rejected the stale zkit/v0.16.0 dependency.

## [zarlcode/v0.16.0] — 2026-09-01

### Changed

- Completed a repository-wide design and style conformance pass across all Go modules.
- Migrated owned tests to external black-box packages and enforced the policy in CI.
- Unified session and draft persistence ordering, surfaced preference persistence errors, and restored grouped tool-call rendering when resuming conversations.
- Tightened context propagation, goroutine/process ownership, queue cancellation, cache health checks, and evaluation driver validation.
- Expanded release, provider wire-contract, MCP cancellation/redaction, and deterministic concurrency coverage.

### Fixed

- Prevented stale draft, clear, and full-session writes from racing during session transitions and shutdown.
- Fixed restored conversations rendering each tool call as a separate assistant message instead of one tool collection.
- Removed unmanaged background shell execution and joined evaluation/example server goroutines.

## [Unreleased]

### Added

- Added a searchable `Ctrl+K` command palette, session naming and Markdown export, one-key copying of the latest assistant response, and transcript visual selection with clipboard yanking.
- Added durable composer drafts plus richer saved-session management with generated or manual names, search, pinning, rename, delete confirmation, and at-a-glance session metadata.
- Added configurable completion and plan-progress terminal bells, and best-effort operating-system sleep inhibition while interactive or headless turns are active.

### Changed

- Hardened module releases with shared semantic-version/changelog validation, isolated dependency checks, atomic annotated tags, deterministic archives/checksums, and exact Homebrew formula verification.
- Aligned the root workspace, published modules, documentation, and release gate on Go 1.27.0 so the declared minimum and exact-toolchain validation cannot drift.

### Fixed

- Prevented no-op publish runs, duplicate or ambiguous publisher dispatches, workspace-contaminated release binaries, and archives with an invalid `dist/zarlcode` layout.


## [zarlcode/v0.15.0] — 2026-08-28
`zarlcode/v0.15.0`

### Added

- Added opt-in sub-agent delegation settings, bounded iteration/depth/fan-out/concurrency/runtime controls, asynchronous spawn lifecycle tools, agent activity, and task ownership tracking.
- Added plan-aware delegation, responsive working-set/process views, local service controls, sudo askpass handling, and persistent provider/model discovery states.

### Changed

- Reworked the terminal UI around open full-screen utility surfaces, standardized context/body/footer modal regions, scrollable Help and Plan panes, clearer settings scope/editing/theme states, and compact narrow-terminal navigation.
- Unified transcript, composer, dashboard, sidebar, status, picker, provider, file, diff, inspector, history, and plan visual grammar with explicit semantic state labels.

### Fixed

- Recovered sub-agent panics into terminal task results, hardened shutdown ownership, bounded metadata/model lookups, enforced workspace sandbox modes, and made numeric resource validation actionable.
- Preserved selection, active context, and close/back actions across utility surfaces and pickers at constrained terminal sizes.

## [zkit/v0.16.0] — 2026-08-28
`zkit/v0.16.0`

### Added

- Added explicit spawn enablement, lifecycle ownership, asynchronous start/list/await/cancel support, per-profile iteration limits, fan-out/concurrency/runtime budgets, and panic-safe child execution.
- Added sandbox work modes, task-ID propagation/ownership checks, coordinated application cleanup, and model metadata timeout controls.

### Changed

- Delegation now composes guardrails, profiles, recursion, planner routing, task scopes, and runtime budgets through one owned task group with deterministic shutdown.

### Fixed

- Prevented leaked child tasks and goroutines during cancellation, shutdown, panic, and terminal races; strengthened await/list snapshots and ownership validation.
- Made the Linux default-policy smoke test independent of user Git signing configuration.

## [zarlcode/v0.14.0] — 2026-08-26
`zarlcode/v0.14.0`

### Added

- Added a configurable `spawn_await_timeout` setting for bounded sub-agent waits.

### Fixed

- Named agents now honor the host-configured spawn iteration budget instead of silently stopping at a lower profile value.
- Agent lifecycle tools recover an omitted task receipt when the target is unambiguous and bounded waits return a running snapshot without cancelling work.
- The synthetic `program` boundary now traverses production guardrails while nested calls remain guarded exactly once.

## [zkit/v0.15.0] — 2026-08-26
`zkit/v0.15.0`

### Changed

- Decompose guardrails can exclude managed control-plane wrappers from retry and delegation accounting.

### Fixed

- Agent await distinguishes polling timeouts from parent cancellation and deadlines, resolves completion races, and reports explicit cancellation successfully.
- Program validates Starlark before execution and gives corrective guidance for invalid escape sequences.
- Agent lifecycle task selection and typed failure handling now remain consistent across await, status, stop, and list operations.

## [zarlcode/v0.13.0] — 2026-08-24
`zarlcode/v0.13.0`

### Fixed

- Model selection remains responsive by rendering models.dev context, capability, and pricing metadata exclusively from the warmed in-memory snapshot, with static metadata as the cold-start fallback.

## [zkit/v0.14.0] — 2026-08-24
`zkit/v0.14.0`

### Added

- Added non-blocking, in-memory-only models.dev lookups and cached provider metadata resolvers for latency-sensitive render paths.

## [zkit/v0.13.0] — 2026-08-24
`zkit/v0.13.0`

### Added

- Added variadic initial values to `zsync.NewSet` and `zsync.NewQueue`.
- Added independent snapshots for `zsync.Map` and `zsync.Queue`.
- Added atomic batch updates with `Set.AddAll`, `Set.RemoveAll`, and `Queue.PushMany`.

## [zarlcode/v0.12.0] — 2026-08-23
`zarlcode/v0.12.0`

### Changed

- Standardized agent configuration on canonical functional options.
- Configured spawn iteration budgets now take precedence over model-supplied values.

## [zkit/v0.12.0] — 2026-08-23
`zkit/v0.12.0`

### Changed

- Centralized `zapp` default initialization and removed redundant defensive checks.
- Standardized agent functional options on `options.Option[T]`.
- Configured spawn iteration budgets now take precedence over model-supplied values.

## [zarlcode/v0.11.0] — 2026-08-21
`zarlcode/v0.11.0`

### Added

- Added a full-screen transcript reader with user-turn navigation, search, stable line selection, range copy, and whole-message copy.
- Typing `@` in the composer now opens fuzzy workspace-file selection and attaches the selected text file to the next prompt.
- Added a dedicated agent activity screen showing delegated-agent status, task details, output, and elapsed time.
- The cockpit now reports separate live elapsed times for the current turn and current iteration.

### Changed

- Directory listings and previews in the file viewer now run asynchronously, cancel obsolete work, and discard stale results.
- Web-search provider changes apply to the live runner when settings close, without restarting Zarlcode.
- Interactive startup now renders before MCP and provider probes complete while holding the first prompt until the runtime is ready.
- Codex OAuth selections are validated against the account's live model catalogue; unavailable persisted models are replaced with a supported selection.

### Fixed

- Fixed a fatal recursive provider-error parser panic that could crash Zarlcode as soon as a conversation started and flood the terminal with stack output.
- Fixed conversation history disappearing or scrolling incorrectly after terminal resizes and display-scale changes.
- Fixed nested agent disclosure rendering and alignment of the collapsed `[+] agents` row with conversation content.
- Provider errors using top-level `detail` or `message` fields now render as concise user-facing errors.
- File previews reject symlinks escaping the workspace and bound image bytes and decoded pixel counts.

## [zkit/v0.11.1] — 2026-08-21
`zkit/v0.11.1`

### Changed

- models.dev metadata snapshots are retained in memory, stale snapshots remain available during refresh failures, and failed refreshes use retry backoff instead of blocking or repeatedly contacting the upstream service.
- Updated root tooling to golangci-lint v2.13.1 and adapted logging, message-bus, formatting, and provider code to its current analysis and dependency APIs.

### Fixed

- Codex response-stream terminal error handling consistently uses the provider error finish reason and preserves error events without duplicated literals.

## [zkit/v0.11.0] — 2026-08-19
`zkit/v0.11.0`

### Added

- `agent_spawn` now launches turn-owned sub-agents asynchronously and returns a task receipt immediately; `agent_status`, `agent_await`, `agent_stop`, and `list_agent_tasks` provide explicit inspection, joining, cancellation, and listing.
- Generated agent task states and workspace access levels make lifecycle and concurrency policy explicit.

### Changed

- Raised the minimum Go toolchain to 1.27.
- Root, named, and recursive agents share one turn-owned task group and workspace coordinator. Read-only children may overlap; implement children and conflicting parent writes are exclusive.
- Parent completion is held while child tasks are running or terminal summaries remain unread; turn shutdown cancels and joins all owned children.
- `zkit/docstore` redesigned around explicit `Record[Value]` identities: concrete `MemoryStore`/`MongoStore`, snapshot cloning at every boundary, and `ErrNotFound`/`ErrConflict`/`ErrInvalidRecord` replace the generic `Store[T]`, fluent `Query`, metadata side channel, and `ID()`/`SetID()` mutation ceremony.
- `zkit/ai/tools/dynamic.Catalog` is now one serialized owner: a single mutex covers store I/O and the in-memory snapshot, `RemoveContext(ctx, name) error` is idempotent, and the detached `Load`/`Add`/`Remove` background-context APIs are removed.
- `zkit/cache` returns `ctx.Err()` directly and no longer exposes `ErrCanceled`.
- `zkit/agent/checkpoint` exposes `checkpoint.ErrNotFound` with contextual wrapping across backends.

### Removed

- `zkit/filesystem` producer-wide interfaces and the SeaweedFS adapter; `OpenFile` returns `io.ReadWriteCloser` and capability interfaces live in consuming packages (`zlog.FileSystem`).
- `zkit/agent/trace` removed entirely (no production consumers; its exporter sink used detached `context.Background()`).
- `zkit/agent/sandbox.Policy.WithExecPath` and `zkit/agent/diffrecorder.Classifier.WithOverride` removed; callers use local launch-policy helpers.

## [zarlcode/v0.10.0] — 2026-08-19
`zarlcode/v0.10.0`

### Changed

- Raised the minimum Go toolchain to 1.27.
- Public catalogue/action names follow one grammar: `list_agents` / `agent_spawn`, `list_skills` / `skill_load` / `skill_create`, and `list_instructions` / `instruction_load`.
- `zarlcode/engine.NewSettings` is a pure composition seam (no context, I/O, logging, or goroutines); `OpenSettings` owns acquisition, migrations, registry reload, warm startup, and cleanup, with `Close` cancelling and joining the warm worker.
- Conversation execution uses one serialized transition, replacing the duplicated `runSpec`/`runSpecWithSetup` paths with coherent repaired-history and partial-terminal-history commits.
- Examples no-op cleanup returns removed; live provider assembly centralized in `examples/internal/exampleclient`.

## [zkit/v0.10.0] — 2026-08-18
`zkit/v0.10.0`

### Added

- Runner finalization warnings can fire before a context deadline, giving wall-clock-bounded coding tasks a last chance to commit useful work.
- Shell mutation analysis now identifies unscoped repository rewrites and additional interpreter execution tunnels used by unattended integrity guardrails.

### Changed

- Read-before-write distinguishes file creation from existing-file mutation across write, append, edit, and patch operations.
- Read-only shell commands and pipelines are allowed; mutation, redirect, opaque-interpreter, verify-mode, and test-fixture protections remain behavior-based.
- Plan-first permits behaviorally read-only shell inspection while continuing to gate workspace-changing commands.
- Tool mutation metadata and write-target extraction now classify process termination, patch paths, destinations, and safe output devices consistently.

## [zarlcode/v0.9.0] — 2026-08-18
`zarlcode/v0.9.0`

### Changed

- PLAN mode retains plan persistence and structured updates while exposing programmatic reads, computer observation, MCP/process inspection, and other read-only investigation tools.
- Programmatic read/search tools are enabled by default, with the direct read/search surface restored when explicitly disabled.
- Skill creation and process-control tools advertise mutation semantics so explore and verify sub-agents cannot change durable state through metadata gaps.

## [swebench-eval/v0.3.0] — 2026-08-18
`swebench-eval/v0.3.0`

### Added

- `--zarlcode-deadline-grace` forwards a wall-clock finalization warning window into the shared zarlcode runner configuration.

## [zkit/v0.9.1] — 2026-08-18
`zkit/v0.9.1`

### Added

- `coderunner.Tuning.IterationTimeout` allows consumers with slow-prefill local models to override the shared five-minute per-iteration wall-clock backstop while retaining the default when unset.

## [zkit/v0.9.0] — 2026-08-18
`zkit/v0.9.0`

### Added

- Code retrieval now labels and deterministically orders definition, test, and reference evidence so coding agents can assemble implementation context with fewer follow-up reads.
- Hashline edit failures now return bounded current-anchor windows for direct stale-edit recovery.
- Standard Go generated-file markers are detected: reads explain generator ownership, while edit and append tools refuse direct mutation before writing.
- Executive and handover compaction can preserve mechanical verification state and bounded unresolved tool failures in addition to plans, working files, and tool usage.

### Changed

- `grep` JSON output is now an object carrying query inputs, match count, truncation state, `max_results`, and `hits`, rather than a bare hit array.
- Read and retrieval truncation messages now identify the exact continuation controls (`offset`, `limit`, `max_files`, or `max_bytes_per_chunk`).
- Explicit invalid `agent_spawn.mode` values return a typed validation failure instead of silently falling back to the unrestricted implement mode.
- Tool-source wrappers forward task lifecycle cleanup so memo and guardrail state is released through composed source chains.

## [zarlcode/v0.8.0] — 2026-08-18
`zarlcode/v0.8.0`

### Added

- Reads and `retrieve_code` results now identify nearest applicable nested `AGENTS.md` / `CLAUDE.md` guidance without eagerly injecting instruction bodies into every request.
- Build-mode completion is evidence-aware: code changes without fresh verification receive one precise correction, failed checks are named, later edits stale earlier checks, and documentation-only changes remain unaffected.
- Executive and handover compaction now receive bounded session working files, tool counts, latest verification status, and unresolved failures from engine-owned operational state.
- Added architecture guidance documenting capability composition, inspection parity, reversible registration, and reconstructable model-visible state.

### Changed

- Incomplete structured-plan correction is prioritized before the one-shot verification correction at completion.
- Interactive, headless, programmatic, diff-recording, guidance, and mode-filter source layers now preserve task cleanup semantics.

## [swebench-eval/v0.2.0] — 2026-08-18
`swebench-eval/v0.2.0`

### Added

- Added `--zarlcode-stream-idle` to configure the maximum gap between provider stream chunks for slow-prefill local models.
- Added `--zarlcode-iteration-timeout` to configure the per-iteration wall-clock backstop independently of the stream-idle watchdog.

### Changed

- The in-process zarlcode driver passes configured stream-idle and iteration-timeout durations through shared `coderunner.Tuning`, keeping eval stall detection aligned with interactive zarlcode behavior.

## [zkit/v0.8.0] — 2026-07-31
`zkit/v0.8.0`

### Added

- `zkit/vault` now provides a versioned wrapped-DEK composition with bounded Argon2id headers, stable header/wrapped-key/envelope persistence encoding, explicit associated data, password rotation by DEK rewrapping, and close-to-wipe backend sessions.
- Wrapped-DEK tests cover wrong passwords, tampering, cross-record substitution, password rotation, closed sessions, malformed persistence, and unsupported versions.


## [zkit/v0.7.0] — 2026-07-30
`zkit/v0.7.0`

### Added

- zlog now supports independent JSON, plain-text, and console formats for file and stdout destinations through `WithFileFormat` and `WithStdoutFormat`.

### Changed

- zlog files default to structured JSON, while stdout defaults to terminal-aware console output. Console formatting never writes ANSI colour into files, and redirected stdout is uncoloured.
- `WithJSONOutput` remains available as a deprecated compatibility option, mapping to destination-specific formats.

## [zarlcode/v0.7.0] — 2026-07-30
`zarlcode/v0.7.0`

### Added

- zarlcode can create reusable Agent Skills through the new BUILD-mode `skill_create` tool. Skills use the portable `<name>/SKILL.md` package layout, become available in the live catalog immediately, and remain compatible with existing flat markdown skills.
- zarlcode transcript browse mode now supports visual line selection with `v`, keyboard range extension, and `y` clipboard copy. Copied text strips ANSI styling and timeline rails.

### Changed

- zarlcode materialises canonical user and workspace extension directories at launch so agents, skills, tools, and hooks have discoverable homes.
- zarlcode now depends on zkit v0.7.0.

## [zkit/v0.6.1] — 2026-07-29
`zkit/v0.6.1`

### Fixed

- Chrome-backed renderer integration tests are now explicit opt-ins via `ZKIT_CHROME_INTEGRATION=1`, so the generic full-module race job does not depend on launching Chromium under peak CI load.
- Renderer shutdown now waits for Chrome to exit before deleting its profile tree, preventing intermittent `directory not empty` cleanup failures.

## [zkit/v0.6.0] — 2026-07-25
`zkit/v0.6.0`

### Added

- Added `zsync.Set.AddIfAbsent`, an atomic insertion operation that reports which concurrent caller inserted a value.

### Changed

- `web_fetch` now reuses one tool-owned Chrome process across browser fallback requests, with isolated tabs, bounded concurrency, caller-aware cancellation, and deterministic lifecycle cleanup.

## [zarlcode/v0.6.3] — 2026-07-24
`zarlcode/v0.6.3`

### Added

- Shell policy gains an `off` mode: it drops the static shell guardrail from the chain entirely, so no bash command is parsed or blocked. A deliberate high-trust opt-in beyond `lenient`, which keeps the guardrail and only steps aside the ergonomic `cd`/redirect steers.

### Changed

- Picks up zkit v0.5.3 for the exported `NameShellPolicy` guardrail handle.

## [zkit/v0.5.3] — 2026-07-24
`zkit/v0.5.3`

### Added

- Exported the `NameShellPolicy` guardrail name constant, and `ShellGuardrail.Name()` now returns it, so consumers can drop the static shell guardrail via `Deps.Disabled` without a hardcoded string — matching `NameImprovementLoop`/`NameSkillHint`.

## [zarlcode/v0.6.2] — 2026-07-23
`zarlcode/v0.6.2`

### Changed

- Picks up zkit v0.5.2: the hashline `edit` tool no longer merges inserted text into a neighbouring line, and no longer shreds a range edit that shares its start with an insert.

## [zkit/v0.5.2] — 2026-07-23
`zkit/v0.5.2`

### Fixed

- The hashline `edit` tool keeps inserted text on lines of its own. An insert splices zero bytes, so — unlike a replace — it borrows no line terminator from what it displaces: an `insert_before`/`insert_after` whose `new_string` omitted the trailing newline fused onto the following line, and an `insert_after` on a final line with no newline fused onto the anchor line. The terminator now matches the file's own LF/CRLF, and a file with no final newline stays that way. This is the case v0.5.1 recorded as unaffected.
- A batch `edit` no longer corrupts a range edit that resolves to the same splice start as an insert (`insert_before` on the first line of a replaced range). Splices run from the tail, and the insert could go down first, shifting the bytes under the range's end offset so that splice landed mid-file — dropping the inserted text and resurrecting part of the replaced range. The wider range is now applied first. The fresh-anchor window returned by `edit` takes the same ordering, so it no longer reports shifted line numbers for that case.

## [zarlcode/v0.6.1] — 2026-07-22
`zarlcode/v0.6.1`

### Changed

- Picks up zkit v0.5.1: `grep`/`head` (and the grep family) run in bash again, the Anthropic static prompt prefix is cached with a 1-hour TTL, and hashline edits keep replaced lines newline-terminated.

## [zkit/v0.5.1] — 2026-07-22
`zkit/v0.5.1`

### Changed

- The shell policy no longer treats the grep family (`grep`/`egrep`/`fgrep`/`rg`/`ripgrep`/`ag`) or `head` as shell read-discovery helpers, so they run in bash — grep is routinely used to filter the output of real commands (`go test ./... | grep FAIL`), where blocking it was pure friction. `cat`/`sed`/`find`/`ls`/`tail` stay steered toward the workspace tools.
- The Anthropic static prompt prefix (tools + system) is cached with a 1-hour TTL so it survives the multi-minute idle gaps between turns, instead of being evicted at the 5-minute default and re-warmed at write pricing on resume. The rolling last-message breakpoint keeps the 5-minute default.

### Fixed

- The hashline `edit` tool keeps a replaced line newline-terminated when `new_string` omits the trailing newline, instead of silently un-terminating the line (merging it with the next) or dropping the file's final newline. Deletes and inserts are unaffected; files with no final newline are left alone.

## [zarlcode/v0.6.0] — 2026-07-22
`zarlcode/v0.6.0`

### Added

- Every guardrail is now configurable in Settings: test-edit (off/advisory/strict), improvement-loop, skill-hints, and shell-policy (auto/strict/lenient).
- Inline model picker in the providers settings panel — asynchronous model fetch, selection, and persist/switch without leaving the panel.
- Configurable response timeout: how long to wait with no output from the model before cancelling an iteration (default 90s; 0 falls back to the default).
- Manual compaction mode: turn off automatic compaction and get a cockpit warning when the context nears the compaction trigger, compacting on demand instead.
- Configurable per-task `agent_spawn` fan-out cap to bound a model that keeps firing sub-agents (default 8; 0 removes the cap).
- Handover compaction engine: clears the whole conversation and reseeds the model from a self-contained handover document written to `.zarlcode/handovers/`.
- Narrative VHS workflow demo and refreshed getting-started/docs.

### Changed

- Codex requests now carry a stable per-task `prompt_cache_key` so all iterations of a task share a cache route.

## [zkit/v0.5.0] — 2026-07-22
`zkit/v0.5.0`

### Added

- The hashline `edit` result returns fresh line/hash anchors around each edited region, so the model can chain further edits to a file without re-reading it.
- Anthropic provider caches the conversation prefix via a rolling ephemeral cache breakpoint on the last message, so large tool results are served from cache across turns rather than reprocessed each iteration.
- Codex Responses provider honours a `prompt_cache_key` request option for prefix-cache routing.
- Configurable stream-idle ("no response") timeout through `coderunner.Tuning.StreamIdle` (zero keeps the shared 90s default).
- Per-task `agent_spawn` fan-out cap in `StandardFanoutLimits` (default 8) so a task cannot fan out sub-agents unbounded.
- `compact` handover engine: collapses the whole conversation to a self-contained handover document and reseeds from it, with an injected writer for persistence.
- Exported guardrail name constants (`NameImprovementLoop`, `NameSkillHint`) so consumers can drop those guardrails via `Deps.Disabled` without hardcoded strings.

## [zarlcode/v0.5.1] — 2026-07-20
`zarlcode/v0.5.1`

### Fixed

- Browser-backed `computer_*` tool sessions now use the application context instead of the per-tool dispatch context, so a session created by `computer_observe` can be reused by later `computer_act` / `computer_observe` calls.

## [zarlcode/v0.5.0] — 2026-07-20
`zarlcode/v0.5.0`

### Added

- Immutable embedded prompt core with additive `~/.zarlcode/preferences.md` guidance and explicit `~/.zarlcode/prompt.override.md` full BUILD-mode overrides.
- Conservative migration handling for legacy `~/.zarlcode/prompt.md` files, including known shipped-seed detection.
- Prompt inspector metadata for active source, preferences source, resolution mode, and rendered prompt size.
- Golden rendered-prompt coverage plus resolver/materialisation/runtime-inspector regression tests.

### Changed

- `zarlcode init` no longer materialises the embedded system prompt into `~/.zarlcode/prompt.md` for new installs.
- Runtime prompt dispatch and inspector rendering now share the same resolved prompt path.
- Prompt rendering now gates tool-authoring and tool-registration guidance separately.
- Plan/build prompt text was tightened around durable preferences, plan persistence order, and tool capability claims.

### Fixed

- Existing users with untouched generated prompt copies now receive shipped prompt fixes again.
- Inspector output no longer diverges from the next provider request when prompt overrides/preferences exist.
- Stale prompt prose was corrected, including the plan-mode keybinding claim, a malformed `agent_spawn` sentence, and a concatenated markdown heading.

## [zkit/v0.4.0] — 2026-07-15
`zkit/v0.4.0`

### Added

- Provider-neutral `program` tool support for bounded, read-only Starlark fan-out over existing guarded tool sources.
- Programmatic read policy wiring for coderunner, including explicit allowlists for read/search/catalogue tools and bounded `call`, `call_many`, and `emit` execution.
- OpenAI Responses API request/stream handling, model plan metadata, endpoint kind, reasoning effort, and token-limit support.
- Session message-count storage migration and query plumbing.

### Changed

- Runner and guardrail event/context accounting now carries richer task/tool metadata for fan-out and context-breakdown reporting.
- Structured tool results normalize cleanly through programmatic execution while preserving typed result rendering for callers.
- OpenAI and Codex model metadata and request construction were refreshed for current endpoint capabilities.

### Fixed

- Hashline parsing and rendering edge cases are covered with broader tests.
- Dynamic MCP/computer tool registration now handles registry errors explicitly.

## [zarlcode/v0.4.0] — 2026-07-15
`zarlcode/v0.4.0`

### Added

- Opt-in `programmatic_tools` setting that exposes a portable `program` tool for read/search/catalogue fan-out while keeping edit/write/bash actions direct.
- Prompt guidance and tool-roster filtering for programmatic read/search workflows.
- Compact TUI rendering for program results, including call summaries and known structured-result summaries instead of raw wrapper JSON.
- Resume/session restoration UI and persistence improvements for continuing prior work.

### Changed

- Cockpit, model switching, runtime catalog, and session restore flows were tightened around live provider/model state.
- Conversation persistence now records additional session metadata for restore/resume views.

### Fixed

- Tool rendering no longer displays the entire program script or raw `{Output, Stats}` wrapper in the conversation trail.
- Settings, inspector, and launch flows handle updated tool/provider state more consistently.

## [zarlai/v0.4.0] — 2026-07-15

`zarlai/v0.4.0`

### Fixed

- Tool registration now propagates or logs zkit registry validation errors instead of ignoring them.
- Module dependency pin updated for the matching zkit release so standalone zarlai builds use the stricter registry API.

## [examples/v0.4.0] — 2026-07-15
`examples/v0.4.0`

### Changed

- Example harnesses now handle tool-registration errors explicitly for the stricter lint configuration.
- Example module dependency pin updated for the matching zkit release.

## [zkit/v0.3.1] — 2026-07-10

`zkit/v0.3.1`

### Added

- Typed workflow node IDs, tool-call IDs, and background process IDs to make shared agent/runtime boundaries harder to misuse.
- Validation helpers and constructors for tool effects, LLM response formats, and dynamic MCP connection specs.
- OpenAI/Codex model metadata updates, including GPT-5.6 family defaults and max reasoning effort support.

### Changed

- First-party tool schemas now preserve enum-backed argument types for plan and computer-use tools.
- OpenAI-compatible and Anthropic model discovery now follows paginated provider responses.

### Fixed

- Downstream zarlcode, swebench-eval, and examples call sites now use the stronger zkit ID and enum types.


## [zkit/v0.3.0] — 2026-07-08

`zkit/v0.3.0`

### Added

- Computer-use agent primitives, browser-backed observation/action flows, and computer action/observation tools for agent tool registries.
- Multimodal image media helpers and provider conversion support for image-capable model requests.
- Atomic batch support for anchored hashline edits so related same-file changes can be applied in one verified write.

### Changed

- Model metadata and capability plumbing now carries multimodal and computer-use hints through provider integrations.
- Read-before-write guardrails account for prior successful writes/edits when validating follow-up file edits.
- `golangci-lint` is pinned as a Go tool for reproducible lint checks.

### Fixed

- Browser computer backend cleanup and lint fixes across computer-use tooling.

## [zarlcode/v0.2.0] — 2026-07-08

`zarlcode/v0.2.0`

### Added

- Image attachment support in the TUI composer, transcript flow, and file viewer.
- Browser computer backend wiring for live agent runs.
- Multimodal prompt/context support for providers that accept images.

### Changed

- Editing guidance now prefers cohesive range or batch edits over many tiny adjacent edits.

### Fixed

- TUI layout and live-run plumbing fixes for multimodal/computer-use flows.

## [examples/v0.2.0] — 2026-07-08

`examples/v0.2.0`

### Added

- Computer-use Wikipedia quiz example with browser automation and LLM-generated questions.

### Changed

- Example lint/build flow now uses the repo-pinned lint tool.

## [v0.2.1] — 2026-06-29

`zkit/v0.2.1`

### Added

- Deterministic Go code-understanding helpers: syntax-aware `file_map`, lexical `retrieve_code`, and shared `sourcecode` parsing utilities for callers that need structured source context without embeddings or LSP.
- `models.dev` model metadata integration for context-window and pricing hints.
- Typed tool adapters (`NewTyped`, typed `DecodeArgs`, and effect derivation hooks) so tool implementations can keep typed business logic at the boundary.
- Typed retrieval metadata filters with equality predicates, plus vector-store/qdrant filter plumbing.

### Changed

- Tool argument decoding is now generic and shared across code, dynamic, fetch, search, and zarlcode catalog/instruction tools.
- LLM chat-template kwargs stay typed longer instead of round-tripping through unstructured maps.

### Fixed

- Claude Code inline tool-call leakage is guarded before it reaches the transcript.
- Read-before-write guardrails now treat a prior successful write/edit as established context for follow-up edits to the same file.
- Test and lint cleanup across the CI-covered modules.

## [v0.1.3] — 2026-06-29

`examples/v0.1.3`

### Changed

- Updated example tools to show the latest typed API patterns: `tools.SchemaFor`, generic `tools.DecodeArgs`, typed result structs, and `tools.NewTyped` for new tool adapters.
- Removed hand-written `map[string]any` JSON Schema trees from example tool definitions where the arguments are statically shaped.

## [v0.1.6] — 2026-06-29

`zarlcode/v0.1.6`

### Added

- Lazy instruction-loading tools so agents can discover workspace guidance without eagerly flooding context.
- TUI and headless wiring for the new code-understanding helpers and `models.dev` model metadata.
- Optional local service management for the bundled SearXNG `web_search` Docker Compose service from settings.
- Opt-in Go pprof/runtime metrics and execution tracing flags for profiling zarlcode runs.

### Changed

- Removed the `zarlcode serve` llama-server wrapper; zarlcode now configures model endpoints but leaves local model server lifecycle to Ollama, llama.cpp, LM Studio, or another OpenAI-compatible server.

### Fixed

- Release-dispatch now grants `actions: write`, allowing its follow-up `gh workflow run release.yml` publisher dispatch to succeed under `GITHUB_TOKEN`.

## [v0.2.0] — 2026-06-27

`zkit/v0.2.0`

### Added

- New agent subsystems: `workflow` (graph executor for multi-node flows), `retrieval` (chunking, embedding, and vector-store search), `hitl` (human-in-the-loop review and steering), `checkpoint` (run state store), and `trace` (JSONL event exporter).
- LLM provider rate-limit classification across anthropic, openai, openai-codex, google, and claude-code, surfacing reset/retry timing to the runner.

### Fixed

- Malformed tool-call JSON emitted as text (transposed or missing brackets) is now recovered by a balanced-bracket fast path in `toolparse`, and a runner guardrail re-prompts the model for anything unrecoverable — instead of the call leaking into the transcript as prose.

## [v0.1.5] — 2026-06-27

`zarlcode/v0.1.5`

### Changed

- Bumped the `zkit` dependency to `v0.2.0`, picking up the malformed tool-call recovery and provider rate-limit handling.

### Added

- TUI rate-limit display showing provider reset/retry state.

## [v0.1.0] — 2025-XX-XX

### Initial release

First public release of `zarlmono` — the Zarldev monorepo.

#### Modules

| Module | Tag | What it is |
|---|---|---|
| `zkit` | `zkit/v0.1.0` | Shared library: agent runner, LLM providers, tool system, guardrails, compaction, MCP, cache, filesystem, HTTP/RPC, logging, notifications, sync primitives |
| `zarlcode` | `zarlcode/v0.1.0` | Terminal coding agent / TUI — plan first, execute second, rewind anytime |
| `zarlai` | — | Smart-home/multimodal assistant (excluded from standard CI; CGO deps) |
| `swebench-eval` | `swebench-eval/v0.1.0` | SWE-bench evaluation driver |
| `examples` | — | Deterministic harness demos (not a consumer module) |

#### zarlcode

- **Plan/Build modes** — `Shift+Tab` toggles read-only Plan (investigation) and full Build (execute) modes
- **Session persistence** — sessions saved to `~/.zarlcode/state.db`; resume with `-continue`
- **Headless mode** — `--headless --prompt-file task.md` for CI, scripts, eval harnesses
- **Self-upgrade** — `zarlcode upgrade` downloads and replaces the binary
- **Release pipeline** — `task zarlcode:release VERSION=vX.Y.Z` tags and pushes
- **Settings system** — workspace/global scope, promote (Ctrl+G), inline save feedback, storage inspector
- **Provider support** — anthropic, openai, deepseek, gemini, google-vertex, llamacpp, ollama, claude-code (OAuth), openai-codex (OAuth)
- **File tools** — read, write, edit, grep, glob — workspace-bounded and tracked
- **Shell tools** — foreground (600s max) and background modes with guardrail policies
- **MCP servers** — stdio and HTTP transports; tools register on the flat tool list
- **Sub-agents** — parallel dispatch with mode enforcement (explore/verify/implement)
- **Compaction** — structural, summary, and executive strategies for long sessions
- **Skills** — hot-reloadable capability guides from workspace/home/source-tree directories
- **Theme system** — palette, JSON loader, live-preview gallery in settings

#### zkit (shared library)

- **Agent runner** — `think → call tools → observe → repeat` loop with streaming, compaction, truncation, steering
- **LLM providers** — OpenAI, Anthropic, Google Gemini, DeepSeek, llama.cpp, Ollama, Claude Code (OAuth), OpenAI Codex (OAuth)
- **Tool system** — typed handlers with reflected JSON Schema, registry, MCP bridge, code tools, fetch, search, dynamic registration
- **Guardrails** — pre/post tool-call validation, shell policy, fan-out caps, schema validation
- **Compaction** — structural trimming, LLM summaries, adaptive pressure handling
- **Stability tiers** — core/stable, shared/stable-ish, beta/evolving, experimental/volatile (documented in `zkit/README.md`)
- **Infrastructure** — cache, docstore, filesystem, messagebus, vectorstore, skills, notifications, sync primitives

#### swebench-eval

- Standalone SWE-bench evaluation driver that shares the same agent assembly as `zarlcode` via `zkit/agent/coderunner`

#### Bug fixes

- Fixed claudecode inline `<assistant_tool_calls>` emitted as text ([#1])
- Fixed recovery interceptor panic propagation
- Fixed golangci-lint exclusion rules across all modules
- Added gosec G101/G204 exclusions for specific files
- Fixed cache_prompt gating to llama.cpp backends only

#### Documentation

- Comprehensive AGENTS.md files in all major packages
- zkit README with package map, stability tiers, dependency policy
- Contributing guide with workflow, style, and gotchas
- Documentation site (Astro Starlight → GitHub Pages)

---

## [v0.1.4] — 2026-06-25

### Fixed

- `zarlcode upgrade` now ignores and clears legacy local source path configuration, falling back to GitHub release upgrades instead of requiring a source checkout.

## [v0.1.3] — 2026-06-24

### Added

- File viewer image previews for PNG, JPEG, and GIF files.
- Ghostty/Kitty terminal-graphics image rendering when supported, with ANSI block fallback elsewhere.

### Fixed

- Provider startup/runtime errors now surface as user-visible notices instead of silently failing.
- Release-dispatch follow-up verification and zarlcode packaging behavior.

## [v0.1.2] — 2025-06-21

### Added

- **go install support** — all `replace` directives stripped from submodule `go.mod` files; `go.work` handles local resolution, module proxy handles remote installs. `go install github.com/zarldev/zarlmono/zarlcode/cmd@v0.1.2` works.

### Changed

- **Dependency pinning** — all modules pin internal dependencies to published versions (`zkit v0.1.2`, `zarlcode v0.1.2`) instead of pseudo-versions with `replace` directives.
- **Release pipeline** — builds output to `dist/` to avoid directory conflicts; Windows dropped from cross-compile matrix (Unix syscall deps).

### Fixed

- CI: `go build ./...` in zarlcode excludes `./cmd` (main package output conflicts with `cmd/` directory)
- Release pipeline: YAML syntax errors resolved, all 4 platforms publish correctly
- Upgrade source: defaults to `zarldev/zarlmono` (was a local path)

## [v0.1.1] — 2025-06-21

### Fixed

- Release pipeline artifacts published to GitHub Releases for linux/{amd64,arm64} + darwin/{amd64,arm64}
- `zarlcode upgrade` works from GitHub Releases
- CI pipeline passes all 10 checks

### Changed

- Release matrix: 4 platforms (dropped windows/amd64 — Unix syscall dependencies)

## [v0.1.0] — 2025-06-18

### Added

- Initial public release of the Zarldev monorepo

[v0.1.4]: https://github.com/zarldev/zarlmono/releases/tag/zarlcode/v0.1.4
[v0.1.3]: https://github.com/zarldev/zarlmono/releases/tag/zarlcode/v0.1.3
[v0.1.2]: https://github.com/zarldev/zarlmono/releases/tag/zarlcode/v0.1.2
[v0.1.1]: https://github.com/zarldev/zarlmono/releases/tag/zarlcode/v0.1.1
[v0.1.0]: https://github.com/zarldev/zarlmono/releases/tag/zarlcode/v0.1.0
