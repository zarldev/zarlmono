package tui

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/joho/godotenv"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	agentmcp "github.com/zarldev/zarlmono/zkit/agent/mcp"
	"github.com/zarldev/zarlmono/zkit/agent/sandbox"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/ai/tools/dynamic"
	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/filesystem"
	"github.com/zarldev/zarlmono/zkit/prefs"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
	"github.com/zarldev/zarlmono/zkit/zapp"
	"github.com/zarldev/zarlmono/zkit/zlog"
)

// Zarlcode is the running application: workspace, settings, the live runner,
// and the bubbletea model. It's the typed instance carried by the zapp
// lifecycle harness — [Launch.Create] wires it (registering closers with the
// app), [Launch.Run] drives it.
type Zarlcode struct {
	root     string
	ws       code.Workspace
	settings *engine.Settings
	sink     *teasink.Sink
	model    *UI
	live     *engine.LiveRunner
	prov     llm.Provider
	spec     engine.ProviderSpec
}

// Launch implements zapp.Program[*Zarlcode]. Flag values are parsed in
// zarlcode.Main and threaded through here so Create/Run never touch the flag
// package — they read intent off the struct.
type Launch struct {
	EnvFile   string
	AgentName string
	Resume    bool
	Headless  bool
	Prompt    string // pre-resolved in Main from --prompt-file/--prompt-text
	MaxIter   int
}

// Name identifies the program to the zapp harness (errors, signals).
func (Launch) Name() string { return "zarlcode" }

