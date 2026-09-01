# Zarlcode UX Handover

## Purpose

This document hands over the ongoing `zarlcode/tui` UX refinement. The work has established a coherent visual grammar and applied it across the transcript, composer/status area, sidebar, context view, transcript reader, intro, and several full-screen utility surfaces.

The next agent should continue **pane by pane**, preserve the settled design language below, and avoid broad rewrites of the rendering architecture.

## Repository and guidance

- Workspace: `/home/bruno/src/zarlmono`
- Owning module: `zarlcode/`
- Read and follow `zarlcode/AGENTS.md` before changing this subtree.
- Focused verification:

  ```bash
  go test -C zarlcode -count=1 ./tui
  ```

- Build/install:

  ```bash
  go tool task zarlcode
  ```

- Installed binary: `~/.local/bin/zarlcode`

## Workspace caution

The worktree is heavily modified across `zarlcode` and `zkit`. These changes include work outside this UX pass. Do not discard, restore, or broadly rewrite existing changes.

At handover time, `git status --short` reports modified files throughout:

- `zarlcode/tui/` — this UX pass and other active work
- `zarlcode/catalog/`, `zarlcode/engine/`, and `zarlcode/README.md`
- `zkit/agent/`, `zkit/ai/tools/code/`, and `zkit/prefs/`

There are also untracked tests and sandbox files. Treat all of this as user work.

## Latest verification

The latest narrow check passed after the workspace-change notification:

```text
go test -C zarlcode -count=1 ./tui
ok github.com/zarldev/zarlmono/zarlcode/tui
```

The latest full rebuild also completed successfully:

```text
/home/bruno/.local/bin/zarlcode
zarlcode/v0.14.0-dirty+20260827-000627
```

A later UX build before the final workspace-change check also succeeded; always generate a fresh version with `go tool task zarlcode` after further edits.

## Settled design language

Treat these as product decisions unless the user explicitly revises them:

1. **Open primary surfaces** rather than unnecessary full boxes.
2. **Thin rules only for genuine boundaries** or changes of scope.
3. **Left-anchored content**, including on wide terminals.
4. **Explicit owner anchors and rails** for transcript relationships.
5. **`[+]` / `[-]` disclosures are preferred.** Do not replace them with triangles.
6. **No redundant semantic prefixes** such as `TOOL tools (3)`; concise summaries are preferred.
7. **Chronological truth over artificial type sorting.** Merge adjacent events of the same type, but do not reorder activity by type.
8. **Minimal indentation.** Avoid stair-step nesting.
9. **Quiet durable state; emphasis for live, exceptional, or transient state.**
10. **No duplicate information** between utility header, composer, status row, and sidebar.
11. **Contextual footers**, not persistent shortcut wallpaper.
12. **Modal dialogs may remain framed.** They are real modal boundaries; full-screen utility surfaces should generally stay open.
13. **Use the existing semantic theme roles.** Do not hard-code colours or use colour as the only state signal.

## Transcript grammar

The current intended grammar is:

```text
[you]
│ User message text
└─

[zarl]
│ Assistant response text
├─
│ [+] thinking
│ [+] tools (2)
│ [+] edits (1 file)
```

Rules:

- Owner anchors occupy dedicated rows; conversation text never shares the anchor row.
- A completed user turn closes with `└─`.
- A queued user turn remains open and shows `◷ queued` beside `[you]`; it receives `└─` only after injection.
- Assistant live state such as `working…` may appear beside `[zarl]` because it describes what the owner is doing.
- `├─` appears only when supporting activity follows assistant prose.
- Thinking, tools, edits, plans, skills, and agents stay on the assistant rail.
- Activity remains chronological.
- No final assistant end-cap has been added yet; evaluate visually before adding one.

Relevant code:

- `zarlcode/tui/timeline.go`
- `zarlcode/tui/group.go`
- `zarlcode/tui/think_item.go`
- `zarlcode/tui/plan_item.go`
- `zarlcode/tui/subagent_item.go`
- `zarlcode/tui/skills_item.go`
- `zarlcode/tui/diff_item.go`

Representative coverage:

