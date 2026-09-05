# AGENTS.md — `zarlcode`

How zarlcode persists user preferences. The README documents *what's* here; this file documents *why* it's shaped this way.

## Build and verification

```bash
go test -C zarlcode -count=1 ./tui
go test -C zarlcode -count=1 ./...
go tool task zarlcode   # build + install ~/.local/bin/zarlcode with version ldflags
go run ./zarlcode/cmd
go run ./zarlcode/cmd -continue
```

The CI build excludes the CLI package in its package matrix (`go list ./... | grep -v '/cmd$' | xargs go build`); use `go tool task zarlcode` when asked to rebuild/install the application.
## One service, two tables, three scope words

Every persisted preference flows through the application facade in `zarlcode/prefs`. It owns zarlcode's stable key catalogue and model-selection transition while embedding the generic scoped service from `zkit/prefs`. The service fronts two underlying tables:

- `settings` — plaintext (workspace, key, value)
- `api_keys` — encrypted (workspace, provider, ciphertext, nonce)

Every operation takes an explicit `scope`:

- `prefs.ScopeWorkspace` — the row keyed to the current workspace root. The per-project pin. The in-TUI settings pane writes here.
- `prefs.ScopeGlobal` — the row keyed to `workspace=""`. The "set once, every workspace inherits via store fallback" path. `zarlcode keys set` and the intro wizard write here.
- `prefs.ScopeEffective` — read-only sentinel. Resolves workspace first, global second, returns whichever has a value. Writers reject it with `prefs.ErrInvalidScope`.

**Never use the empty string to mean "global" outside the service.** The store's schema uses `workspace=""` for globals, but passing the empty string as a sentinel silently writes to the wrong row. Callers ask for `prefs.ScopeGlobal` by name — it's a first-class type, not a magic value.

## Promote, not dual-write

Saves from the settings pane land in workspace scope. To make a value the default in every workspace, focus the row and press **Ctrl+G** — the promote path **MOVES** the workspace row to the global row.

Move, not copy: after a promote, a later workspace edit signals "per-workspace override" rather than silently diverging from the global default. Re-promote to republish.

## Where saves come from (and where they land)

| Entry point | Default scope |
|---|---|
| `zarlcode keys set` CLI | global |
| Intro wizard's first-time save | global |
| Settings pane edit (any row) | workspace |
| Settings pane edit + Ctrl+G | workspace → global (promote) |
| Model picker (provider/model swap) | workspace, atomically via `zarlcode/prefs.Service.SetModelSelection` |
| OAuth completion handler | global, via `prefs.Service.SetKey(prefs.ScopeGlobal, …)` |

The model picker doesn't write the `settings` table directly: `applyConfigChange` mutates the live provider/model config, and persistence writes the application-owned pair through `zarlcode/prefs.Service.SetModelSelection`. The facade translates that transition to generic `zkit/prefs.Service.ApplySettings` operations in one transaction.

The split between write-once fields (theme / provider / model / agent, from the quick pickers and the settings pane) and read-write fields (everything else) is load-bearing: `currentSettings()` only knows about the write-once fields, so widening `saveSettings` without widening `currentSettings` deletes the unmentioned settings on every persist.

## Settings dialog structure

The settings overlay (`settingsDialog` in `tui/settings_dialog.go`) is a master-detail view: a category nav column on the left, the selected category's rows on the right with inline edit. Each row carries its resolved value, scope, and whether it's set (so `(unset)` defaults render correctly). The dialog is stateful and persistent — it holds the `*engine.Settings` handle so side effects (prefs writes, live theme apply) happen inline rather than returning intents.

Rows are one of: text (free-text inline editor), enum (pick-one with cycling), action (opens a nested dialog), or model (per-provider model picker). The category list also includes special categories backed by sub-dialogs: the providers panel, the theme gallery (live-preview grid), read-only agents/skills/hooks inventory panels, and the MCP server list.

## Feedback affordances on the settings pane

The pane carries `lastSaved + lastAt` so every commit lights up:

- Inline **✓ saved (scope)** badge on the row for ~2s after a save. A `tea.Tick` schedules the fade.
- Bottom **status strip**: `last save: <label> → <value> · scope: <scope> · Ns ago`. Survives past the badge TTL.
- **Pre-save echo**: while the inline editor is open, the value column paints `→ <pending>` so the user sees what will commit. Vault keys mask to `••••`.
- Failures render `✗ failed` + `last save FAILED: <error>`, so a failed save can't slip past unnoticed.

Picker-routed rows (theme / provider / model / agent) close the pane after commit, so their saved feedback does not survive the reopen.

## Storage inspector

`/storage` opens a read-only inspector listing every known setting + provider key across workspace, global, effective, and source columns. Use it to answer "did my save land?" without dropping to sqlite. Outside the TUI, `zarlcode keys list` shows the global-scope key roster.

## Logging

Bubbletea's `tea.WithAltScreen()` captures stdout but NOT stderr. slog's default handler writes to `os.Stderr`, so any `slog` call before the file handler activates paints log lines directly over the TUI frame.

`tui/launch.go` calls `setupLaunchLogging` to redirect slog to a file-backed handler before the TUI starts. If setup fails, a discard handler stays in place and the failure is surfaced through the session — slog never falls back to stderr (which would corrupt the layout).

If you add startup logging that must be visible without a working file logger, post it through the session's toast/notice mechanisms — never directly through slog (hidden) or `fmt.Fprintln(os.Stderr, …)` (corrupts the frame).

## Live runner ownership

`engine.NewLiveRunner` installs a truthful no-op `LiveSink`; `WithLiveSink(nil)` panics. Construction-only dependencies use typed options. Do not compensate for invalid construction with nil checks in event or plan publishing paths.

Contexts are operation-scoped: turns, source construction, provider rebuilds, tool setup, and inspection receive context explicitly. `LiveRunner` does not store an application context. Runtime provider/spec/window changes use one atomic target transition so a turn cannot snapshot partially updated policy.

## Things to never do

- Put zarlcode preference keys or application-specific preference transitions in `zkit/prefs`. The app-owned catalogue and transitions belong in `zarlcode/prefs`; `zkit/prefs` exposes only generic scoped operations.
- Call `store.SetAPIKey` / `store.SetSetting` directly from a user-reachable write path. Use `prefs.Service.SetKey(scope, …)` / `prefs.Service.SetSetting(scope, …)` — direct calls bypass the scope enum's guarantees and the audit surface.
- Pass `""` as a workspace argument to mean "global". Use `prefs.ScopeGlobal`.
- Dual-write workspace + global. The promote action is the explicit publish path; dual-write diverges.
- Restore the slog default handler to anything but discard or the zlog file handler while the alt-screen is up. Stderr writes corrupt the layout.
