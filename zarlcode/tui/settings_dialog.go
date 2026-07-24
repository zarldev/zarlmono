package tui

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openaicodex"
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

	// models caches per-provider model lists fetched asynchronously;
	// modelsLoading marks a fetch in flight. Keyed by provider name.
	models        map[string][]string
	modelsLoading map[string]bool
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
	// agentsPane / skillsPane / hooksPane are the read-only inventory panels
	// rendered as the detail of the Agents / Skills / Hooks categories.
	agentsPane *catalogPane
	skillsPane *catalogPane
	hooksPane  *catalogPane
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
	// agents / skills / hooks mark the categories whose detail is a read-only
	// inventory panel (discovered agent / skill / hook definitions).
	agents bool
	skills bool
	hooks  bool
	// mcp marks the category whose detail is the editable MCP-server list.
	mcp bool
}

// settingsRow is one editable preference. The static fields (label, key,
// kind, def, opts, numeric) are fixed at construction; value/scope/isSet are
// refreshed from the store on open and after every mutation.
type settingsRow struct {
	label   string
	key     string
	kind    settingsRowKind
	def     string                        // shown (dim) when no row is set
	desc    string                        // one-line help shown in the detail panel
	opts    []string                      // rowEnum options
	numeric bool                          // validate as a non-negative integer before commit
	open    func(*engine.Settings) dialog // rowAction: builds the nested dialog

	value string
	scope prefs.Scope
	isSet bool
}

func newSettingsDialog(s *engine.Settings) *settingsDialog {
	return newSettingsDialogWithContext(context.Background(), s)
}

