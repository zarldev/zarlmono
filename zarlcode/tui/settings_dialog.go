package tui

import (
	"context"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/prefs"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

// settingsDialog is the master-detail settings overlay: a category nav
// column on the left, the selected category's rows on the right with inline
// edit. Unlike the quick pickers (themePicker), it is stateful and
// persistent, so it owns its own side effects (prefs writes, live theme
// apply) rather than returning intents — it holds the *engine.Settings handle that
// those effects need. It pops only on esc/ctrl+s.
//
// Writes land at workspace scope (a per-project pin); `p` promotes the
// selected row's value to global (the default every workspace inherits).
// Each row shows the scope it resolved from so precedence is visible.
type settingsDialog struct {
	ctx       context.Context
	s         *engine.Settings
	cats      []settingsCat
	cat       int  // selected category (nav)
	row       int  // selected row within the category
	focusRows bool // false = nav column focused, true = detail rows
	editing   bool // inline editor open on the current row
	editor    composer
	status    string    // last-action toast text
	statusAt  time.Time // when status was set, so the footer toast ages out

	// modelsLoaded distinguishes a completed empty result from not yet fetched;
	// modelsErr keeps failures visible on the row after the footer toast ages out.
	models        map[string][]string
	modelsLoaded  map[string]bool
	modelsLoading map[string]bool
	modelsErr     map[string]error
	// pendingFetch is a provider whose model list to fetch once a nested
	// picker closes — set when the compaction provider changes (a picker
	// closure can't return a fetch intent itself). Drained in handleAction.
	pendingFetch string

	// providers is the inline panel rendered as the detail of the
	// Providers category.
	providers *providersDialog
	// gallery is the inline theme grid rendered as the detail of the
	// Appearance category.
	gallery *themeGallery
	// catalogPane is the read-only inventory panel rendered as the detail of
	// the Catalog category (agents / skills / hooks as sub-tabs).
	catalogPane *catalogPane
	// mcp is the editable MCP-server list rendered as the detail of the MCP
	// category.
	mcp *mcpPane
}

// modelCustomSentinel is the model-picker entry that drops to free-text
// entry instead of choosing a fetched model.
const modelCustomSentinel = "✎ custom model…"

// compactActiveSentinel is the compaction / judge provider+model picker entry
// meaning "reuse the active provider/model" — committing it clears the
// override.
const compactActiveSentinel = "(active)"

const codexEffortAuto = "(auto)"

type settingsRowKind int

const (
	rowText   settingsRowKind = iota // free text; enter opens the inline editor
	rowEnum                          // pick-one; enter/→ cycles, committed live
	rowAction                        // enter opens a nested dialog (open())
	rowModel                         // enter opens the per-provider model picker
	rowKey                           // vault-stored credential; enter edits it (masked)
	rowAgent                         // enter opens the discovered agent-profile picker
)

type settingsCat struct {
	name string
	rows []settingsRow
	// providers marks the category whose detail is the inline providers
	// panel (the list + per-provider actions render in the detail region,
	// not a separate popup).
	providers bool
	// gallery marks the category whose detail is the inline theme gallery
	// (a live-preview grid of every theme), instead of setting rows.
	gallery bool
	// catalog marks the category whose detail is the combined
	// agents/skills/hooks inventory panel.
	catalog bool
	// mcp marks the category whose detail is the editable MCP-server list.
	mcp bool
}

// settingsRow is one editable preference. The static fields (label, key,
// kind, def, opts, numeric) are fixed at construction; value/scope/isSet are
// refreshed from the store on open and after every mutation.
type settingsRow struct {
	label   string
	section string // optional group header rendered above the row
	key     string
	cred    string // rowKey: api_keys provider tag
	kind    settingsRowKind
	def     string                        // shown (dim) when no row is set
	desc    string                        // one-line help shown in the detail panel
	opts    []string                      // rowEnum options
	numeric bool                          // validate as a non-negative integer before commit
	max     int                           // numeric upper bound; zero means no explicit ceiling
	open    func(*engine.Settings) dialog // rowAction: builds the nested dialog

	value string
	scope prefs.Scope
	isSet bool
}

func newSettingsDialog(ctx context.Context, s *engine.Settings) *settingsDialog {
	d := &settingsDialog{
		ctx:           ctx,
		s:             s,
		models:        map[string][]string{},
		modelsLoaded:  map[string]bool{},
		modelsLoading: map[string]bool{},
		modelsErr:     map[string]error{},
		providers:     newProvidersDialog(ctx, s),
		gallery:       newThemeGalleryWithContext(ctx, s),
		catalogPane:   newCatalogPane(s),
		mcp:           newMCPPane(ctx, s),
		cats: []settingsCat{
			{name: "model", rows: []settingsRow{
				{label: "provider", key: prefs.KeyProvider, kind: rowEnum, def: "llamacpp", opts: providerNames(s),
					desc: "which llm backend runs the agent. manage keys & sign-in under providers."},
				{label: "model", key: prefs.KeyModel, kind: rowModel, def: "(provider default)",
					desc: "model id for the active provider. enter to pick from the fetched list."},
				{label: "agent", key: prefs.KeyAgent, kind: rowText, def: "default",
					desc: "named agent preset; 'default' is the built-in coding agent."},
				{label: "reasoning effort", key: prefs.KeyCodexEffort, kind: rowEnum, def: codexEffortAuto,
					desc: "reasoning effort for OpenAI Codex models. (auto) uses the model/default heuristic; options narrow to the selected model when known."},
				{label: "temperature", key: prefs.KeyTemperature, kind: rowEnum, def: "(default)", opts: []string{"(default)", "0", "0.2", "0.5", "0.7", "1.0"},
					desc: "sampling temperature. (default) leaves it to the server; a low value (0–0.2) makes local models more deterministic and improves tool-call reliability."},
			}},
			{name: "providers", providers: true},
			{name: "catalog", catalog: true},
			{name: "context", rows: []settingsRow{
				{label: "reserve tokens", section: "Headroom", key: prefs.KeyReserveTokens, kind: rowText, numeric: true, def: "512",
					desc: "headroom held back from the context window for the compactor."},
				{label: "tool result max kb", section: "Headroom", key: prefs.KeyToolResultMaxKB, kind: rowText, numeric: true, def: "50",
					desc: "cap on a tool result (KB) before tail-truncation + spill to disk. lower it for small-context local models."},
				{label: "tool result max lines", section: "Headroom", key: prefs.KeyToolResultMaxLines, kind: rowText, numeric: true, def: "2000",
					desc: "line cap on a tool result before tail-truncation + spill to disk."},
				{label: "mode", section: "Compaction", key: prefs.KeyCompactionMode, kind: rowEnum, def: "auto", opts: []string{"auto", "manual"},
					desc: "auto trims history under context pressure automatically. manual leaves it intact, warns in the cockpit near the limit, and waits for you to compact on demand (conversation actions › compact)."},
				{label: "engine", section: "Compaction", key: prefs.KeyCompactEngine, kind: rowEnum, def: "tiered", opts: compactEngineOpts(),
					desc: "how chats are condensed: structural trims, tiered ramps, summary/executive use an llm. handover clears the whole context and reseeds from a handover document written to .zarlcode/handovers/."},
				{label: "provider", section: "Compaction", key: prefs.KeyCompactProvider, kind: rowEnum, def: "(active)",
					desc: "provider for llm compaction (summary/executive). (active) reuses the active provider."},
				{label: "model", section: "Compaction", key: prefs.KeyCompactModel, kind: rowModel, def: "(active)",
					desc: "model for llm compaction, from the compaction provider's list. (active) reuses the active model."},
			}},
			{name: "limits", rows: []settingsRow{
				{label: "enable sub-agents", section: "Sub-agents", key: prefs.KeySpawnEnabled, kind: rowEnum, def: "off", opts: []string{"off", "on"},
					desc: "register agent_spawn/await/status/stop/list for each turn. off removes delegation from the model's tool surface entirely."},
				{label: "max iterations", key: prefs.KeyMaxIterations, kind: rowText, numeric: true, max: 1000, def: "20",
					desc: "cap on the agent loop per turn before it must finalize. maximum 1000."},
				{label: "response timeout", key: prefs.KeyResponseTimeout, kind: rowText, numeric: true, def: "90",
					desc: "seconds to wait with no output from the model before cancelling the iteration. raise it for slow local models/connections; non-positive falls back to 90."},
				{label: "spawn max iterations", key: prefs.KeySpawnMaxIterations, kind: rowText, numeric: true, max: 1000, def: "20",
					desc: "cap on sub-agent iterations per agent_spawn call. unset inherits the parent max; maximum 1000."},
				{label: "spawn await timeout", key: prefs.KeySpawnAwaitTimeout, kind: rowText, numeric: true, def: "30",
					desc: "seconds agent_await waits before returning a RUNNING snapshot without cancelling the sub-agent."},
				{label: "spawn await max timeout", key: prefs.KeySpawnAwaitMaxTimeout, kind: rowText, numeric: true, def: "300",
					desc: "maximum timeout_seconds an agent may request for one agent_await call. 0 disables the ceiling."},
				{label: "spawn depth", key: prefs.KeySpawnMaxDepth, kind: rowText, numeric: true, max: 16, def: "(unset)",
					desc: "how deep agent_spawn may recurse. unset uses the built-in default; maximum 16."},
				{label: "fanout cap", key: prefs.KeyFanoutCap, kind: rowText, numeric: true, max: 1000, def: "0",
					desc: "max calls per capped discovery tool (ls/grep/glob) per task. 0 keeps the built-in per-tool defaults; a positive value caps them uniformly; maximum 1000."},
				{label: "spawn fanout cap", key: prefs.KeySpawnFanoutCap, kind: rowText, numeric: true, max: 1000, def: "8",
					desc: "max agent_spawn calls per task before the guardrail refuses more. bounds a model that keeps firing sub-agents. 0 removes the cap; maximum 1000."},
				{label: "default explore agent", section: "Sub-agents", key: prefs.KeySpawnDefaultExploreAgent, kind: rowAgent, def: agentParentSentinel,
					desc: "named agent profile used when agent_spawn omits agent in explore mode. enter to choose a discovered profile."},
				{label: "default verify agent", section: "Sub-agents", key: prefs.KeySpawnDefaultVerifyAgent, kind: rowAgent, def: agentParentSentinel,
					desc: "named agent profile used when agent_spawn omits agent in verify mode. enter to choose a discovered profile."},
				{label: "default implement agent", section: "Sub-agents", key: prefs.KeySpawnDefaultImplementAgent, kind: rowAgent, def: agentParentSentinel,
					desc: "named agent profile used when agent_spawn omits agent in implement mode (including omitted mode). enter to choose a discovered profile."},
				{label: "explore provider", key: prefs.KeySpawnDefaultExploreProvider, kind: rowEnum, def: "(active)",
					desc: "provider for unnamed explore tasks when no default agent profile is selected. (active) reuses the parent provider."},
				{label: "explore model", key: prefs.KeySpawnDefaultExploreModel, kind: rowModel, def: "(active)",
					desc: "model for unnamed explore tasks, from the explore provider. ignored when a named agent profile is selected."},
				{label: "verify provider", key: prefs.KeySpawnDefaultVerifyProvider, kind: rowEnum, def: "(active)",
					desc: "provider for unnamed verify tasks when no default agent profile is selected. (active) reuses the parent provider."},
				{label: "verify model", key: prefs.KeySpawnDefaultVerifyModel, kind: rowModel, def: "(active)",
					desc: "model for unnamed verify tasks, from the verify provider. ignored when a named agent profile is selected."},
				{label: "implement provider", key: prefs.KeySpawnDefaultImplementProvider, kind: rowEnum, def: "(active)",
					desc: "provider for unnamed implement tasks when no default agent profile is selected. (active) reuses the parent provider."},
				{label: "implement model", key: prefs.KeySpawnDefaultImplementModel, kind: rowModel, def: "(active)",
					desc: "model for unnamed implement tasks, from the implement provider. ignored when a named agent profile is selected."},
				{label: "explore iterations", section: "Sub-agents", key: prefs.KeySpawnExploreMaxIterations, kind: rowText, numeric: true, max: 1000, def: "(shared)",
					desc: "per-explore child iteration cap. empty/0 inherits spawn max iterations; maximum 1000."},
				{label: "verify iterations", section: "Sub-agents", key: prefs.KeySpawnVerifyMaxIterations, kind: rowText, numeric: true, max: 1000, def: "(shared)",
					desc: "per-verify child iteration cap. empty/0 inherits spawn max iterations; maximum 1000."},
				{label: "implement iterations", section: "Sub-agents", key: prefs.KeySpawnImplementMaxIterations, kind: rowText, numeric: true, max: 1000, def: "(shared)",
					desc: "per-implement child iteration cap. empty/0 inherits spawn max iterations; maximum 1000."},
				{label: "max concurrent", section: "Sub-agents", key: prefs.KeySpawnMaxConcurrent, kind: rowText, numeric: true, max: 256, def: "0",
					desc: "simultaneously running sub-agents per turn. 0 is unbounded; spawn fanout still caps total calls. maximum 256."},
				{label: "max runtime seconds", section: "Sub-agents", key: prefs.KeySpawnMaxRuntime, kind: rowText, numeric: true, max: 604800, def: "0",
					desc: "maximum total lifetime for each child task. 0 is unbounded; timeout is reported as budget exhaustion. maximum 604800 (7 days)."},
				{label: "fallback", section: "Sub-agents", key: prefs.KeySpawnFallback, kind: rowEnum, def: "planner", opts: []string{"planner", "parent", "error"},
					desc: "unresolved routing: planner tries a registered profile then parent; parent skips planning; error refuses the spawn."},
				{label: "program parallel calls", key: prefs.KeyProgramParallel, kind: rowText, numeric: true, def: "0",
					desc: "max nested program-tool calls call_many runs concurrently. 0 keeps the built-in default (8)."},
			}},
			{name: "safety", rows: []settingsRow{
				{label: "plan first", section: "Guardrails", key: prefs.KeyPlanFirst, kind: rowEnum, def: "off", opts: []string{"off", "on"},
					desc: "require update_plan before the first workspace-changing call in a task. on makes planning mandatory (weak/local models); off lets the model dive straight in."},
				{label: "read before write", section: "Guardrails", key: prefs.KeyReadBeforeWrite, kind: rowEnum, def: "off", opts: []string{"off", "advisory", "strict"},
					desc: "require the task to read the target file or nearby context before edit/write. advisory and strict both refuse blind edits; strict is the strongest local-model setting."},
				{label: "test edit guard", section: "Guardrails", key: prefs.KeyTestEditGuard, kind: rowEnum, def: "off", opts: []string{"off", "advisory", "strict"},
					desc: "watch for edits to test files that would make a failing test pass without fixing the code. advisory warns; strict refuses. headless runs are always strict."},
				{label: "improvement loop", section: "Guardrails", key: prefs.KeyImprovementGuard, kind: rowEnum, def: "on", opts: []string{"on", "off"},
					desc: "keep the agent working while its verifiers still report failure instead of stopping early. off removes the guardrail from the chain."},
				{label: "skill hints", section: "Guardrails", key: prefs.KeySkillHints, kind: rowEnum, def: "on", opts: []string{"on", "off"},
					desc: "suggest a recovery skill after a tool call keeps failing. off removes the guardrail from the chain."},
				{label: "decompose judge", section: "Guardrails", key: prefs.KeyDecomposeJudge, kind: rowEnum, def: "off", opts: []string{"off", "on"},
					desc: "llm verdict judge for repeatedly failing tool calls (grammar-constrained enum). off keeps the deterministic advisory."},
				{label: "judge provider", section: "Guardrails", key: prefs.KeyJudgeProvider, kind: rowEnum, def: "(active)",
					desc: "provider for judge verdicts. (active) reuses the active provider — verdicts want a small fast model."},
				{label: "judge model", section: "Guardrails", key: prefs.KeyJudgeModel, kind: rowModel, def: "(active)",
					desc: "model for judge verdicts, from the judge provider's list. (active) reuses the active model."},
				{label: "shell policy", section: "Shell", key: prefs.KeyShellGuard, kind: rowEnum, def: "auto", opts: []string{"auto", "strict", "lenient", "off"},
					desc: "static shell-command guardrail leniency. auto follows the sandbox (strict when on, lenient when off); strict/lenient pin it regardless of the sandbox; off removes the guardrail from the chain entirely."},
				{label: "sandbox", section: "Shell", key: prefs.KeySandbox, kind: rowEnum, def: "on", opts: []string{"on", "off"},
					desc: "kernel-enforced filesystem confinement for bash commands. turn off only when a command needs host paths outside the workspace allow-list. applies on restart."},
				{label: "sudo askpass", section: "Shell", key: prefs.KeySudoAskpass, kind: rowEnum, def: "off", opts: []string{"off", "on"},
					desc: "enable sudo -A support for bash commands. when on, sudo requests show a password popup in the TUI. applies on restart."},
				{label: "verify command", section: "Verification", key: prefs.KeyVerifyTests, kind: rowText, def: "(off)",
					desc: "headless oracle: shell command (sh -c) whose zero exit means verified done; failures re-drive the agent."},
				{label: "verify attempts", section: "Verification", key: prefs.KeyVerifyAttempts, kind: rowText, numeric: true, def: "1",
					desc: "headless verified re-drive attempt cap. 1 = single-shot; the loop arms at 2+ with a command set."},
				{label: "credential protection", section: "Credentials", key: prefs.KeyCredentialProtection, kind: rowEnum, def: prefs.CredentialProtectionOff, opts: []string{prefs.CredentialProtectionOff, prefs.CredentialProtectionPassphrase},
					desc: "off stores credentials plaintext in state.db. passphrase encrypts them and prompts on startup. toggling migrates stored keys."},
			}},
			{name: "tools", rows: []settingsRow{
				{label: "web tools", section: "Surface", key: prefs.KeyEnableWeb, kind: rowEnum, def: "on", opts: []string{"on", "off"},
					desc: "register web_search + web_fetch. off drops both from the tool surface for a leaner local-model setup."},
				{label: "programmatic tools", section: "Surface", key: prefs.KeyProgrammaticTools, kind: rowEnum, def: "on", opts: []string{"on", "off"},
					desc: "replace direct read/search/catalogue tools with one program tool for bounded Starlark fan-out and aggregation."},
				{label: "mcp tools", section: "Surface", key: prefs.KeyEnableMCP, kind: rowEnum, def: "on", opts: []string{"on", "off"},
					desc: "register the mcp_connect/disconnect/list tools. off drops MCP management from the tool surface."},
				{label: "background processes", section: "Surface", key: prefs.KeyEnableBackground, kind: rowEnum, def: "on", opts: []string{"on", "off"},
					desc: "enable bash background mode + the bash_output/stop_process/list_processes tools. off drops the trio and bash runs foreground-only."},
				{label: "web search provider", section: "Services", key: prefs.KeySearchProvider, kind: rowEnum, def: "searxng", opts: []string{"searxng", "brave"},
					desc: "backend the web_search tool queries. brave uses the brave_search key from the credential store (set it with: zarlcode keys set brave_search <key>); searxng uses the endpoint below."},
				{label: "web search key", section: "Services", kind: rowKey, cred: engine.SearchKeyProviderBrave, def: "(unset)",
					desc: "brave search api key for web_search, stored encrypted in the credential vault (global — shared across workspaces). enter to set/replace; empty + enter clears."},
				{label: "web search", section: "Services", key: prefs.KeySearxngURL, kind: rowText, def: engine.DefaultSearxngURL,
					desc: "searxng endpoint the web_search tool queries. empty uses the local default."},
				{label: "local web_search service", section: "Services", kind: rowAction, def: "SearXNG",
					desc: "install/start the optional bundled SearXNG Docker Compose service for web_search. model servers stay external.",
					open: func(*engine.Settings) dialog { return newServiceDialog(ctx) }},
				{label: "chrome path", section: "Services", key: prefs.KeyChromeBinPath, kind: rowText, def: "(auto-detect)",
					desc: "absolute path to a Chrome or Chromium binary for the web_fetch browser fallback. empty auto-detects."},
				{label: "editor", section: "Services", key: prefs.KeyEditor, kind: rowText, def: "(uses $EDITOR)",
					desc: "command to edit agents/skills (may carry flags, e.g. 'code -w'). empty falls back to $ZARLCODE_EDITOR / $VISUAL / $EDITOR, then vi."},
				{label: "max alive", section: "Processes", key: prefs.KeyMaxAliveProcesses, kind: rowText, numeric: true, def: "16",
					desc: "cap on concurrent background bash processes. applies on restart."},
				{label: "output buffer", section: "Processes", key: prefs.KeyProcessOutputBuffer, kind: rowText, numeric: true, def: "10000",
					desc: "lines of output retained per background process. applies on restart."},
			}},
			{name: "mcp", mcp: true},
			{name: "appearance", gallery: true, rows: []settingsRow{
				{label: "theme", key: prefs.KeyTheme, kind: rowEnum, def: palette.Name, opts: themeNames(),
					desc: "colour theme. move to preview live; enter to keep."},
			}},
			{name: "interface", rows: []settingsRow{
				{label: "confirm quit", key: prefs.KeyConfirmQuit, kind: rowEnum, def: "on", opts: []string{"on", "off"},
					desc: "show a confirmation prompt before quitting via ctrl+c. turn off to quit instantly."},
				{label: "notification sounds", key: prefs.KeyNotificationSounds, kind: rowEnum, def: "completion", opts: []string{"off", "completion", "all"},
					desc: "terminal bell on completed runs; all also rings when plan steps become completed."},
				{label: "pprof address", section: "Diagnostics", key: prefs.KeyPprofAddr, kind: rowText, def: "(off)",
					desc: "optional listen address for Go pprof + runtime metrics, e.g. 127.0.0.1:6060. applies on restart; CLI -pprof overrides."},
				{label: "trace file", section: "Diagnostics", key: prefs.KeyTraceFile, kind: rowText, def: "(off)",
					desc: "optional Go execution trace output path. applies on restart; CLI -trace overrides."},
			}},
		},
	}
	d.gallery.onError = func(err error) { d.setStatus("theme: " + err.Error()) }
	d.refresh(ctx)
	return d
}

// compactEngineOpts is the selectable compaction engines, default (tiered)
// first. Mirrors compact.ParseEngine's accepted names.
func compactEngineOpts() []string {
	return []string{"tiered", "structural", "summary", "executive", "handover"}
}

func providerNames(s *engine.Settings) []string {
	if s == nil || s.Registry == nil {
		return nil
	}
	defs := s.Registry.All()
	out := make([]string, 0, len(defs))
	for _, def := range defs {
		out = append(out, def.Name)
	}
	return out
}

func themeNames() []string {
	bs := theme.Builtins()
	out := make([]string, 0, len(bs))
	for _, t := range bs {
		out = append(out, t.Name)
	}
	slices.Sort(out)
	return out
}

// refresh re-reads every row's effective value + source from the store, so
// the view always reflects what's persisted (including scope after a
// promote).
func (d *settingsDialog) refresh(ctx context.Context) {
	for ci := range d.cats {
		for ri := range d.cats[ci].rows {
			r := &d.cats[ci].rows[ri]
			if r.kind == rowKey {
				if d.s == nil || d.s.Svc == nil {
					continue
				}
				k, err := d.s.Svc.GetKey(ctx, prefs.ScopeGlobal, r.cred)
				if err == nil && k != "" {
					r.value, r.scope, r.isSet = k, prefs.ScopeGlobal, true
				} else {
					r.value, r.isSet = "", false
				}
				continue
			}
			if r.key == "" || d.s == nil || d.s.Svc == nil {
				continue // action rows have no backing setting
			}
			sv, err := d.s.Svc.GetSetting(ctx, prefs.ScopeEffective, r.key)
			if err == nil {
				r.value, r.scope, r.isSet = sv.Value, sv.Source, true
			} else {
				r.value, r.isSet = "", false
			}
		}
	}
	if d.providers != nil {
		d.providers.refresh() // keep the active marker + key badges in sync
	}
	if d.gallery != nil {
		d.gallery.refresh() // keep the gallery cursor on the persisted theme
	}
	if d.mcp != nil {
		d.mcp.refresh() // keep the server list in sync after add/delete/toggle
	}
}

// handleProviders routes keys to the inline providers panel; esc/left/tab
// return focus to the category nav unless the panel is mid-edit (where they
// cancel its sub-mode).
func (d *settingsDialog) handleProviders(msg tea.KeyPressMsg) action {
	if !d.providers.inSubMode() {
		switch msg.String() {
		case "ctrl+s":
			return actionClose{}
		case "esc", "q", "left", "h", "tab":
			d.focusRows = false
			return actionNone{}
		}
	}
	return d.providers.handleKey(msg)
}

// handleMCP routes keys to the inline MCP-server list; esc/left/tab return
// focus to the category nav unless the add form is open (where they cancel it).
func (d *settingsDialog) handleMCP(msg tea.KeyPressMsg) action {
	if !d.mcp.inSubMode() {
		switch msg.String() {
		case "ctrl+s":
			return actionClose{}
		case "esc", "q", "left", "h", "tab":
			d.focusRows = false
			return actionNone{}
		}
	}
	return d.mcp.handleKey(msg)
}

// handleGallery routes keys to the inline theme gallery; esc/tab return focus
// to the nav (reverting any live preview), enter commits.
func (d *settingsDialog) handleGallery(msg tea.KeyPressMsg) action {
	switch msg.String() {
	case "ctrl+s":
		d.gallery.leave()
		return actionClose{}
	case "esc", "q", "tab":
		reverted := d.gallery.isPreviewing()
		d.gallery.leave()
		if reverted {
			d.setStatus("theme preview reverted to " + d.gallery.origin)
		}
		d.focusRows = false
		return actionNone{}
	}
	if !d.gallery.focused {
		d.gallery.enter() // first interaction — capture the revert point
	}
	if d.gallery.handleKey(msg) {
		d.setStatus("theme kept: " + palette.Name)
		d.refresh(d.ctx)
	}
	return actionNone{}
}

// handleCatalog routes keys to a read-only inventory pane (agents / skills);
// tab/q return focus to the nav, left/esc collapse an open body drawer first
// and only then return to the nav, ctrl+s saves+closes.
func (d *settingsDialog) handleCatalog(p *catalogPane, msg tea.KeyPressMsg) action {
	if !p.inSubMode() {
		switch msg.String() {
		case "ctrl+s":
			return actionClose{}
		case "tab", "q":
			d.focusRows = false
			return actionNone{}
		case "esc", "left", "h":
			if p.expanded {
				p.expanded = false
			} else {
				d.focusRows = false
			}
			return actionNone{}
		}
	}
	return p.handleKey(msg)
}

// setStatus records a toast and timestamps it so the footer can age it out.
func (d *settingsDialog) setStatus(s string) {
	d.status, d.statusAt = s, time.Now()
}

func (d *settingsDialog) rows() []settingsRow { return d.cats[d.cat].rows }

func (d *settingsDialog) curRow() *settingsRow {
	rs := d.cats[d.cat].rows
	if d.row < 0 || d.row >= len(rs) {
		return &settingsRow{}
	}
	return &rs[d.row]
}

func (d *settingsDialog) handleKey(msg tea.KeyPressMsg) action {
	if d.editing {
		return d.handleEdit(msg)
	}
	// The Providers / Appearance categories own their detail panel: once
	// focused, delegate to it (esc/tab return to the nav).
	if d.focusRows && d.cats[d.cat].providers {
		return d.handleProviders(msg)
	}
	if d.focusRows && d.cats[d.cat].gallery {
		return d.handleGallery(msg)
	}
	if d.focusRows && d.cats[d.cat].catalog {
		return d.handleCatalog(d.catalogPane, msg)
	}
	if d.focusRows && d.cats[d.cat].mcp {
		return d.handleMCP(msg)
	}
	switch msg.String() {
	case "esc", "q":
		if d.focusRows {
			d.focusRows = false
			return actionNone{}
		}
		return actionClose{}
	case "ctrl+s":
		return actionClose{}
	case "tab":
		d.focusRows = !d.focusRows
	case "up", "k":
		if d.focusRows {
			if d.row > 0 {
				d.row--
			}
		} else if d.cat > 0 {
			d.cat--
			d.row = 0
		}
	case "down", "j":
		if d.focusRows {
			if d.row < len(d.rows())-1 {
				d.row++
			}
		} else if d.cat < len(d.cats)-1 {
			d.cat++
			d.row = 0
		}
	case "right", "l":
		if !d.focusRows {
			d.focusRows = true
			return actionNone{}
		}
		return d.activate(+1)
	case "left", "h":
		if !d.focusRows {
			return actionNone{}
		}
		if d.curRow().kind == rowEnum {
			return d.activateEnum(-1)
		}
		d.focusRows = false
		return actionNone{}
	case "enter", "space", " ":
		if !d.focusRows {
			d.focusRows = true
			return actionNone{}
		}
		return d.activate(+1)
	case "p":
		if d.focusRows {
			d.promote()
		}
	}
	return actionNone{}
}

// activate handles a key on the focused row: open a nested dialog (action
// row), edit a text row, cycle an enum (refetching models when the provider
// changes), or open the populated model picker.
func (d *settingsDialog) activate(dir int) action {
	r := d.curRow()
	switch r.kind {
	case rowAction:
		if r.open != nil {
			return actionPush{d: r.open(d.s)}
		}
	case rowText, rowKey:
		d.startEdit()
	case rowEnum:
		return d.activateEnum(dir)
	case rowModel:
		return d.activateModel()
	case rowAgent:
		return d.activateAgent()
	}
	return actionNone{}
}
