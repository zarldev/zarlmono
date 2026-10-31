# Shared appearance

`appearance` owns the single family theme selection and its explicit overrides. It uses the existing `tui/theme` catalogue, not a second palette registry.

Effective precedence:

1. this app in this workspace;
2. this app's default;
3. the global family selection;
4. the family fallback, `ansi-bbs`.

`DefaultPath` is `$XDG_CONFIG_HOME/zarl/appearance.db` (or the platform user-config directory). Reads and resets of missing configuration do not create files or directories. Writes are private SQLite transactions, so separate applications do not overwrite each other's settings. Each operation closes its own handles; there is no background watcher. Apps refresh inherited selections through their owned update loops, never from drawing.

Use stable application IDs (`zarlcode`, `zarlplayer`, `zarlchat`) and `CanonicalWorkspace` at the command/input boundary. A workspace is a project directory, not an account/profile data directory. The displayed player identity remains **zarlamp**.

`Global`, `App`, and `Workspace` construct explicit targets; `TargetFor` parses a user-facing scope. `Set` saves exactly one override; `Reset` removes exactly that override. Inherited values are not copied into more-specific scopes. `Lookup` exposes explicit values for source displays and backwards-compatible adapters. An unknown saved palette produces a diagnostic and falls through to a valid inherited palette; malformed storage is reported without silently replacing it.

`Picker` is backend-neutral. Moving a preview or changing target scope writes nothing. `Commit` saves; `Inherit` removes the targeted override; `Original` retains the complete opening palette for cancellation. `LinesWithStyle` uses the shared chrome selection treatment and respects the caller's plain/ASCII policy. It shows the effective theme/source separately from the selected preview and save scope. Apps own key routing, applying previews, restoring the opening value on cancel, and displaying persistence errors.

Zarlcode's explicit legacy DB preferences are compatibility app/workspace overrides. Its application adapter merges those at the corresponding specificity and removes them only through the prefs facade when the user explicitly chooses inheritance. Family appearance does not own credentials, account data, providers, or resource caches.

No JSON theme files are rewritten or reloaded by this store. The existing catalogue's custom-theme directory remains supported. `--theme` session overrides are separate from persistence, as are live uncommitted previews.
