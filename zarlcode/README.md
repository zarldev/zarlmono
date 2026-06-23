# zarlcode

```

                              ________  ________  ________  _____     ________  ________   _______  _______
                             ╱        ╲╱        ╲╱        ╲╱     ╲   ╱        ╲╱        ╲_╱       ╲╱       ╲
                            ╱-        ╱    ╱    ╱     ╱   ╱      ╱  ╱      ___╱     ╱   ╱     ╱   ╱       _╱
                           ╱        _╱         ╱        _╱      ╱__╱         ╱         ╱         ╱       _╱
                           ╲________╱╲___╱____╱╲____╱___╱╲________╱╲________╱╲________╱╲________╱╲_______╱
 

Terminal-native coding agent. Stream an LLM into your workspace — it reads, writes, runs commands, searches the web, and spawns sub-agents while you steer from a keyboard-driven TUI.

Built on [`zkit`](../zkit/). Plan first, execute second, rewind anytime.

## Concepts


zarlcode has two modes, toggled with `Shift+Tab`:

| Mode | What it does |
|------|-------------|
| **Plan** | Read-only investigation. The agent can read files, search, and think — but cannot write, edit, or run commands. Use it to explore a codebase, design a change, or sanity-check an idea before anything gets mutated. |
| **Build** | Full tool surface. Read, write, edit, patch, bash, grep, web search, MCP tools, sub-agent dispatch. Guardrails still apply (shell policy, fanout caps, schema validation). |

Every session persists to `~/.zarlcode/state.db`. Pick up where you left off with `zarlcode -continue`.

## Quick start

```bash
# Build and install to ~/.local/bin/zarlcode
go tool task zarlcode

# First-time setup
zarlcode init
zarlcode keys set <provider>   # anthropic, openai, gemini, deepseek, etc.