- `timeline_render_internal_test.go`
- `timeline_selection_internal_test.go`
- `timeline_internal_test.go`
- `transcript_toggle_internal_test.go`

## Transcript shell and utility header

The transcript is open on the left, right, and bottom. It has a two-row top boundary:

```text
ƶ · running state · token rate                 model · follow/browse/visual
──────────────────────────────────────────────────────────────────────────
```

Decisions:

- Compact `ƶ` orientation mark, not the full repeated product title.
- Live execution state and token rate belong beside `ƶ`.
- Stable model and viewport state belong on the right.
- Workflow mode does not belong in this header.
- Content starts at the pane edge; there is no extra pane-level left gutter.
- Wide transcript content remains left-anchored and caps at 110 columns.
- Sidebar scrollbar owns the final column and appears only when useful.

Relevant code:

- `drawTimeline`, `drawTimelineTopBar`, `transcriptHeaderSegments`, and `transcriptContentGeometry` in `timeline.go`
- matching hit-testing in `mouse.go`

## Browse mode and composer coexistence

The user explicitly requested typing while scrolled up. Current behavior:

- Browse mode freezes the transcript viewport, not the composer.
- Printable input goes to the composer while browsing.
- `Enter`, `Backspace`, `Left`, and `Right` edit or submit a non-empty draft.
- Arrow/Page/Home/End keys remain dedicated transcript navigation.
- Vim-style printable browse mnemonics no longer steal typed characters.
- `Esc` exits browse mode.

Relevant code and coverage:

- `handleBrowseKey` in `composer.go`
- `TestComposer_RemainsEditableWhileTranscriptIsBrowsed` in `composer_internal_test.go`

Be careful when changing this routing: transcript visual selection has its own key handling and must retain copy/navigation controls.

## Composer and state row

The composer has returned to a compact `›` prompt. Build/plan mode now gives the bottom status row a durable purpose:

```text
build mode
plan mode
build mode  ·  running
plan mode   ·  running
```

Rules:

- Plan mode still changes composer border/accent colour.
- Slash-command suggestions temporarily replace the normal mode hint.
- Toasts render on the right side of the same row.
- Do not reintroduce persistent shortcut matrices into this row.
- Full shortcut discovery belongs in help.

Relevant code:

- `composer.draw` and `handleBrowseKey` in `composer.go`
- `statusPane.statusHint` / `statusToast` in `pane_status.go`

## Compaction telemetry

Compaction telemetry no longer belongs inline in conversation content. Automatic and manual compaction now use the transient status toast:

```text
↯ compacted 776→776 msgs · 250.3KB reclaimed · tiered
```

Relevant code:

- `handle_runner.go`
- `compact_now.go`
- `compactionNotice` in `cockpit.go`
- `compact_status_internal_test.go`

The event/run telemetry remains available elsewhere; do not put operational telemetry back into assistant prose.

## State sidebar

- Toggle shortcut: `ctrl+b`.
- Visibility is shell-local through `Session.StateSidebarHidden`.
- Hiding it returns width to the transcript.
- The visible purpose is `context`, not a duplicate running-state title.
- It uses one vertical separator rather than a complete outer box.
- Preserve useful plan, context, run, cost, tools, provider, model, window, and session information.

Relevant code:

- `sidebar.go`
- `layout.go`
- `model.go`
- `session.go`
- `composer.go` (`ctrl+b`)

## Context dashboard

The full-width context view now uses:

- active tab row as the surface identity,
- one divider below tabs,
- open content body,
- no redundant `context view` frame label,
- no duplicated persistent tab-help/footer wallpaper.

Preserve overview, prompt, tools, events, and preview data.

Relevant code: `dashboard.go`, `cockpit_render.go`, and inspector/context-view tests.

## Transcript reader

The full-screen reader follows the transcript shell:

- open utility header,
- divider beneath it,
- same owner anchors/rails,
- viewport state retained,
- footer only for active search or visual selection.

Relevant code: `transcript_reader.go`.

## Full-screen utility surfaces

The following now share `drawUtilitySplitPane`:

- file/workspace viewer,
- inspector,
- tool history,
- execution tray.

Shared structure:

```text
utility title  tabs/status  ·  summary
──────────────────────────────────────
nav pane             │ detail pane
                     │
contextual footer
```

