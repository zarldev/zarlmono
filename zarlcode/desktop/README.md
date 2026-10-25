# Native workbench

Wails v3 + React/TypeScript frontend for the existing zarlcode agent. **Submitting a prompt calls the configured LLM and runs its tools in the current workspace.** There is no replay mode in the native application.

## Run

From the repository root:

```sh
go tool task workbench
```

Build once, then launch against another repository:

```sh
go tool task workbench-build
cd /path/to/project
/path/to/zarlmono/bin/zarlcode-workbench
```

Connect uses the same saved provider, model, vault and workspace settings as the TUI. Enter the vault passphrase if needed; leave it blank for configurations that do not need one. Select **Continue last session** to resume existing saved history. New provider credentials/OAuth setup still uses the existing CLI/TUI settings workflow.

Native prerequisites: repository Go toolchain, Node 22.12+ (or a supported newer LTS), and:

- **Fedora:** C compiler, GTK4 and WebKitGTK 6 development packages. Check with `pkg-config --modversion gtk4 webkitgtk-6.0`.
- **Apple Silicon macOS:** Xcode command-line tools and a native arm64 build. Execution on macOS remains unverified here. If the existing sandbox policy requires unconfined-shell consent, the connection UI requires explicit consent; it is never silently granted.

Tasks do not install OS packages. Native code/assets require `desktop`; normal TUI/headless builds do not need frontend assets or GTK headers. Wails Go and JS runtimes are pinned to `v3.0.0-beta.26`.

## Implemented

- Real prompt submission and follow-up turns; Cmd/Ctrl+Enter sends.
- Stop cancels the active operation; admission stays blocked until execution drains and session persistence settles.
- Plan/Build mode and provider/model switching using the existing backend.
- Canonical streamed conversation, tool input/output, structured plans, delegated-agent state, and recorded diffs.
- Existing before-turn checkpoints, completion barriers, session save/conflict handling, and compatible resume. Unsaved state blocks further submission and offers Retry save.
- Draft edits use the existing durable draft path; resume restores the composer and accepted submission clears it. The native bridge must receive an edit before it can be saved.
- Read-only inspector and virtualized, selectable transcript. Empty panes use static structural placeholders, not explanatory copy.
- Changed-snapshot polling with opaque revision tokens. Display is bounded to the newest 2,000 entries and roughly 1 MiB, with 32 KiB previews for large text fields; truncation does not alter stored history.

The temporary adapter in `tui/workbench.go` drives the existing controller through a nonrendering, signal-isolated Bubble Tea scheduler. It does **not** open a terminal, duplicate agent execution, or bypass persistence by calling `LiveRunner` directly. The eventual extraction can move this controller behind a neutral package without changing the frontend contract.

Closing while active requests Stop and keeps the window available until it is saved; close again afterward. Unsaved state refuses normal quit so it is not hidden behind a vanished window. The shutdown path joins the controller before closing its provider, sink, process manager and storage.

## Remaining replacement coverage

This is a functional live frontend, not yet full TUI feature parity. Native credential/OAuth onboarding, rich model selection, the complete settings UI, session browser/rename/branch/rewind, queued steering, attachments, reasoning/usage panels and sudo askpass UI are not wired. Large history/output pagination remains to be added beyond the bounded current view. The TUI stays available for those workflows while the replacement progresses.

The process starts in its working directory; there is no native workspace picker yet. No paid real-provider smoke prompt is sent by the tests.

## Verification and generation

```sh
(cd zarlcode/desktop/frontend && npx playwright install chromium)
go tool task workbench-test

# After changing native service/DTOs; requires desktop prerequisites:
go tool task workbench-bindings
```

Go integration tests use controlled providers with the actual engine/controller/store to exercise turns, tool calls, cancellation, save and resume. Browser tests inject an explicit test-only service. The normal browser preview is disconnected and never substitutes fixture results for a native error:

```sh
go tool task workbench-preview
```

The generated fixture JSON is used only by tests. Wails-generated TypeScript is checked in; regenerate it with the pinned command instead of editing it. Test HTML and fixture data are excluded from the production build. Chromium tests do not replace native WebKitGTK/WKWebView verification.

## Boundary

The bound service exposes Connect and typed session commands, not direct vault, filesystem, database or shell APIs. Credentials stay backend-owned; the supplied unlock passphrase is cleared from UI state immediately and from connection options after startup, and is never returned or logged. JavaScript string memory cannot be reliably zeroized.

Assets are bundled locally. CSP blocks external resources, inline executable scripts, frames and form submission. Model/repository/tool content is rendered as escaped text, not executable HTML. Camera, microphone, location, notification and clipboard-read permissions are denied. Clipboard writing is user initiated. No native automation/debug server is enabled. The normal sandbox re-exec shim is installed before GUI startup.