// Create wires the application against the workspace (the launch cwd):
// optional .env, file-only logging (the alt-screen owns stdout), shared
// ~/.zarlcode settings + provider, the bubbletea model, and the live runner.
// Long-lived resources are registered with app.AddCloser so the harness
// closes them deterministically on exit.
func (p Launch) Create(ctx context.Context, app *zapp.App[*Zarlcode]) (*Zarlcode, error) {
	if p.EnvFile != "" {
		// Overload so the .env wins over a stale ambient value, matching the
		// eval driver's --env behaviour.
		_ = godotenv.Overload(p.EnvFile)
	}

	// Redirect logs to a file BEFORE the alt-screen opens — slog/log default
	// to stderr, which would paint over the rendered frame.
	_, logCloser := setupLaunchLogging()
	_ = app.AddCloser("logs", logCloser)
	// Surface any embedded-theme load failure now that logging is wired but
	// before the alt-screen opens — a corrupt builtin theme degrades the
	// palette, and this is the one place that's diagnosable.
	if err := theme.LoadError(); err != nil {
		slog.WarnContext(ctx, "theme: embedded builtins failed to load", "err", err)
	}
	root, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("cwd: %w", err)
	}
	ws, err := code.NewWorkspace(root)
	if err != nil {
		return nil, fmt.Errorf("workspace %q: %w", root, err)
	}

	// Peek at the persisted theme so the pre-settings splash (vault unlock)
	// matches the user's chosen palette, not just the env/default. The
	// full settings path below replaces this wholesale, so the peek is
	// only for the startup screen.
	UseTheme(peekTheme(ctx, root))

	settings, err := engine.OpenSettings(ctx, root, vaultPassphraseFunc(ctx, !p.Headless))
	if err != nil {
		return nil, fmt.Errorf("settings: %w", err)
	}
	_ = app.AddCloser("settings", settings)

	// Kernel sandbox for shell commands: Landlock filesystem allow-list
	// rooted at the workspace (zkit/agent/sandbox). One instance shared
	// by foreground bash and the process manager so both run under the
	// same policy. On kernels without Landlock the shell runs unconfined
	// with a warning — the guardrail chain still applies either way.
	var sb code.Sandboxer
	sbPolicy := sandbox.DefaultPolicy(ws.Root())
	var askpassSrv *askpassServer
	var toolEnv map[string]string
	if !p.Headless && settings.SudoAskpass(ctx) {
		askpassSrv, err = newAskpassServer(ctx, root)
		if err != nil {
			slog.WarnContext(ctx, "askpass: sudo integration unavailable", "err", err)
		} else {
			_ = app.AddCloser("askpass", askpassSrv)
			toolEnv = askpassSrv.Env()
			sbPolicy = sbPolicy.WithExecPath(askpassSrv.script)
		}
	}
	if cp := settings.ChromeBinPath(ctx); cp != "" {
		sbPolicy = sbPolicy.WithExecPath(cp)
	}
	sandboxEnabled := settings.ShellSandbox(ctx)
	if enabled, ok := sandbox.EnvOverride(); ok {
		sandboxEnabled = enabled
	}
	if !sandboxEnabled {
		if _, ok := sandbox.EnvOverride(); ok {
			slog.InfoContext(ctx, "sandbox: shell confinement disabled via ZARLCODE_SANDBOX override")
		} else {
			slog.InfoContext(ctx, "sandbox: shell confinement disabled in settings")
		}
	} else if s, err := sandbox.New(sbPolicy); err != nil {
		slog.WarnContext(ctx, "sandbox: shell confinement unavailable, running unconfined", "err", err)
	} else {
		sb = s
	}

	// Background-process manager for bash(background=true) + the
	// bash_output / stop_process / list_processes tools. Limits come from
	// settings (process section). Closed on exit so a server/watcher the agent
	// started doesn't leak past the shell.
	maxAlive, bufferLines := settings.ProcessLimits(ctx)
	pm := code.NewProcessManager(ws,
		code.WithMaxAliveProcesses(maxAlive),
		code.WithProcessOutputBuffer(bufferLines),
		code.WithProcessSandbox(sb),
		code.WithProcessEnv(toolEnv),
	)
	_ = app.AddCloser("processes", closerFunc(func() error {
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		pm.Close(shutdownCtx)
		return nil
	}))

	// Provider/model/theme come from the shared ~/.zarlcode settings opened
	// above, with the ZARLCODE_* env vars as the fallback for any unset row.
	fallback := engine.ProviderSpec{
		Name:    "llamacpp",
		Model:   envOr("ZARLCODE_MODEL", "local"),
		BaseURL: os.Getenv("ZARLCODE_BASE_URL"),
		APIKey:  os.Getenv("ZARLCODE_API_KEY"),
	}
	prov, spec, err := settings.BuildActive(ctx, fallback)
	if err != nil {
		model := New()
		model.SetWorkspace(root, "")
		model.SetStartupFailure(root, "provider startup", fmt.Sprintf("provider %q: %v", spec.Name, err))
		return &Zarlcode{
			root:  root,
			ws:    ws,
			model: model,
			spec:  spec,
		}, nil
	}

	// Sink first (no send yet); Run wires it to the program once it exists.
	sink := teasink.New(nil)
	_ = app.AddCloser("sink", closerFunc(func() error { sink.Close(); return nil }))

	UseTheme(selectThemeByName(settings.Theme(ctx, envOr("ZARLCODE_THEME", "catppuccin-mocha"))))

	// Resolve the ACTIVE provider's compaction budget. Local backends
	// report it at runtime; hosted providers usually use a static table;
	// Codex OAuth asks /codex/models with the vault token for its auto-compact cap.
	ctxWindow := settings.ContextWindow(ctx, spec)

	m := New()
	m.SetWorkspace(root, spec.Model)
	m.SetProvider(spec.Name)
	m.SetContextWindow(ctxWindow)
	m.SetSettings(settings) // ctrl+s settings overlay
	m.SetProviderContext(fallback, spec)
	m.appliedReasoning, m.appliedWindow = activeProviderPolicy(settings, spec.Name) // baseline for maybeRepoint

	live := engine.NewLiveRunner(prov, ws, sink, spec.Model)
	live.SetContext(ctx)
	_ = app.AddCloser("live", closerFunc(func() error {
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		return live.Close(shutdownCtx)
	}))

	// MCP: a persistent registry holding live external-server connections.
	// Connected servers' tools land on mcpHost and are merged into each turn's
	// registry; the connect/disconnect/list tools are bound to mcpReg so a
	// connection made one turn survives into the next. Server notifications are
	// queued into the same live-turn steerer as user-entered mid-run input.
	mcpHost := tools.NewRegistry()
	mcpReg := dynamic.NewMCPRegistry(mcpHost, agentmcp.NotifierFor(live.QueueInjector()))
	connectConfiguredMCPServers(ctx, settings, mcpReg)

	live.SetProcessManager(pm)
	live.SetSandbox(sb)
	live.SetToolEnv(toolEnv)
	live.SetMCP(mcpReg, mcpHost)
	live.SetProviderSpec(prov, spec)
	live.SetContextWindow(ctxWindow)
	live.SetSearxngURL(settings.SearxngURL(ctx)) // enable web_search (SearXNG)
	live.SetSettingsHandle(settings)             // resolve compaction engine live per turn
	lim := settings.Limits(ctx)
	live.SetLimits(lim.ReserveTokens, lim.MaxIterations, lim.SpawnMaxIterations, lim.SpawnMaxDepth)
	live.SetVerifyLoop(settings.VerifyLoop(ctx)) // headless verified re-drive (verify_tests / verify_attempts)
	m.SetPressureConfig(ctxWindow, lim.ReserveTokens)
	m.SetLiveRunner(live) // also sets the run handler; enables mid-session re-point
	m.askpass = askpassSrv

	// The intro (fresh-start prompt + saved-session picker) is an interactive
	// affordance; headless drives the loop directly in Run and skips it.
	if !p.Headless {
		m.ActivateIntro(ctx)
	}

	return &Zarlcode{
		root:     root,
		ws:       ws,
		settings: settings,
		sink:     sink,
		model:    m,
		live:     live,
		prov:     prov,
		spec:     spec,
	}, nil
}