Rules:

- no outer box,
- one header divider,
- one nav/detail separator,
- no redundant close text in the header,
- actions remain in contextual footers.

Relevant code:

- `drawUtilitySplitPane` in `draw.go`
- `file_viewer.go`
- `inspector.go`
- `tool_history.go`
- `steer_tray.go`
- `utility_surface_internal_test.go`

`working_set.go` still uses the older framed/split treatment and is an obvious next candidate for alignment.

## Intro and startup

The intro footer is focus-specific rather than a broad shortcut matrix:

- prompt focus: start, sessions, help,
- session focus: navigate, resume, prompt.

Startup failure retains the functional `ctrl+s settings` recovery action plus quit controls. Do not remove a recovery action merely to reduce chrome.

Relevant code: `intro.go`, `startup_failure.go`.

## Theme system

The existing theme system already provides semantic roles for:

- background/foreground,
- primary/secondary,
- user/assistant,
- tool,
- success/error/warning,
- muted/subtle,
- border/highlight/info,
- plan mode.

`semanticThemeComplete` and `styles_internal_test.go` now assert that every built-in theme supplies all required roles.

Keep using semantic roles from `zkit/tui/theme`; do not introduce raw colours in pane code.

## Recommended next work

Continue in small, reviewable pane families:

1. **Working set**
   - Move from the older boxed split shell to `drawUtilitySplitPane`.
   - Preserve loading/error/empty states and existing action semantics.

2. **Remaining full-screen utilities**
   - Catalogue any surfaces still calling `drawSplitPane` directly.
   - Align headers, summaries, empty states, and contextual footers.

3. **Modal dialog standardization**
   - Keep borders.
   - Standardize title → content → local feedback → action footer.
   - Remove duplicate shortcut wording, but preserve functional recovery actions.

4. **Settings discussion and pass**
   - This was intentionally left as a separate design discussion.
   - Review category navigation, inline editing, scope comprehension, save feedback, provider/model discovery states, and theme preview.
   - Follow the persistence invariants in `zarlcode/AGENTS.md` exactly.

5. **Responsive audit**
   - Narrow terminals: owner rails, activity disclosure, utility split fallbacks, dialog minimum sizes.
   - Wide terminals: transcript must remain left-anchored.
   - Header truncation priority should favor live state and viewport state before decorative identity.

6. **Final semantic/accessibility audit**
   - Verify all built-in themes visually.
   - Ensure state is not communicated through colour alone.
   - Check selection, errors, disabled controls, and active tabs.

## Verification strategy

For TUI-only changes, use the narrow package check:

```bash
go test -C zarlcode -count=1 ./tui
```

Before reporting completion:

```bash
git diff --check -- zarlcode/tui
go tool task zarlcode
```

If changing settings/persistence, engine, catalog, or shared zkit behavior, widen verification according to `zarlcode/AGENTS.md` and the owning module guidance.

## Final handover note

The strongest user preference throughout this pass has been **clear relationship without excess chrome**. The user responds well to concrete visual grammar and incremental live builds, and strongly dislikes redundant labels, centered transcript content, excessive left padding, persistent shortcut wallpaper, and replacing `[+]`/`[-]` with unfamiliar disclosure symbols.


## Agentic tools and guardrails handover (2026-08-27)

A second workstream in this same worktree hardened sub-agent configuration, lifecycle tools, runner identity, guardrail composition, and verify-mode shell isolation. Preserve it alongside the UX work above.

### Completed agentic work

- Sub-agents are an explicit opt-in capability; disabling them removes the whole spawn lifecycle tool family from the model surface.
- Unnamed tasks have typed per-mode policy for explore, verify, and implement:
  - named-agent/profile defaults,
  - provider/model defaults,
  - iteration limits,
  - strict/planner/parent fallback policy.