func newSettingsDialogWithContext(ctx context.Context, s *engine.Settings) *settingsDialog {
	if ctx == nil {
		ctx = context.Background()
	}
	d := &settingsDialog{
		ctx:           ctx,
		s:             s,
		models:        map[string][]string{},
		modelsLoading: map[string]bool{},
		providers:     newProvidersDialogWithContext(ctx, s),
		gallery:       newThemeGalleryWithContext(ctx, s),
		agentsPane:    newAgentsPane(s),
		skillsPane:    newSkillsPane(s),
		hooksPane:     newHooksPane(s),
		mcp:           newMCPPaneWithContext(ctx, s),
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
			{name: "agents", agents: true},
			{name: "skills", skills: true},
			{name: "hooks", hooks: true},
			{name: "budget", rows: []settingsRow{
				{label: "reserve tokens", key: prefs.KeyReserveTokens, kind: rowText, numeric: true, def: "512",
					desc: "headroom held back from the context window for the compactor."},
				{label: "max iterations", key: prefs.KeyMaxIterations, kind: rowText, numeric: true, def: "20",
					desc: "cap on the agent loop per turn before it must finalize."},
				{label: "response timeout", key: prefs.KeyResponseTimeout, kind: rowText, numeric: true, def: "90",
					desc: "seconds to wait with no output from the model before cancelling the iteration. raise it for slow local models/connections; non-positive falls back to 90."},
				{label: "spawn max iterations", key: prefs.KeySpawnMaxIterations, kind: rowText, numeric: true, def: "20",
					desc: "cap on sub-agent iterations per spawn_agent call. unset inherits the parent max."},
				{label: "spawn depth", key: prefs.KeySpawnMaxDepth, kind: rowText, numeric: true, def: "(unset)",
					desc: "how deep spawn_agent may recurse. unset uses the built-in default."},
				{label: "verify command", key: prefs.KeyVerifyTests, kind: rowText, def: "(off)",
					desc: "headless oracle: shell command (sh -c) whose zero exit means verified done; failures re-drive the agent."},
				{label: "verify attempts", key: prefs.KeyVerifyAttempts, kind: rowText, numeric: true, def: "1",
					desc: "headless verified re-drive attempt cap. 1 = single-shot; the loop arms at 2+ with a command set."},
				{label: "tool result max kb", key: prefs.KeyToolResultMaxKB, kind: rowText, numeric: true, def: "50",
					desc: "cap on a tool result (KB) before tail-truncation + spill to disk. lower it for small-context local models."},
				{label: "tool result max lines", key: prefs.KeyToolResultMaxLines, kind: rowText, numeric: true, def: "2000",
					desc: "line cap on a tool result before tail-truncation + spill to disk."},
				{label: "fanout cap", key: prefs.KeyFanoutCap, kind: rowText, numeric: true, def: "0",
					desc: "max calls per capped discovery tool (ls/grep/glob) per task. 0 keeps the built-in per-tool defaults; a positive value caps them uniformly."},
				{label: "spawn fanout cap", key: prefs.KeySpawnFanoutCap, kind: rowText, numeric: true, def: "8",
					desc: "max spawn_agent calls per task before the guardrail refuses more. bounds a model that keeps firing sub-agents. 0 removes the cap."},
			}},
			{name: "integrations", rows: []settingsRow{
				{label: "web search", key: prefs.KeySearxngURL, kind: rowText, def: engine.DefaultSearxngURL,
					desc: "searxng endpoint the web_search tool queries. empty uses the local default."},
				{label: "chrome path", key: prefs.KeyChromeBinPath, kind: rowText, def: "(auto-detect)",
					desc: "absolute path to a Chrome or Chromium binary for the web_fetch browser fallback. empty auto-detects."},
				{label: "editor", key: prefs.KeyEditor, kind: rowText, def: "(uses $EDITOR)",
					desc: "command to edit agents/skills (may carry flags, e.g. 'code -w'). empty falls back to $ZARLCODE_EDITOR / $VISUAL / $EDITOR, then vi."},
				{label: "web tools", key: prefs.KeyEnableWeb, kind: rowEnum, def: "on", opts: []string{"on", "off"},
					desc: "register web_search + web_fetch. off drops both from the tool surface for a leaner local-model setup."},
				{label: "programmatic tools", key: prefs.KeyProgrammaticTools, kind: rowEnum, def: "off", opts: []string{"off", "on"},
					desc: "replace direct read/search/catalogue tools with one program tool for bounded Starlark fan-out and aggregation."},
				{label: "local web_search service", kind: rowAction, def: "SearXNG",
					desc: "install/start the optional bundled SearXNG Docker Compose service for web_search. model servers stay external.",
					open: func(*engine.Settings) dialog { return newServiceDialog(ctx) }},
				{label: "mcp tools", key: prefs.KeyEnableMCP, kind: rowEnum, def: "on", opts: []string{"on", "off"},
					desc: "register the mcp_connect/disconnect/list tools. off drops MCP management from the tool surface."},
				{label: "pprof address", key: prefs.KeyPprofAddr, kind: rowText, def: "(off)",
					desc: "optional listen address for Go pprof + runtime metrics, e.g. 127.0.0.1:6060. applies on restart; CLI -pprof overrides."},
				{label: "trace file", key: prefs.KeyTraceFile, kind: rowText, def: "(off)",
					desc: "optional Go execution trace output path. applies on restart; CLI -trace overrides."},
			}},
			{name: "mcp", mcp: true},
			{name: "processes", rows: []settingsRow{
				{label: "max alive", key: prefs.KeyMaxAliveProcesses, kind: rowText, numeric: true, def: "16",
					desc: "cap on concurrent background bash processes. applies on restart."},
				{label: "output buffer", key: prefs.KeyProcessOutputBuffer, kind: rowText, numeric: true, def: "10000",
					desc: "lines of output retained per background process. applies on restart."},
				{label: "background processes", key: prefs.KeyEnableBackground, kind: rowEnum, def: "on", opts: []string{"on", "off"},
					desc: "enable bash background mode + the bash_output/stop_process/list_processes tools. off drops the trio and bash runs foreground-only."},
			}},
			{name: "guardrails", rows: []settingsRow{
				{label: "plan first", key: prefs.KeyPlanFirst, kind: rowEnum, def: "off", opts: []string{"off", "on"},
					desc: "require update_plan before the first workspace-changing call in a task. on makes planning mandatory (weak/local models); off lets the model dive straight in."},
				{label: "decompose judge", key: prefs.KeyDecomposeJudge, kind: rowEnum, def: "off", opts: []string{"off", "on"},
					desc: "llm verdict judge for repeatedly failing tool calls (grammar-constrained enum). off keeps the deterministic advisory."},
				{label: "judge provider", key: prefs.KeyJudgeProvider, kind: rowEnum, def: "(active)",
					desc: "provider for judge verdicts. (active) reuses the active provider — verdicts want a small fast model."},
				{label: "judge model", key: prefs.KeyJudgeModel, kind: rowModel, def: "(active)",
					desc: "model for judge verdicts, from the judge provider's list. (active) reuses the active model."},
				{label: "read before write", key: prefs.KeyReadBeforeWrite, kind: rowEnum, def: "off", opts: []string{"off", "advisory", "strict"},
					desc: "require the task to read the target file or nearby context before edit/write. advisory and strict both refuse blind edits; strict is the strongest local-model setting."},
				{label: "test edit guard", key: prefs.KeyTestEditGuard, kind: rowEnum, def: "off", opts: []string{"off", "advisory", "strict"},
					desc: "watch for edits to test files that would make a failing test pass without fixing the code. advisory warns; strict refuses. headless runs are always strict."},
				{label: "improvement loop", key: prefs.KeyImprovementGuard, kind: rowEnum, def: "on", opts: []string{"on", "off"},
					desc: "keep the agent working while its verifiers still report failure instead of stopping early. off removes the guardrail from the chain."},
				{label: "skill hints", key: prefs.KeySkillHints, kind: rowEnum, def: "on", opts: []string{"on", "off"},
					desc: "suggest a recovery skill after a tool call keeps failing. off removes the guardrail from the chain."},
				{label: "shell policy", key: prefs.KeyShellGuard, kind: rowEnum, def: "auto", opts: []string{"auto", "strict", "lenient", "off"},
					desc: "static shell-command guardrail leniency. auto follows the sandbox (strict when on, lenient when off); strict/lenient pin it regardless of the sandbox; off removes the guardrail from the chain entirely."},
			}},
			{name: "compaction", rows: []settingsRow{
				{label: "mode", key: prefs.KeyCompactionMode, kind: rowEnum, def: "auto", opts: []string{"auto", "manual"},
					desc: "auto trims history under context pressure automatically. manual leaves it intact, warns in the cockpit near the limit, and waits for you to compact on demand (conversation actions › compact)."},
				{label: "engine", key: prefs.KeyCompactEngine, kind: rowEnum, def: "tiered", opts: compactEngineOpts(),
					desc: "how chats are condensed: structural trims, tiered ramps, summary/executive use an llm. handover clears the whole context and reseeds from a handover document written to .zarlcode/handovers/."},
				{label: "provider", key: prefs.KeyCompactProvider, kind: rowEnum, def: "(active)",
					desc: "provider for llm compaction (summary/executive). (active) reuses the active provider."},
				{label: "model", key: prefs.KeyCompactModel, kind: rowModel, def: "(active)",
					desc: "model for llm compaction, from the compaction provider's list. (active) reuses the active model."},
			}},
			{name: "appearance", gallery: true, rows: []settingsRow{
				{label: "theme", key: prefs.KeyTheme, kind: rowEnum, def: palette.Name, opts: themeNames(),
					desc: "colour theme. move to preview live; enter to keep."},
			}},
			{name: "behavior", rows: []settingsRow{
				{label: "confirm quit", key: prefs.KeyConfirmQuit, kind: rowEnum, def: "on", opts: []string{"on", "off"},
					desc: "show a confirmation prompt before quitting via ctrl+c. turn off to quit instantly."},
				{label: "credential protection", key: prefs.KeyCredentialProtection, kind: rowEnum, def: prefs.CredentialProtectionOff, opts: []string{prefs.CredentialProtectionOff, prefs.CredentialProtectionPassphrase},
					desc: "off stores credentials plaintext in state.db. passphrase encrypts them and prompts on startup. toggling migrates stored keys."},
				{label: "sudo askpass", key: prefs.KeySudoAskpass, kind: rowEnum, def: "off", opts: []string{"off", "on"},
					desc: "enable sudo -A support for bash commands. when on, sudo requests show a password popup in the TUI. applies on restart."},
				{label: "sandbox", key: prefs.KeySandbox, kind: rowEnum, def: "on", opts: []string{"on", "off"},
					desc: "kernel-enforced filesystem confinement for bash commands. turn off only when a command needs host paths outside the workspace allow-list. applies on restart."},
			}},
		},
	}
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
			if r.key == "" || d.s == nil || d.s.Svc == nil {
				continue // action rows have no backing setting
			}
			sv, ok, err := d.s.Svc.GetSetting(ctx, prefs.ScopeEffective, r.key)
			if err == nil && ok {
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
		d.gallery.leave()
		d.focusRows = false
		return actionNone{}
	}
	if !d.gallery.focused {
		d.gallery.enter() // first interaction — capture the revert point
	}
	if d.gallery.handleKey(msg) {
		d.setStatus("theme → " + palette.Name)
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

func (d *settingsDialog) tabBar() string {
	parts := make([]string, len(d.cats))
	for i, c := range d.cats {
		if i == d.cat {
			parts[i] = palette.Primary.On("[ " + c.name + " ]")
		} else {
			parts[i] = palette.Subtle.On(c.name)
		}
	}
	return strings.Join(parts, "  ")
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
	if d.focusRows && d.cats[d.cat].agents {
		return d.handleCatalog(d.agentsPane, msg)
	}
	if d.focusRows && d.cats[d.cat].skills {
		return d.handleCatalog(d.skillsPane, msg)
	}
	if d.focusRows && d.cats[d.cat].hooks {
		return d.handleCatalog(d.hooksPane, msg)
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
	case rowText:
		d.startEdit()
	case rowEnum:
		return d.activateEnum(dir)
	case rowModel:
		return d.activateModel()
	}
	return actionNone{}
}

// activateEnum handles enter/←/→ on an enum row. Theme and codex-effort open a
// visible picker (so you choose from the full list with live preview instead
// of cycling blind); the rest cycle in place. dir is the cycle direction for
// the in-place case.
func (d *settingsDialog) activateEnum(dir int) action {
	r := d.curRow()
	switch r.key {
	case prefs.KeyTheme:
		return actionPush{d: newThemePickerFor(func(name string) { d.commit(prefs.KeyTheme, name) })}
	case prefs.KeyProvider:
		items := providerNames(d.s)
		sel := r.value
		if !r.isSet {
			sel = r.def
		}
		return actionPush{d: newListPicker("provider", items, sel, func(choice string) {
			d.commit(prefs.KeyProvider, choice)
			d.s.ResetModelToProviderDefault(d.ctx, choice)
			d.refresh(d.ctx)
			d.modelsLoading[choice] = true
			d.pendingFetch = choice
		})}
	case prefs.KeyCompactEngine:
		return actionPush{d: newListPicker("compaction engine", compactEngineOpts(), r.value, func(choice string) {
			d.commit(prefs.KeyCompactEngine, choice)
		})}
	case prefs.KeyCompactProvider:
		items := append([]string{compactActiveSentinel}, providerNames(d.s)...)
		sel := compactActiveSentinel
		if r.value != "" {
			sel = r.value
		}
		return actionPush{d: newListPicker("compaction provider", items, sel, func(choice string) {
			if choice == compactActiveSentinel {
				d.commit(prefs.KeyCompactProvider, "") // reuse the active provider
			} else {
				d.commit(prefs.KeyCompactProvider, choice)
			}
			// Queue a model fetch for the new compaction provider so its model
			// picker is populated (a picker closure can't return a fetch).
			if p := d.compactProvider(); p != "" {
				if _, ok := d.models[p]; !ok && !d.modelsLoading[p] {
					d.modelsLoading[p] = true
					d.pendingFetch = p
				}
			}
		})}
	case prefs.KeyJudgeProvider:
		items := append([]string{compactActiveSentinel}, providerNames(d.s)...)
		sel := compactActiveSentinel
		if r.value != "" {
			sel = r.value
		}
		return actionPush{d: newListPicker("judge provider", items, sel, func(choice string) {
			if choice == compactActiveSentinel {
				d.commit(prefs.KeyJudgeProvider, "") // reuse the active provider
			} else {
				d.commit(prefs.KeyJudgeProvider, choice)
			}
			// Queue a model fetch for the new judge provider so its model
			// picker is populated (a picker closure can't return a fetch).
			if p := d.judgeProvider(); p != "" {
				if _, ok := d.models[p]; !ok && !d.modelsLoading[p] {
					d.modelsLoading[p] = true
					d.pendingFetch = p
				}
			}
		})}
	case prefs.KeyCodexEffort:
		items := d.codexEffortItems()
		sel := codexEffortAuto
		if r.value != "" {
			sel = r.value
		}
		return actionPush{d: newListPicker("reasoning effort", items, sel, func(choice string) {
			val := choice
			if choice == codexEffortAuto {
				val = ""
			}
			d.commit(prefs.KeyCodexEffort, val)
		})}
	default:
		d.cycleEnum(dir)
		if r.key == prefs.KeyProvider {
			return d.onProviderCycled()
		}
	}
	return actionNone{}
}

// onProviderCycled runs after the provider enum changes: reset the model to
// the new provider's default (so we never strand a cross-provider model like
// deepseek + opus), refresh the rows, and request the new model list.
func (d *settingsDialog) onProviderCycled() action {
	d.s.ResetModelToProviderDefault(d.ctx, d.currentProvider())
	d.refresh(d.ctx)
	return d.fetchForCurrentProvider()
}

// startEdit opens the inline editor on the current row, prefilled with its
// current value.
func (d *settingsDialog) startEdit() {
	r := d.curRow()
	d.editing = true
	d.editor = composer{}
	if r.isSet {
		d.editor.insert(r.value)
	}
}

// --- model list (per-provider, async-fetched) ---

// currentProvider is the effective provider the model picker fetches for:
// the provider row's set value, else its default.
func (d *settingsDialog) currentProvider() string {
	for _, c := range d.cats {
		for _, r := range c.rows {
			if r.key == prefs.KeyProvider {
				if r.isSet && r.value != "" {
					return r.value
				}
				return r.def
			}
		}
	}
	return ""
}

// compactProvider is the provider the compaction model picker fetches for:
// the compact_provider row's value, else the active provider (the engine
// reuses the active backend when no override is set).
func (d *settingsDialog) compactProvider() string {
	for _, c := range d.cats {
		for _, r := range c.rows {
			if r.key == prefs.KeyCompactProvider && r.isSet && r.value != "" {
				return r.value
			}
		}
	}
	return d.currentProvider()
}

// judgeProvider is the provider the judge model picker fetches for: the
// judge_provider row's value, else the active provider (the engine reuses the
// active backend when no override is set).
func (d *settingsDialog) judgeProvider() string {
	for _, c := range d.cats {
		for _, r := range c.rows {
			if r.key == prefs.KeyJudgeProvider && r.isSet && r.value != "" {
				return r.value
			}
		}
	}
	return d.currentProvider()
}

// providerForRow is the provider a model row's picker + hint resolve against:
// the compaction / judge model rows track their own provider rows, the main
// model row the active one.
func (d *settingsDialog) providerForRow(key string) string {
	switch key {
	case prefs.KeyCompactModel:
		return d.compactProvider()
	case prefs.KeyJudgeModel:
		return d.judgeProvider()
	}
	return d.currentProvider()
}

func (d *settingsDialog) activeModel() string {
	for _, c := range d.cats {
		for _, r := range c.rows {
			if r.key == prefs.KeyModel {
				if r.isSet && r.value != "" {
					return r.value
				}
				return r.def
			}
		}
	}
	return ""
}

func (d *settingsDialog) codexEffortItems() []string {
	items := []string{codexEffortAuto}
	if d.currentProvider() != backends.NameOpenAICodex.String() {
		return append(items, "low", "medium", "high", "xhigh", "max")
	}
	if variants := openaicodex.EffortVariants(d.activeModel()); len(variants) > 0 {
		return append(items, variants...)
	}
	return append(items, "low", "medium", "high", "xhigh", "max")
}

// takePendingFetch returns and clears any queued model-fetch provider.
func (d *settingsDialog) takePendingFetch() string {
	p := d.pendingFetch
	d.pendingFetch = ""
	return p
}

// fetchForCurrentProvider requests a model fetch for the active provider.
func (d *settingsDialog) fetchForCurrentProvider() action { return d.fetchFor(d.currentProvider()) }

// fetchFor requests a model fetch for provider unless it's already cached or
// in flight. Returns the push-to-root intent.
func (d *settingsDialog) fetchFor(p string) action {
	if p == "" {
		return actionNone{}
	}
	if _, ok := d.models[p]; ok || d.modelsLoading[p] {
		return actionNone{}
	}
	d.modelsLoading[p] = true
	return actionFetchModels{provider: p}
}

// onModelsLoaded records a completed fetch so the model picker can present
// the list.
func (d *settingsDialog) onModelsLoaded(provider string, models []string, err error) {
	if d.providers != nil && d.providers.onModelsLoaded(provider, models, err) {
		return
	}
	d.modelsLoading[provider] = false
	if err != nil && len(models) == 0 {
		d.setStatus("models: " + err.Error())
		return
	}
	d.models[provider] = models
	if provider == d.currentProvider() && len(models) > 0 {
		d.setStatus(fmt.Sprintf("%d models for %s", len(models), provider))
	}
}

// activateModel opens a picker over the fetched model list for the row's
// provider (the active one for the main model, the compaction provider for
// the compaction model), plus a custom-entry escape — and, for the compaction
// model, an "(active)" entry that clears the override. With nothing fetched it
// kicks a fetch and falls back to free-text entry.
func (d *settingsDialog) activateModel() action {
	key := d.curRow().key
	p := d.providerForRow(key)
	opts := d.models[p]
	if len(opts) == 0 {
		d.startEdit()
		return d.fetchFor(p) // populate for next time
	}
	items := make([]string, 0, len(opts)+2)
	if key == prefs.KeyCompactModel || key == prefs.KeyJudgeModel {
		items = append(items, compactActiveSentinel)
	}
	items = append(items, modelCustomSentinel)
	items = append(items, opts...)
	meta := newModelInfoResolver(d.s)
	right := func(item string) string {
		if item == modelCustomSentinel || item == compactActiveSentinel {
			return ""
		}
		return meta.summary(p, item)
	}
	return actionPush{d: newListPickerWithRight("models · "+p, items, d.curRow().value, func(choice string) {
		switch choice {
		case modelCustomSentinel:
			d.startEdit()
		case compactActiveSentinel:
			d.commit(key, "") // reuse the active model
		default:
			d.commit(key, choice)
		}
	}, right)}
}

// modelHintFor is the trailing badge on a model row: the fetch state or the
// fetched count for the row's provider. Bare (no leading pad) so it joins into
// the badge column via joinBadges.
func (d *settingsDialog) modelHintFor(provider string) string {
	if d.modelsLoading[provider] {
		return palette.Warning.On("fetching…")
	}
	if n := len(d.models[provider]); n > 0 {
		return palette.Subtle.On("(" + strconv.Itoa(n) + " models)")
	}
	return ""
}

func (d *settingsDialog) cycleEnum(dir int) {
	r := d.curRow()
	if r.kind != rowEnum || len(r.opts) == 0 {
		return
	}
	cur := r.value
	if !r.isSet {
		cur = r.def
	}
	idx := indexOf(r.opts, cur)
	if idx < 0 {
		idx = 0
	} else {
		idx = (idx + dir + len(r.opts)) % len(r.opts)
	}
	val := r.opts[idx]
	d.commit(r.key, val)
	if r.key == prefs.KeyTheme && val != "" {
		if t, ok := theme.ByName(val); ok {
			UseTheme(t) // live preview
		}
	}
}

// handlePaste feeds clipboard content to whichever text-entry sub-mode is
// open: the inline row editor, or the focused detail panel (providers
// key/add form, mcp add form). Every settings field is single-line, so
// embedded newlines are stripped — a pasted API key carrying a trailing
// newline lands clean. No-op when nothing is in a text-entry state.
func (d *settingsDialog) handlePaste(content string) {
	content = stripNewlines(content)
	if content == "" {
		return
	}
	switch {
	case d.editing:
		d.editor.insert(content)
	case d.focusRows && d.cats[d.cat].providers:
		d.providers.handlePaste(content)
	case d.focusRows && d.cats[d.cat].mcp:
		d.mcp.handlePaste(content)
	}
}

// stripNewlines removes CR/LF so pasted clipboard content stays single-line
// for the settings fields (API keys, urls, model ids).
func stripNewlines(s string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(s)
}

func (d *settingsDialog) handleEdit(msg tea.KeyPressMsg) action {
	switch msg.String() {
	case "esc":
		d.editing = false
	case "enter":
		val := d.editor.submit()
		if d.curRow().numeric && val != "" {
			if n, err := strconv.Atoi(val); err != nil || n < 0 {
				d.setStatus(d.curRow().label + ": want a non-negative integer")
				d.editing = false
				return actionNone{}
			}
		}
		d.commit(d.curRow().key, val)
		d.editing = false
	case "backspace":
		d.editor.backspace()
	case "left":
		d.editor.left()
	case "right":
		d.editor.right()
	default:
		if msg.Text != "" {
			d.editor.insert(msg.Text)
		}
	}
	return actionNone{}
}

// commit persists val at workspace scope (or clears the row when empty),
// records a status badge, and refreshes the view.
func (d *settingsDialog) commit(key, val string) {
	if d.s == nil || d.s.Svc == nil {
		return
	}
	ctx := d.ctx
	if key == prefs.KeyCredentialProtection {
		switch val {
		case prefs.CredentialProtectionOff:
			if n, err := d.s.Svc.DisableCredentialProtection(ctx, nil); err != nil {
				d.setStatus("credential protection: " + err.Error())
			} else {
				d.setStatus(fmt.Sprintf("credential protection off — %d key(s) plaintext", n))
			}
		case prefs.CredentialProtectionPassphrase:
			if !d.s.Svc.HasVault() {
				d.setStatus("enable with: zarlcode keys protect on")
				return
			}
			if n, err := d.s.Svc.EnableCredentialProtection(ctx, nil); err != nil {
				d.setStatus("credential protection: " + err.Error())
			} else {
				d.setStatus(fmt.Sprintf("credential protection enabled — %d key(s) encrypted", n))
			}
		default:
			d.setStatus("credential protection: invalid value " + val)
		}
		d.refresh(ctx)
		return
	}
	switch val {
	case "":
		if err := d.s.Svc.DeleteSetting(ctx, prefs.ScopeWorkspace, key); err != nil {
			d.setStatus("error: " + err.Error())
		} else {
			d.setStatus(key + " cleared")
		}
	default:
		if err := d.s.Svc.SetSetting(ctx, prefs.ScopeWorkspace, key, val); err != nil {
			d.setStatus("error: " + err.Error())
		} else {
			d.setStatus(key + " → " + val + " (workspace)")
		}
	}
	d.refresh(ctx)
}

func (d *settingsDialog) promote() {
	if d.s == nil || d.s.Svc == nil {
		return
	}
	r := d.curRow()
	if !r.isSet || r.scope != prefs.ScopeWorkspace {
		d.setStatus("nothing to promote (already global / unset)")
		return
	}
	ctx := d.ctx
	if err := d.s.Svc.PromoteSetting(ctx, r.key); err != nil {
		d.setStatus("promote: " + err.Error())
	} else {
		d.setStatus(r.key + " promoted to global")
	}
	d.refresh(ctx)
}

func indexOf(ss []string, v string) int {
	for i, s := range ss {
		if s == v {
			return i
		}
	}
	return -1
}