// Run drives the application. --headless runs one task to completion and
// returns its exit code (no TUI). Otherwise it starts the bubbletea v2 loop,
// then persists the resumable session.
func (p Launch) Run(ctx context.Context, _ *zapp.App[*Zarlcode], z *Zarlcode) int {
	if p.Headless {
		return engine.RunHeadlessProcess(ctx, z.live, p.Prompt, p.MaxIter)
	}
	prog := tea.NewProgram(z.model, tea.WithContext(ctx))
	if z.sink != nil {
		z.sink.SetSend(prog.Send)
	}
	if z.model.askpass != nil {
		z.model.askpass.SetSend(prog.Send)
	}

	if _, err := prog.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "tui:", err)
		return zapp.ExitFailure
	}
	// Persist the resumable session on the way out. Detach from ctx (a
	// SIGINT-cancelled parent would otherwise abort the DB write) and bound it
	// so a large history's json.Marshal can't hang the shell after exit.
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if err := z.model.SaveSession(saveCtx); err != nil {
		fmt.Fprintln(os.Stderr, "session save:", err)
	}
	return zapp.ExitOK
}

// closerFunc adapts a func() error to io.Closer for app.AddCloser.
type closerFunc func() error

func (f closerFunc) Close() error { return f() }

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// connectConfiguredMCPServers connects every enabled MCP server from the
// persistent settings config through the connect tool, so a settings-defined
// server is live before the first turn. Failures are logged, never fatal —
// one bad server config must not block launch.
func connectConfiguredMCPServers(ctx context.Context, settings *engine.Settings, mcpReg *dynamic.MCPRegistry) {
	if settings == nil || settings.Store == nil || mcpReg == nil {
		return
	}
	servers, err := settings.Store.ListMCPServers(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mcp: list servers:", err)
		return
	}
	connect := dynamic.NewMCPConnect(mcpReg)
	for _, srv := range servers {
		if !srv.Enabled {
			continue
		}
		res, err := connect.Execute(ctx, tools.ToolCall{
			ID: "startup-mcp-" + srv.Name,
			Arguments: tools.ToolParameters{
				"name":       srv.Name,
				"transport":  srv.Transport,
				"command":    srv.Command,
				"args":       srv.Args,
				"env":        srv.Env,
				"base_url":   srv.BaseURL,
				"auth_token": resolveMCPAuthToken(ctx, settings, srv),
			},
		})
		switch {
		case err != nil:
			fmt.Fprintf(os.Stderr, "mcp: connect %q: %v\n", srv.Name, err)
		case res != nil && !res.Success:
			fmt.Fprintf(os.Stderr, "mcp: connect %q: %s\n", srv.Name, res.Error)
		}
	}
}