- Settings expose profile pickers, per-mode limits, concurrency, child runtime, await defaults/ceilings, and fallback policy.
- `Group` now owns cancellation, one retryable join path, panic containment, concurrency admission, bounded observed-result retention, and maximum child runtime.
- Terminal outcomes distinguish cancellation (`Transient`), iteration/runtime exhaustion (`Budget`), and execution faults (`Fatal`).
- Lifecycle tools validate nil configuration, task lookup errors, await timeouts, and negative spawn iteration input.
- `list_agent_tasks` is metadata-only and non-consuming; status/await/stop deliver terminal summaries.
- Strict fallback avoids planner/resolver side effects where the registered inventory proves a route invalid.
- Runner rejects concurrent ownership of the same explicit task ID and permits reuse after the owning run ends.
- Post-call guardrails all observe the original result; the first rejection still wins.
- Plan-first treats opaque shell/interpreter execution as potentially mutating.
- Linux verify-mode shell execution uses a read-only workspace Landlock policy, including managed background commands; static shell policy remains the non-Linux/unavailable fallback.

Primary files:

- `zkit/agent/tools/spawn/`
- `zkit/agent/runner/{runner.go,run.go,errors.go,task_id_ownership_test.go}`
- `zkit/agent/guardrails/`
- `zkit/agent/sandbox/workmode*.go`
- `zkit/ai/tools/code/{sandboxer.go,bash.go,processmgr.go}`
- `zarlcode/engine/{settings.go,live.go,live_spawn.go}`
- `zarlcode/tui/{settings_dialog.go,launch.go}`
- `zkit/prefs/keys.go`

### Latest agentic verification

The latest workspace-change check passed:

```bash
go test -C zkit -count=1 ./agent/tools/spawn
go test -C zkit -race -count=1 ./agent/tools/spawn
git diff --check
```

Earlier focused checks also passed for:

```bash
go test -C zkit -count=1 ./agent/runner ./agent/guardrails ./agent/sandbox ./ai/tools ./ai/tools/code ./prefs
go test -C zarlcode -count=1 ./engine
go test -C zarlcode -count=1 ./tui -run 'Settings|Spawn|SubAgent'
```

The broad `zkit/agent/sandbox` run has an environment-sensitive existing smoke failure on this machine because local Git signing configuration references `~/.ssh/github.pub`, which the default sandbox denies. Focused verify-mode sandbox normal/race tests passed.

### Next-agent checklist, in priority order

1. **Review the aggregate agentic diff before adding more behavior.**
   - This is now a large cross-package change assembled incrementally.
   - Check public API naming, docs, error wording, duplicate helpers, and whether any implementation can be simplified without weakening lifecycle ownership.
   - Do not mix this review with the unrelated TUI visual rewrite.

2. **Add settings UI contract coverage for every new sub-agent row.**
   - Assert enablement defaults off.
   - Assert agent-profile picker selection and `(parent/planner)` clearing.
   - Assert workspace/global scope promotion.
   - Assert numeric values and fallback enum persist and resolve correctly.

3. **Add end-to-end engine registration tests.**
   - Disabled: no spawn lifecycle tools are registered.
   - Enabled: all five tools are registered.
   - Per-mode defaults select the expected runner/provider/model.
   - Strict fallback and mode iteration settings reach the runtime tool.

4. **Finish verify-mode sandbox integration coverage.**
   - Add a managed-background verify command test, not only foreground execution.
   - Verify workspace write denial and allowed temp/cache writes.
   - Test behavior/reporting when Landlock is unavailable or sandboxing is disabled.
   - Do not claim kernel isolation on unsupported platforms.

5. **Audit configuration bounds.**
   - Settings currently parse several integers but should have explicit reasonable upper bounds where resource abuse is possible: max concurrency, child runtime, per-mode iterations, spawn depth, and fan-out.
   - Invalid persisted values should resolve predictably and surface useful settings feedback.

6. **Audit completion evidence after retention eviction.**
   - Current policy never evicts unobserved terminal tasks and bounds observed history.
   - Add a stress/property-style test combining completion, status/await observation, pruning, list ordering, and concurrent polling.

7. **Review strict-routing inventory freshness.**
   - Strict rejection uses the registered planner candidate inventory to avoid resolver side effects.
   - Confirm catalog reload/profile deletion cannot leave stale candidates that produce misleading `registered but could not load` behavior.

8. **Consider typed lifecycle payloads only if an external consumer needs them.**
   - Current `map[string]any` payloads are stable in package tests and serialize correctly.
   - Do not introduce exported payload structs merely for aesthetics; first identify a real consumer or contract failure.