# Launch
zarlcode                       # Interactive TUI
zarlcode -continue             # Resume last session
```

Supported providers: `anthropic`, `openai`, `deepseek`, `gemini`, `google-vertex`, `llamacpp`, `ollama`, plus OAuth-backed `claude-code` and `openai-codex`. Run `zarlcode keys --help` for provider-specific setup.

## Capabilities

**File tools.** Read, write, and edit files, plus `grep` and `glob` for search. All file ops are workspace-bounded and tracked.

**Shell.** `bash` with foreground (blocking, 600s max) and background (detached process) modes. Long output is spooled to disk and tail-summarised. Guardrail policies block destructive patterns.

**Web.** `web_search` via a local SearXNG instance, `web_fetch` with headless Chrome fallback for JS-heavy pages.

### Browser-backed `web_fetch`

`web_fetch` tries a plain HTTP GET first. It launches Chrome/Chromium through
chromedp only when you pass `use_browser: true` or when the HTTP response looks
like an empty JavaScript app shell.

Browser mode needs a Chrome/Chromium executable that the zarlcode process can
start. If auto-detection cannot find the right browser, open settings with
`Ctrl+S`, edit **integrations → chrome path**, and set an absolute path such as:

```text
/snap/bin/chromium
/usr/bin/chromium
/usr/bin/google-chrome
```

Leave the row blank to use auto-detection. Settings edited in the pane are saved
for the current workspace; workspace values override global values. To make the
current value the global default, focus the row and press `Ctrl+G`. Use the
storage inspector to check workspace/global/effective values when a stale global
path keeps reappearing.

WSL note: zarlcode runs as a Linux process under WSL. Prefer a Linux
Chrome/Chromium installed inside WSL (Snap Chromium works when snapd is usable;
Google Chrome's `.deb` is another reliable option). A Windows path such as
`/mnt/c/Program Files/Google/Chrome/Application/chrome.exe` may exist but fail to
start under chromedp because Chrome is launched with Linux profile/cache paths.

Common fixes for browser fallback warnings:

- `no Chrome/Chromium browser binary found`: install Chromium/Chrome or set
  **chrome path** explicitly.
- `chrome binary not found at configured path`: update or clear the configured
  path.
- `permission denied` / snap metadata errors: try a non-snap Chrome/Chromium or
  adjust the local snap/WSL environment.
- Browser warnings while HTTP content is still returned: the fast HTTP path
  succeeded, but the optional browser fallback failed; fix Chrome setup only if
  you need rendered JavaScript content.

**MCP servers.** Connect to Model Context Protocol servers (`mcp_connect` / `mcp_disconnect`) — their tools register on the flat tool list and are callable like any other registered tool. stdio (subprocess) and HTTP transports.

**Sub-agents.** `spawn_agent` dispatches focused sub-tasks to fresh agents — run a researcher, reviewer, and coder in parallel without polluting your main context. Mode enforcement (`explore` / `verify` / `implement`) keeps sub-agents within bounds.

**Dynamic tools.** Optional dynamic-tool support can author Go tools from within a session via `new_tool`, compile them, and register them immediately. It is not part of the default TUI tool surface unless explicitly enabled.

**Compaction.** Long sessions auto-compact: older messages are summarised, context stays lean, the agent keeps working. Structural, summary, and executive compaction strategies.

**Skills.** Hot-reloadable capability guides from workspace, home, and source-tree directories. Load a skill mid-session when the task matches.

## TUI

Keyboard-driven, mouse-aware. The timeline shows streaming responses, tool calls (expandable with `Enter`), diffs, and thinking blocks. A cockpit pane tracks tokens, compaction events, and active tools in real time.

### Keybindings

#### Compose (default mode)

| Key | Action |
|-----|--------|
| `Enter` | Submit prompt |
| `Shift+Enter` | Insert newline |
| `Shift+Tab` | Toggle Plan ↔ Build mode |
| `Tab` | Enter transcript browse mode |
| `Esc` | Stop running turn |
| `Ctrl+C` | Quit |
| `Ctrl+Q` | Clear context |
| `Ctrl+L` | Expand context dashboard |
| `PgUp` / `PgDn` | Page transcript |

#### Global overlays (work from any mode)

| Key | Action |
|-----|--------|
| `Ctrl+F` | File viewer — browse the workspace tree |
| `Ctrl+E` | Model quick picker — switch provider/model |
| `Ctrl+S` | Settings — edit persisted prefs |
| `Ctrl+T` | Theme picker |
| `Ctrl+P` | Plan pane — structured step list with status tracking |
| `Ctrl+G` | Help — full key reference |
| `Ctrl+W` | Working set pane — files touched this session |
| `Ctrl+Y` | Execution tray — steer a live run |
| `Ctrl+I` | Inspector — drill into tool calls and results |

#### Browse mode (`Tab` to enter)

| Key | Action |
|-----|--------|
| `↑` `↓` / `j` `k` | Move cursor |
| `Enter` / `Space` | Expand/collapse item |
| `g` / `Home` | Jump to top |
| `End` | Jump to bottom |
| `Esc` / `i` | Return to compose |

#### Mouse

| Gesture | Action |
|---------|--------|
| Scroll wheel | Line-scroll transcript |
| Click `[+]` / `[-]` | Expand/collapse groups, thinking blocks, diffs |
| Click right gutter | Jump timeline position |

## Headless mode

Run a task to completion without the TUI — useful for CI, scripts, and eval harnesses:

```bash
zarlcode --headless --prompt-file task.md          # Read prompt from file
zarlcode --headless --prompt-text "fix the build"  # Inline prompt
zarlcode --headless --max-iter 20 --prompt-file task.md  # Override iteration cap
```

Exit codes: 0 = completed, 1 = max iterations / cancelled, 2 = error, 4 = bad invocation. Summary goes to stderr; final answer to stdout. Headless runs are recorded in `state.db` alongside interactive sessions.

## CLI subcommands

| Command | What it does |
|---------|-------------|
| `zarlcode init` | Materialise `~/.zarlcode/` (prompt.md, skills, tools, config skeleton) |
| `zarlcode keys` | Manage credentials: `list`, `set`, `delete`, `oauth`, `protect status/on/off` |
| `zarlcode serve` | Exec `llama-server` with zarlcode's canonical local-model defaults |
| `zarlcode upgrade` | Self-upgrade — download and replace the zarlcode binary |
| `zarlcode --askpass` | Internal: sudo `SUDO_ASKPASS` shim used when `sudo_askpass` is enabled |

Interactive flags: `-env`, `-agent`, `-continue`, `-version`. Headless flags: `--headless`, `--prompt-file`, `--prompt-text`, `--max-iter`.

## Module structure

```
zarlcode/
├── cmd/           # ~10-line entry point — calls zarlcode.Main()
├── tui/           # Bubble Tea UI: timeline, cockpit, dialogs, composer, theming
├── engine/        # TUI-to-runner bridge: LiveRunner, headless mode, settings
├── catalog/       # Agent and skill catalogue (load, scaffold, validate)
├── cli/           # Operational subcommands: init, keys, serve, upgrade, --askpass
├── hooks/         # Workspace lifecycle hooks (OnToolResult, OnCompaction)
├── instructions/  # Workspace instruction loading (AGENTS.md, CLAUDE.md, etc.)
├── prompts/       # System prompt templates (system.md, plan.md, init.md)
├── home/          # Materialises ~/.zarlcode/ on first run
└── version/       # Build-time version stamp (Version, Commit, Date)
```

## Build and test

From the repository root:

```bash
go tool task zarlcode              # Build and install to ~/.local/bin/zarlcode
go test -C zarlcode ./...          # Run tests
go test -C zarlcode -race ./...    # With race detector
go run ./zarlcode/cmd              # Run from source
```

## Documentation

- [`AGENTS.md`](AGENTS.md) — Implementation notes: TUI, settings/prefs, storage
- [`zkit/README.md`](../zkit/README.md) — Shared substrate (agent runner, LLM providers, tools, MCP)

## Trust boundaries

zarlcode executes shell commands, mutates files, and spawns subprocesses based on model output. Guardrails and shell policies are applied via `zkit/agent/guardrails`, but it runs with your user's privileges. Review tool calls in the timeline before continuing when uncertain.

## License

MIT — see [LICENSE](../LICENSE).