// resolveMCPAuthToken returns the bearer token for an MCP server, preferring
// the encrypted vault (provider key mcpAuthKeyProvider(name)) over the legacy
// plaintext column. A row that still carries a plaintext token — written
// before tokens moved to the vault — is migrated on first launch: the value
// is copied into the vault and the column cleared, so it stops living in the
// DB. When no vault is available the legacy plaintext is used as-is (degraded
// but functional). All failures are non-fatal: launch must not be blocked.
func resolveMCPAuthToken(ctx context.Context, settings *engine.Settings, srv db.MCPServerRow) string {
	if settings.Svc != nil {
		if k, ok, err := settings.Svc.GetKey(ctx, prefs.ScopeEffective, mcpAuthKeyProvider(srv.Name)); err == nil && ok && k != "" {
			return k
		}
	}
	if srv.AuthToken == "" {
		return ""
	}
	if settings.Svc == nil {
		return srv.AuthToken
	}
	// Legacy plaintext row: move it into the credential store, then clear the
	// column. The store writes plaintext or encrypted material according to the
	// user's credential_protection setting.
	if err := settings.Svc.SetKey(ctx, prefs.ScopeGlobal, mcpAuthKeyProvider(srv.Name), srv.AuthToken); err == nil {
		migrated := srv
		migrated.AuthToken = ""
		if uerr := settings.Store.UpsertMCPServer(ctx, migrated); uerr != nil {
			fmt.Fprintf(os.Stderr, "mcp: clear legacy token for %q: %v\n", srv.Name, uerr)
		}
	} else {
		fmt.Fprintf(os.Stderr, "mcp: migrate token for %q to vault: %v\n", srv.Name, err)
	}
	return srv.AuthToken
}

// selectThemeByName resolves a theme name to a builtin theme, falling back to
// the dark default when the name doesn't match.
func selectThemeByName(name string) theme.Theme {
	if t, ok := theme.ByName(name); ok {
		return t
	}
	return theme.DarkDefault()
}

// peekTheme reads the persisted theme from the shared db so the startup
// splash (vault unlock) can match the user's chosen palette. A brief
// connection is opened and closed before the long-lived settings store
// takes over; the double-open is a small one-time startup cost and
// ensures the flash screen carries the right colours. On any failure the
// theme degrades to the env/default; a missing setting is not an error.
func peekTheme(ctx context.Context, wsRoot string) theme.Theme {
	store, err := db.Open(ctx, "")
	if err != nil {
		return selectThemeByName(envOr("ZARLCODE_THEME", "catppuccin-mocha"))
	}
	defer store.Close()
	if name, ok, _ := store.GetSetting(ctx, wsRoot, prefs.KeyTheme); ok && name != "" {
		return selectThemeByName(name)
	}
	if name, ok, _ := store.GetSetting(ctx, "", prefs.KeyTheme); ok && name != "" {
		return selectThemeByName(name)
	}
	return selectThemeByName(envOr("ZARLCODE_THEME", "catppuccin-mocha"))
}

// setupLaunchLogging redirects slog and the stdlib log away from stderr
// (which would paint over the alt-screen) to a log file, returning its path
// and a closer. On failure it discards logs rather than corrupt the screen.
func setupLaunchLogging() (string, io.Closer) {
	dir, err := os.UserCacheDir()
	if err != nil || dir == "" {
		dir = os.TempDir()
	} else {
		dir = filepath.Join(dir, "zarlcode")
	}
	_ = os.MkdirAll(dir, filesystem.ModePrivateDir)

	path := filepath.Join(dir, "zarlcode.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, filesystem.ModePrivateFile)
	if err != nil {
		slog.SetDefault(slog.New(slog.DiscardHandler))
		zlog.SetStdlibOutput(io.Discard)
		return "", closerFunc(func() error { return nil })
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: slog.LevelInfo})))
	zlog.SetStdlibOutput(f)
	return path, f
}