9. **Run broader verification once concurrent UX work settles.**
   - At minimum:

   ```bash
   go test -C zkit -count=1 ./agent/runner ./agent/tools/spawn ./agent/guardrails ./agent/sandbox ./ai/tools ./ai/tools/code ./prefs
   go test -C zkit -race -count=1 ./agent/runner ./agent/tools/spawn ./agent/guardrails
   go test -C zarlcode -count=1 ./engine
   go test -C zarlcode -count=1 ./tui
   git diff --check
   ```

   - Then use `go tool task check` if the worktree is ready for a repository-wide signal.
   - Separate failures caused by the active UX work or machine-local sandbox/Git configuration from agentic regressions; do not silently ignore them.

10. **Update user documentation after behavior is final.**
    - Ensure `zarlcode/README.md`, spawn README, settings labels, defaults, and fallback descriptions agree.
    - In particular, document that sub-agents default off and that verify-mode kernel isolation is platform/availability dependent.

### Agentic invariants to preserve

- Every child goroutine has one Group owner, cancellation path, and join path.
- A timed-out `agent_await` never cancels the child.
- Every terminal summary remains outstanding until an explicit consuming lifecycle call observes it.
- Workspace conflicts are recoverable and non-blocking; malformed/internal coordinator failures are not mislabeled as budget pressure.
- Explicit agent arguments beat defaults; a requested mode cannot escalate a stricter profile mode.
- Provider/model behavior belongs in profiles or typed per-mode targets, not duplicated ad hoc settings.
- Verify/explore cannot obtain workspace write capability through tool selection or opaque shell indirection when enforceable sandboxing is available.
- Never discard the extensive concurrent TUI and agentic changes in this worktree.

---

# Handover Update — Workspace Coordination and Core QoL Program

## Current request and product direction

The user asked for core-product fixes, not metrics work. The active product program is tracked in:

- `.zarlcode/plans/core-qol-roadmap.md`
- `.zarlcode/plans/workspace-wait-observability.md`

Work completed in this uncommitted worktree includes path-aware multi-agent workspace coordination, wait observability, session naming/search/pinning/metadata, first-prompt titles, a command palette, clipboard improvements, Markdown export, completion sounds, and draft recovery.

The next agent should **stabilize and review the current large worktree before starting another roadmap feature**. Do not assume every modified file belongs to this program; preserve all existing user work.

## Non-negotiable test-style rule

The user explicitly corrected the testing style during this session:

> All tests must be black-box tests in external `*_test` packages.

This is now recorded in root `AGENTS.md`. Do not add package-internal tests and do not add coverage to `*_internal_test.go`. If behavior is not testable externally, expose the smallest behavior-specific public seam or move pure logic into a small domain package.

During this session, newly added white-box TUI/engine tests were removed. Current new tests created by this work are external packages, including:

- `zarlcode/draft/draft_test.go` — `package draft_test`
- `zkit/db/session_draft_test.go` — `package db_test`
- `zkit/db/session_pin_test.go` — `package db_test`
- `zkit/db/session_rename_test.go` — `package db_test`
- `zkit/agent/coderunner/workspace_coordination_integration_test.go` — `package coderunner_test`

Before adding tests, inspect the package declaration explicitly.

## Workspace coordination work completed

### Typed tool scopes

`tools.ToolSpec` now carries typed workspace-scope metadata. Built-in tools declare one of:

- argument-derived paths;
- fixed paths;
- Codex patch paths;
- conservative workspace-root fallback.

The strategy identity uses a generated concrete enum rather than free-form strings. Relevant files include:

- `zkit/ai/tools/tools.go`
- `zkit/ai/tools/workspace_scope_enum.go`
- generated `zkit/ai/tools/workspacescopekinds_enums.go`
- workspace-aware tools under `zkit/ai/tools/code/`

### Per-call coordination only

Legacy coarse child/task-lifetime leases were removed. The production model is per tool call:

- disjoint paths may run concurrently;
- ancestor/descendant and equal paths conflict;
- compatible readers overlap;
- opaque effects such as shell operations conservatively use the workspace root;
- unsafe or missing inferred paths fall back to root.

