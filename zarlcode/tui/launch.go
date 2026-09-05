package tui

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/joho/godotenv"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/home"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zarlcode/sleepinhibit"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	agentmcp "github.com/zarldev/zarlmono/zkit/agent/mcp"
	"github.com/zarldev/zarlmono/zkit/agent/sandbox"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/ai/tools/dynamic"
	"github.com/zarldev/zarlmono/zkit/ai/tools/search"
	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/filesystem"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
	"github.com/zarldev/zarlmono/zkit/zapp"
	"github.com/zarldev/zarlmono/zkit/zlog"
)

const (
	startupMCPConnectTimeout = 15 * time.Second
	startupMetadataTimeout   = 3 * time.Second
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
	EnvFile       string
	AgentName     string
	Resume        bool
	Headless      bool
	Prompt        string // pre-resolved in Main from --prompt-file/--prompt-text
	MaxIter       int
	PprofAddr     string
	TraceFile     string
	PromptProfile engine.PromptProfile
	ReportFile    string
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
	root = ws.Root()
	if _, err := home.Materialise(); err != nil {
		return nil, fmt.Errorf("seed zarlcode home: %w", err)
	}
	if _, err := home.MaterialiseWorkspace(root); err != nil {
		return nil, fmt.Errorf("seed workspace extensions: %w", err)
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
	perf, err := startPerf(perfOptions{
		pprofAddr: firstNonEmpty(strings.TrimSpace(p.PprofAddr), settings.PprofAddr(ctx)),
		traceFile: firstNonEmpty(strings.TrimSpace(p.TraceFile), settings.TraceFile(ctx)),
	})
	if err != nil {
		return nil, fmt.Errorf("perf: %w", err)
	}
	_ = app.AddCloser("perf", closerFunc(func() error {
		if err := perf.stop(); err != nil {
			slog.DebugContext(ctx, "stop perf profiling", "error", err)
			return err
		}
		return nil
	}))

	// Kernel sandbox for shell commands: Landlock filesystem allow-list
	// rooted at the workspace (zkit/agent/sandbox). One instance shared
	// by foreground bash and the process manager so both run under the
	// same policy. On kernels without Landlock the shell runs unconfined
	// with a warning — the guardrail chain still applies either way.
	var sb code.Sandboxer
	sbPolicy := sandbox.DefaultPolicy(ws.Root())
	var askpassSrv *AskpassServer
	var toolEnv map[string]string
	if !p.Headless && settings.SudoAskpass(ctx) {
		askpassSrv, err = NewAskpassServer(ctx, root)
		if err != nil {
			slog.WarnContext(ctx, "askpass: sudo integration unavailable", "err", err)
		} else {
			_ = app.AddCloser("askpass", askpassSrv)
			toolEnv = askpassSrv.Env()
			sbPolicy = grantSandboxExecPath(sbPolicy, askpassSrv.script)
		}
	}
	if cp := settings.ChromeBinPath(ctx); cp != "" {
		sbPolicy = grantSandboxExecPath(sbPolicy, cp)
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
	} else if normal, err := sandbox.New(sbPolicy); err != nil {
		slog.WarnContext(ctx, "sandbox: shell confinement unavailable, running unconfined", "err", err)
	} else if verify, err := sandbox.New(sandbox.VerifyPolicy(sbPolicy, ws.Root())); err != nil {
		slog.WarnContext(ctx, "sandbox: verify confinement unavailable, running unconfined", "err", err)
	} else {
		sb = sandbox.NewWorkModeSandbox(normal, verify)
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
	_ = app.AddContextCloser("processes", zapp.ContextCloseFunc(func(shutdownCtx context.Context) error {
		pm.Close(shutdownCtx)
		return nil
	}))

	// Database settings override application defaults, never ambient credentials.
	fallback := engine.ProviderSpec{Name: backends.DefaultBuiltinName.String(), Model: "local"}
	prov, spec, err := settings.BuildActive(ctx, fallback)
	if err != nil {
		model := New()
		model.SetWorkspace(root, "")
		model.SetStartupFailure(root, "provider startup", fmt.Sprintf("provider %q: %v", spec.Name, err))
		model.SetSettings(settings)
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

	UseTheme(selectThemeByName(settings.Theme(ctx, "catppuccin-mocha")))

	// Use static provider metadata immediately. Interactive launches defer network
	// probes (local server props and Codex account limits) until Bubble Tea can
	// render; headless resolves the exact window before its first turn.
	ctxWindow := settings.Registry.ContextWindow(spec.Name, spec.Model)
	if p.Headless {
		ctxWindow = settings.ContextWindow(ctx, spec)
	}
	if ctxWindow <= 0 {
		ctxWindow = engine.LiveContextWindow
	}

	m := New()
	m.ctx = ctx
	m.SetWorkspace(root, spec.Model)
	m.SetProvider(spec.Name)
	m.SetContextWindow(ctxWindow)
	m.SetSettings(settings) // ctrl+s settings overlay
	m.SetProviderContext(fallback, spec)
	m.appliedReasoning, m.appliedWindow = activeProviderPolicy(settings, spec.Name) // baseline for maybeRepoint

	// Persist full, untruncated tool results to state.db for the ctrl+h
	// history viewer. Session identity resolves lazily at record time because
	// resume/new-session identity may not be set until later.
	toolSink := &engine.ToolOutputSink{
		Store:     settings.Store,
		SessionID: func() string { return m.session.ID },
	}

	// Tee background-process exit output into the same store. The manager is
	// constructed before the sink exists, so wire it here.
	pm.SetOutputSink(func(id code.ProcessID, command string, exitCode int, stdout, stderr []string) {
		toolSink.RecordProcess(ctx, id.String(), command, exitCode, stdout, stderr)
	})

	live := engine.NewLiveRunner(
		prov,
		ws,
		spec.Model,
		engine.WithPromptProfile(p.PromptProfile),
		engine.WithLiveSink(sink),
		engine.WithSettings(settings),
		engine.WithToolOutputSink(toolSink),
		engine.WithProcessManager(pm),
		engine.WithSandbox(sb),
		engine.WithToolEnvironment(toolEnv),
		engine.WithComputerHeadless(p.Headless),
	)
	_ = app.AddContextCloser("live", zapp.ContextCloseFunc(live.Close))

	// MCP: a persistent registry holding live external-server connections.
	// Connected servers' tools land on mcpHost and are merged into each turn's
	// registry; the connect/disconnect/list tools are bound to mcpReg so a
	// connection made one turn survives into the next. Server notifications are
	// queued into the same live-turn steerer as user-entered mid-run input.
	mcpHost := tools.NewRegistry()
	mcpReg := dynamic.NewMCPRegistry(mcpHost, agentmcp.NotifierFor(live.QueueInjector()))
	// Advisory startup discovery may update the target later, but it must not
	// gate prompt submission. LiveRunner target transitions are atomic, so a
	// turn already in flight keeps its snapshot and the update applies next turn.
	m.startupReady = true
	if !p.Headless {
		m.startupCmd = finishStartupMetadataCmd(ctx, settings, spec)
		m.startupMCPCmd = connectConfiguredMCPServersCmd(ctx, settings, mcpReg)
	}

	live.AttachMCP(mcpReg, mcpHost)
	live.SetProviderSpec(prov, spec)
	live.SetContextWindow(ctxWindow)
	// web_search: pick the configured backend and build its tool here, at the
	// composition root. SearXNG (self-host) needs a URL; Brave needs an API key
	// from the encrypted credential store.
	live.SetWebSearch(configuredWebSearch(ctx, settings))
	lim := settings.Limits(ctx)
	live.SetLimits(lim.ReserveTokens, lim.MaxIterations, lim.SpawnMaxIterations, lim.SpawnMaxDepth)
	live.SetVerifyLoop(settings.VerifyLoop(ctx)) // headless verified re-drive (verify_tests / verify_attempts)
	m.SetPressureConfig(ctxWindow, lim.ReserveTokens)
	m.SetLiveRunner(live) // also sets the run handler; enables mid-session re-point
	m.askpass = askpassSrv

	// Resume applies to both interactive and headless runs; only the intro is
	// an interactive affordance and is skipped in headless mode.
	if p.Resume {
		if err := m.resumeLatestSession(ctx); err != nil {
			return nil, fmt.Errorf("continue: %w", err)
		}
	} else if !p.Headless {
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

func configuredWebSearch(ctx context.Context, settings *engine.Settings) tools.Tool {
	if settings == nil {
		return nil
	}
	if settings.SearchProvider(ctx) == "brave" {
		return search.NewBrave(settings.SearchKey(ctx, engine.SearchKeyProviderBrave))
	}
	return search.NewSearxng(settings.SearxngURL(ctx))
}

// Run drives the application. --headless runs one task to completion and
// returns its exit code (no TUI). Otherwise it starts the bubbletea v2 loop,
// then persists the resumable session.
func (p Launch) Run(ctx context.Context, _ *zapp.App[*Zarlcode], z *Zarlcode) int {
	if p.Headless {
		var report *os.File
		if p.ReportFile != "" {
			f, err := os.Create(p.ReportFile)
			if err != nil {
				fmt.Fprintln(os.Stderr, "headless: report:", err)
				return zapp.ExitFailure
			}
			defer f.Close()
			report = f
		}
		inhibitor, err := sleepinhibit.Acquire(ctx)
		if err != nil {
			slog.WarnContext(ctx, "sleep inhibition unavailable", "err", err)
		} else {
			defer inhibitor.Close()
		}
		return engine.RunHeadlessProcess(ctx, z.live, p.Prompt, p.MaxIter, report)
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
	if err := z.model.FlushSessionPersistence(saveCtx); err != nil {
		fmt.Fprintln(os.Stderr, "session save:", err)
	}
	return zapp.ExitOK
}

func grantSandboxExecPath(policy sandbox.Policy, path string) sandbox.Policy {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" || !filepath.IsAbs(path) {
		return policy
	}
	grant := func(path string) {
		policy.ReadFiles = append(policy.ReadFiles, path)
		for dir := filepath.Dir(path); dir != "." && dir != string(filepath.Separator) && dir != ""; dir = filepath.Dir(dir) {
			policy.ReadDirs = append(policy.ReadDirs, dir)
		}
	}
	grant(path)
	if strings.HasSuffix(strings.ToLower(path), ".exe") {
		if b, err := os.ReadFile("/proc/sys/fs/binfmt_misc/WSLInterop"); err == nil {
			for line := range strings.SplitSeq(string(b), "\n") {
				if interpreter, ok := strings.CutPrefix(strings.TrimSpace(line), "interpreter "); ok && filepath.IsAbs(interpreter) {
					grant(filepath.Clean(interpreter))
				}
			}
		}
	}
	return policy
}

// closerFunc adapts a func() error to io.Closer for app.AddCloser.
type closerFunc func() error

func (f closerFunc) Close() error { return f() }

// connectConfiguredMCPServers connects every enabled MCP server from the
// persistent settings config through the connect tool, so a settings-defined
// server is live before the first turn. Failures are logged, never fatal —
// one bad server config must not block launch.
type startupReadyMsg struct {
	window       int
	provider     llm.Provider
	spec         engine.ProviderSpec
	modelChanged bool
}

func finishStartupMetadataCmd(ctx context.Context, settings *engine.Settings, spec engine.ProviderSpec) tea.Cmd {
	return func() tea.Msg {
		// Startup metadata is advisory and must never hold the first submitted
		// turn behind a slow model catalogue or local-provider probe. The static
		// window selected during Create remains the fallback.
		metadataCtx, cancel := context.WithTimeout(ctx, startupMetadataTimeout)
		defer cancel()
		validated, changed, err := settings.ValidateCodexModel(metadataCtx, spec)
		if err != nil {
			slog.WarnContext(ctx, "validate Codex model", "err", err)
			validated = spec
		}
		var provider llm.Provider
		if changed {
			provider, err = engine.BuildProvider(metadataCtx, settings.Registry, settings.Svc, validated)
			if err != nil {
				slog.WarnContext(ctx, "rebuild validated Codex provider", "err", err)
				validated, changed = spec, false
			}
		}
		return startupReadyMsg{
			window: settings.ContextWindow(metadataCtx, validated), provider: provider,
			spec: validated, modelChanged: changed,
		}
	}
}

func connectConfiguredMCPServersCmd(ctx context.Context, settings *engine.Settings, mcpReg *dynamic.MCPRegistry) tea.Cmd {
	return func() tea.Msg {
		connectConfiguredMCPServers(ctx, settings, mcpReg)
		return nil
	}
}

func connectConfiguredMCPServers(ctx context.Context, settings *engine.Settings, mcpReg *dynamic.MCPRegistry) {
	if settings == nil || settings.Store == nil || mcpReg == nil {
		return
	}
	servers, err := settings.Store.ListMCPServers(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mcp: list servers:", err)
		return
	}
	type startupMCPServer struct {
		row       db.MCPServerRow
		authToken string
	}
	startupServers := make([]startupMCPServer, 0, len(servers))
	for _, srv := range servers {
		if !srv.Enabled {
			continue
		}
		// Resolve/migrate credentials before dialing concurrently. The dial path is
		// I/O-bound and safe to fan out; keeping credential-store writes serial avoids
		// turning startup into a burst of SQLite/vault mutations.
		startupServers = append(startupServers, startupMCPServer{
			row:       srv,
			authToken: resolveMCPAuthToken(ctx, settings, srv),
		})
	}
	if len(startupServers) == 0 {
		return
	}

	connect := dynamic.NewMCPConnect(mcpReg)
	var wg sync.WaitGroup
	var errMu sync.Mutex
	for _, srv := range startupServers {
		wg.Add(1)
		go func(srv startupMCPServer) {
			defer wg.Done()
			connectCtx, cancel := context.WithTimeout(ctx, startupMCPConnectTimeout)
			defer cancel()
			res, err := connect.Execute(connectCtx, tools.ToolCall{
				ID: tools.ToolCallID("startup-mcp-" + srv.row.Name),
				Arguments: tools.ToolParameters{
					"name":       srv.row.Name,
					"transport":  srv.row.Transport,
					"command":    srv.row.Command,
					"args":       srv.row.Args,
					"env":        srv.row.Env,
					"base_url":   srv.row.BaseURL,
					"auth_token": srv.authToken,
				},
			})
			errMu.Lock()
			defer errMu.Unlock()
			switch {
			case err != nil:
				slog.WarnContext(ctx, "mcp: startup connect", "server", srv.row.Name, "err", err)
			case res != nil && !res.Success:
				slog.WarnContext(ctx, "mcp: startup connect", "server", srv.row.Name, "err", res.Error)
			}
		}(srv)
	}
	wg.Wait()
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
		if k, err := settings.Svc.GetKey(ctx, prefs.ScopeEffective, mcpAuthKeyProvider(srv.Name)); err == nil && k != "" {
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
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
// theme uses the application default; a missing setting is not an error.
func peekTheme(ctx context.Context, wsRoot string) theme.Theme {
	store, err := db.Open(ctx, "")
	if err != nil {
		return selectThemeByName("catppuccin-mocha")
	}
	defer store.Close()
	svc := prefs.NewService(store, nil, wsRoot)
	setting, err := svc.GetSetting(ctx, prefs.ScopeEffective, prefs.KeyTheme)
	if err == nil && setting.Value != "" {
		return selectThemeByName(setting.Value)
	}
	return selectThemeByName("catppuccin-mocha")
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
