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
- `Group` now owns cancellation, one retryable join path, panic containment, workspace leases, concurrency admission, bounded observed-result retention, and maximum child runtime.
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