`WorkspaceCoordinator` supports fail-fast acquisition and cancellable fair waiting. Overlapping waiters are FIFO relative to conflicting requests; disjoint requests bypass them.

Core files:

- `zkit/ai/tools/workspace_coordinator.go`
- `zkit/agent/coderunner/coordinated_source.go`
- `zarlcode/engine/live.go`
- `zarlcode/engine/live_spawn.go`

### Wait observability

Workspace waits emit a typed lifecycle through context observers and runner events. The TUI shows waiting state on the existing tool row, including nested program tool calls.

Core files:

- `zkit/ai/tools/workspace_wait.go`
- `zkit/ai/tools/workspace_wait_outcome_enum.go`
- generated `workspacewaitoutcomes` enum file
- `zkit/agent/runner/workspace_wait_publish.go`
- runner sink/event files
- `zarlcode/tui/teasink/messages.go`
- `zarlcode/tui/teasink/teasink.go`
- `zarlcode/tui/timeline.go`

A full async-spawn integration test verifies that overlapping child tool calls wait and publish lifecycle events.

## Session and intro QoL completed

### Session naming

- `Ctrl+N` names the active session.
- `/name <label>` sets it directly.
- The intro screen supports rename-in-place on the selected saved session.
- Saved labels are shown correctly with timestamp/unnamed fallback only at display time.
- The prior bug where close/save replaced user names with timestamps was fixed by keeping timestamp fallback out of `Session.Label`.
- Intro deletion now requires confirmation; only Enter/Y confirms and all other keys cancel.

### Search, pinning, and richer metadata

The intro screen now supports:

- `/` search mode over label, agent name, model, and ID;
- stable selection by session ID under filtering;
- `p` pin toggle;
- pinned-first ordering by pin time, then normal recent ordering;
- richer cheap metadata: agent, model, messages, plan progress, changed-file count, and Draft marker;
- confirmed delete and rename operating on the filtered selection.

DB migrations added during this program:

- `zkit/db/migrations/00020_session_pins.sql`
- `zkit/db/migrations/00021_session_intro_metadata.sql`
- `zkit/db/migrations/00022_session_label_provenance.sql`

Queries were regenerated with SQLC from `zkit/db/queries/sessions.sql`; do not edit generated files directly.

### First-prompt titles

Session labels now have explicit provenance:

- generated from the first real prompt only;
- slash commands do not become titles;
- generated title is one line and bounded to 80 runes;
- manual rename before or after generation always wins;
- manual clearing remains explicit/manual and does not regenerate;
- provenance survives save, close, and resume.

## Command palette and copy/export work

### Command palette

`Ctrl+K` opens a searchable command palette backed by a generated concrete command ID enum. Initial commands include help, settings, theme, model selection, naming, plan, tool history, files, copy-last-response, and export.

Files:

- `zarlcode/tui/command_id_enum.go`
- generated `zarlcode/tui/commandids_enums.go`
- `zarlcode/tui/command_palette.go`

### Clipboard

A shared clipboard result/toast path was added in `zarlcode/tui/clipboard.go`.

- `Ctrl+Shift+C` copies the latest non-empty assistant response.
- The same action is available in the command palette.
- Transcript reader’s existing selection/message copy remains.

### Markdown export

Active sessions can be exported via:

- command palette `Export session`;
- `/export [path]`.

Default output is collision-safe under:

```text
.zarlcode/exports/<safe-label>-<short-id>.md
```

The serializer is deterministic and preserves Markdown/code content. Implementation is in `zarlcode/tui/session_export.go`.

## Notification sounds

A persisted `notification sounds` setting was added under Interface with:

- `off`
- `completion` (default)
- `all`

It uses terminal BEL through Bubble Tea rather than platform audio APIs. Top-level successful completion rings once; `all` additionally rings once when plan steps newly become completed.

Files:

- `zkit/prefs/keys.go`
- `zarlcode/engine/settings.go`
- `zarlcode/tui/settings_dialog.go`
- `zarlcode/tui/notification_sound.go`

## Draft recovery completed

Draft recovery was the most recent feature.

### Domain and storage

A public bounded codec lives in:

- `zarlcode/draft/draft.go`
- external test `zarlcode/draft/draft_test.go`

Text is limited to 256 KiB. Empty text serializes to the legacy `[]` pending representation.

Dedicated DB operations were added:

- `Store.SaveSessionDraft`
- `Store.ClearSessionDraft`

They write only `pending_json`; on existing sessions they preserve history, labels, and activity timestamps. External DB coverage is in `zkit/db/session_draft_test.go`.

### TUI behavior

`zarlcode/tui/draft_persist.go` owns draft debounce and persistence:

- Bubble Tea-owned debounce, no free-running goroutine;
- stale generations are ignored;
- composer text changes/paste schedule a save;
- session identity is created before draft save;
- resume decodes pending JSON and restores composer text;
- real accepted prompt submission clears persisted draft state;
- save errors appear as toasts;
- intro summary query exposes a cheap `HasDraft` marker.

Confirmed intro deletion is currently the explicit discard path for saved draft-only sessions.

## Verification state

The latest full verification command completed successfully:

```bash
go test -C zkit -race -count=1 ./db
go test -C zarlcode -race -count=1 ./draft ./tui/...
go tool task check
go tool github.com/golangci/golangci-lint/v2/cmd/golangci-lint run ./zarlcode/... ./zkit/db/...
go tool task zarlcode
```

Earlier `go tool task race` was also rerun successfully after a transient failure report.

A previous repository-wide lint run was once blocked by unrelated `zkit/mcp/client_test.go` err113 issues while other workspace work was in progress. The latest affected-module lint command above passes. Re-run `go tool task lint` before final delivery and distinguish unrelated failures carefully.

The rebuilt binary has been installed by `go tool task zarlcode` multiple times after feature milestones.

## Current worktree warning

The workspace is very dirty and contains a large mixture of modified and untracked files. `git status --short` currently reports well over one hundred paths. Important points:

- Do not reset, restore, or discard changes.
- Do not assume all changes were made by this session.
- Review diffs by subsystem before editing.
- There are currently no running sub-agent tasks.
- All SQLC/generated enum source files should be treated according to repo guidance.

Useful first commands for the next agent:

```bash
git status --short
git diff --stat
git diff --check
go test -C zkit -count=1 ./db
go test -C zarlcode -count=1 ./draft ./tui/...
```

## Remaining QoL roadmap

From `.zarlcode/plans/core-qol-roadmap.md`, the main uncompleted product items are:

1. Better sub-agent status and controls.
2. Focus/attention-aware completion notifications.
3. Additional context-aware copy actions beyond copy-last-response.
4. Export of persisted/inactive sessions from the intro screen, plus richer optional sections.
5. Draft attachment recovery and a dedicated recover/discard prompt if desired.
6. Registry convergence so palette/help/slash metadata share more definitions.

The roadmap originally listed draft recovery before sub-agent controls; draft recovery is now implemented. The recommended next feature is **sub-agent status and controls**, but first review and stabilize the current worktree.

## Recommended next-agent sequence

1. Read root and nested `AGENTS.md` files for the subsystem.
2. Run the focused smoke tests above.
3. Audit `git diff --check` and generated-file consistency.
4. Review draft recovery and session migration diffs for accidental duplication.
5. Confirm all newly added tests are external packages:

   ```bash
   git status --short | awk '$2 ~ /_test\.go$/ {print $2}' | while read f; do head -n 1 "$f"; done
   ```

6. Re-run `go tool task check` and `go tool task lint` before starting new feature work.
7. If clean enough, start sub-agent controls using an event-driven TUI task view; keep `spawn.Group` as lifecycle owner and avoid polling goroutines.

## Known design constraints for next work

- Do not add metrics work; user asked for core product fixes.
- Do not reintroduce task-lifetime workspace leases.
- Do not add model-authored workspace path scopes.
- Do not use strings where a concrete generated domain type is appropriate.
- Do not add white-box/internal tests.
- Every goroutine/process must have an owner, stop condition, and wait path.
- Preserve user-named sessions; generated titles/timestamps must never overwrite manual names.
- Keep intro list queries blob-free.
