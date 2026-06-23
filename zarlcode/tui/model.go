// Package tui is the agent TUI: bubbletea v2 + ultraviolet, built
// around the code runner (zkit/agent/runner). It is a single tea.Model
// that paints panes onto a uv.ScreenBuffer via Draw(scr, area); panes
// are imperative structs the root draws directly, with no per-pane
// Update loop. Runner events arrive as tea messages via the teasink
// event spine and land in the run timeline.
package tui

import (
	"context"
	"log/slog"
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/prefs"
)

// UI is the sole bubbletea v2 model. Per-pane state lives in imperative
// sub-structs the root drives directly; they do not implement tea.Model.
type UI struct {
	width    int
	height   int
	layout   uiLayout
	timeline *timeline
	composer composer
	// inputHistory is the in-memory prompt history for the editor pane. historyPos
	// is an index into inputHistory while browsing, or len(inputHistory) when the
	// user is editing a fresh draft; historyDraft preserves that fresh draft while
	// Up/Down navigates prior submissions.
	inputHistory []string
	historyPos   int
	historyDraft string
	overlay      overlay
	// intro is the full-screen fresh-start surface: first prompt plus a picker
	// for saved sessions in this workspace. Nil after the user starts/resumes.
	intro *introPane
	// dashboardScroll is the vertical offset inside the expanded context view.
	dashboardScroll int
	// frame counts animation ticks; ticking guards a single in-flight tick
	// loop. Both drive the streaming pulse on the cockpit gauge while a run
	// is live; they idle (no ticks scheduled) when nothing is running.
	frame   int
	ticking bool
	// runFn launches a live run for a submitted prompt; nil means the
	// composer just echoes prompts locally (no runner wired).
	runFn func(prompt string) tea.Cmd

	// settings is the persistence handle (prefs + provider registry) the
	// settings overlay reads and writes. nil disables the overlay.
	settings *engine.Settings
	// live is the run target; held so a provider change in the settings
	// overlay can re-point it (SetProvider) without a restart.
	live *engine.LiveRunner
	// appliedReasoning / appliedWindow are the build-affecting definition
	// fields the live provider was last built with (reasoning policy and the
	// declared context window). They aren't part of ProviderSpec, so
	// maybeRepoint compares them to detect an edit and rebuild.
	appliedReasoning llm.ReasoningHistory
	appliedWindow    int
	// widthMethod is how we measure grapheme width when laying out the
	// canvas. It mirrors bubbletea's renderer: WcWidth by default, switched
	// to GraphemeWidth once the terminal reports mode 2027 (Unicode core)
	// support. Our canvas must match the renderer's method or emoji-width
	// disagreements shift one pane row into its neighbour.
	widthMethod ansi.Method

	// session holds the shared mutable state that runner events update and
	// panes read. Set during Create; nil only in tests that call New() directly.
	session *Session

	// askpass serves sudo -A password requests from bash subprocesses. Nil when
	// the sudo_askpass setting is off.
	askpass *askpassServer

	// prRefreshPending marks that a git/gh tool ran during the live turn, so the
	// PR card is re-fetched when the turn ends rather than per tool call.
	prRefreshPending bool

	// Panes — imperative rendering regions driven by the shell.
	headerPane *headerPane
	statusPane *statusPane
	// startupFailure is a full-screen fatal launch surface shown when zarlcode
	// can initialize the shell UI but cannot finish startup (for example an
	// invalid workspace-scoped provider config). Non-nil disables the normal
	// cockpit and exits on enter/esc.
	startupFailure *startupFailurePane

}

// SetRunFn wires the live-run launcher invoked when the user submits a
// prompt. The standalone cmd sets this after building the runner factory.
func (m *UI) SetRunFn(fn func(prompt string) tea.Cmd) { m.runFn = fn }


func (m *UI) cancelLiveTurnForQuit() {
	if m.live != nil {
		m.live.CancelTurn()
	}
}

func (m *UI) SetStartupFailure(wsRoot, title, err string) {
	m.startupFailure = newStartupFailurePane(shortenHome(wsRoot), title, err)
}

func (m *UI) appContext() context.Context {
	if m != nil && m.live != nil {
		return m.live.ParentContext()
	}
	return context.Background()
}

// SetLiveRunner wires the live runner as the prompt handler AND keeps a
// reference so a provider change in the settings overlay can re-point it
// mid-session (see maybeRepoint).
func (m *UI) SetLiveRunner(l *engine.LiveRunner) {
	m.live = l
	m.runFn = func(prompt string) tea.Cmd { return RunFn(l, prompt) }
}

// SetProviderContext records the env-derived fallback spec and the currently
// active spec, so the settings overlay can detect a provider change and
// re-point the live runner on close.
func (m *UI) SetProviderContext(fallback, current engine.ProviderSpec) {
	m.session.SetProviderContext(fallback, current)
}

// SetProvider records the active provider name (e.g. "llamacpp", "anthropic")
// for the cockpit's identity section, and whether it's a local/unmetered
// backend (drives the COST label). Empty hides the provider row.
func (m *UI) SetProvider(name string) {
	m.session.SetProviderDisplay(name)
}

// togglePlan flips PLAN mode: the live runner switches to a read-only tool
// surface + planning prompt, and the UI repaints in the PlanMode tint. The
// flag is read live by the runner, so toggling mid-turn gates the next tool.
func (m *UI) togglePlan() {
	planMode := m.session.TogglePlanMode()
	if m.live != nil {
		m.live.SetPlanMode(planMode)
	}
}

// SetWorkspace sets the workspace path (~-shortened), git branch (if any), and
// model name. The workspace/branch render in the state sidebar; the model also
// appears in the timeline title. Resolving the model name seeds the cockpit's
// best-effort token pricing (overridable via SetPricing).
func (m *UI) SetWorkspace(root, model string) {
	m.session.SetWorkspace(root, model)
}

// SetPricing overrides the cockpit's per-1k (input, output) USD token price.
// Use it when the exact rate is known and the name-matched default is wrong
// or missing.
func (m *UI) SetPricing(inPer1k, outPer1k float64) {
	m.session.SetPricing(inPer1k, outPer1k)
}

// setModel updates the active model name for the current provider and
// repoints the live runner so the change takes effect on the next turn.
func (m *UI) setModel(name string) {
	m.session.SetActiveModel(name)
	if m.live != nil {
		m.live.SetModel(name)
	}
	m.session.ApplyModelPricing(name)
	m.session.SetSuccessToast("model → " + name)
}

// openModelQuickPick pushes the model list picker onto the overlay with a
// provider tab bar and model list. Cached models are pre-seeded for instant
// open. Provider switching is handled automatically: if the user selects a
// model from a different provider, the selection is persisted to settings
// and the close action triggers maybeRepoint to rebuild the provider.
// Model-only changes on the active provider are applied directly via
// setModel so the live runner picks them up immediately.
func (m *UI) openModelQuickPick() tea.Cmd {
	current := m.session.ActiveProviderSpec()
	var provNames []string
	if m.settings != nil && m.settings.Registry != nil {
		for _, def := range m.settings.Registry.All() {
			provNames = append(provNames, def.Name)
		}
	}
	if len(provNames) == 0 {
		provNames = []string{current.Name}
	}

	cache := make(map[string][]string, len(m.session.ModelCache))
	for k, v := range m.session.ModelCache {
		cache[k] = v
	}

	picker := newModelQuickPick(provNames, cache, current.Name, m.session.Model, func(prov, model string) {
		active := m.session.ActiveProviderSpec()
		// Persist provider + model to settings so the change survives
		// restart and maybeRepoint can read it back as the new spec.
		if m.settings != nil && m.settings.Svc != nil {
			ctx := m.appContext()
			if prov != active.Name {
				if err := m.settings.Svc.SetSetting(ctx, prefs.ScopeWorkspace, prefs.KeyProvider, prov); err != nil {
					slog.WarnContext(ctx, "persist provider switch", "err", err, "provider", prov)
				}
				// Reset to the new provider's default so a cross-provider
				// model (e.g. deepseek + claude-opus) can't be stranded.
				// The user's explicit pick below overwrites it.
				m.settings.ResetModelToProviderDefault(ctx, prov)
			}
			if err := m.settings.Svc.SetSetting(ctx, prefs.ScopeWorkspace, prefs.KeyModel, model); err != nil {
				slog.WarnContext(ctx, "persist model switch", "err", err, "model", model)
			}
		}

		if prov != active.Name {
			// Provider changed: persist is done above. Don't update the
			// active Session provider spec here — let maybeRepoint detect
			// the diff and rebuild the llm.Provider from persisted settings.
			m.session.SetToast("switching to " + prov + " / " + model + "…")
		} else {
			// Same provider, only model changed: apply directly via setModel
			// so the live runner picks it up on the very next turn.
			// setModel also updates Session.ProvSpec.Model so maybeRepoint is a no-op.
			m.setModel(model)
		}
	})
	m.overlay.push(picker)

	if _, ok := m.session.ModelCache[current.Name]; !ok {
		return m.fetchModelsCmd(current.Name)
	}
	return nil
}

// SetSettings wires the persistence handle so the settings overlay (ctrl+s)
// can read and write preferences. Nil leaves the overlay unavailable.
// Also resolves the confirm_quit setting.
func (m *UI) SetSettings(s *engine.Settings) {
	m.settings = s
	m.session.SetConfirmQuit(s.ConfirmQuit(m.appContext()))
	if s != nil && s.Registry != nil {
		m.session.SetModelMeta(s.Registry)
	}
}

// handleQuit returns a quit command, optionally showing a confirmation
// dialog first when confirm_quit is enabled.
func (m *UI) handleQuit() tea.Cmd {
	if m.session.ConfirmQuit {
		m.overlay.push(newQuitConfirmDialog())
		return nil
	}
	m.cancelLiveTurnForQuit()
	return tea.Quit
}

// SetContextWindow overrides the cockpit gauge's denominator (the model's
// usable context window, in tokens). Defaults to the live runner's window.
func (m *UI) SetContextWindow(tokens int) {
	m.session.SetContextWindow(tokens)
}

// SetPressureConfig records the compaction pressure threshold (window - reserve)
// so the context bar can draw a marker at the trigger point and the headline can
// show "compact at ~Xk". Pass window=0 to hide the pressure indicator.
func (m *UI) SetPressureConfig(window, reserve int) {
	m.session.SetPressureConfig(window, reserve)
}

// New constructs the v2 UI model. The cockpit gauge defaults to the live
// runner's context window so the fill fraction is meaningful from the first
// turn even before the consumer overrides it.
func New() *UI {
	s := NewSession("", "", "")
	m := &UI{
		timeline:   newTimeline(),
		session:    s,
		headerPane: newHeaderPane(s),
		statusPane: newStatusPane(s),
	}
	s.Run = RunState{window: engine.LiveContextWindow}
	return m
}

// Init implements tea.Model. bubbletea sends an initial WindowSizeMsg on
// start; the TUI has nothing else to schedule until then.
func (m *UI) Init() tea.Cmd {
	return m.fetchPRCmd()
}

// Update implements tea.Model. Resize recomputes the layout rects; esc stops
// a running turn while ctrl+c handles quitting. Runner events (teasink
// messages) are handled here too.
func (m *UI) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(frameMsg); ok {
		m.frame++
		if m.session.Run.Running {
			return m, tick() // keep breathing while the turn is live
		}
		m.ticking = false // run ended — let the loop die
		return m, nil
	}
	if _, ok := msg.(settingsTickMsg); ok {
		// A slow heartbeat that runs only while the full-screen settings
		// surface is open, purely so an idle footer toast ages out on time.
		if m.overlay.coversScreen() {
			return m, settingsTick()
		}
		return m, nil
	}
	if _, ok := msg.(mainToastMsg); ok {
		return m, nil // redraw so the status-bar toast ages out
	}
	if ok, cmd := m.handleRunnerMsg(msg); ok {
		// Kick a single animation loop the moment a run goes live; the loop
		// re-schedules itself via frameMsg and stops when running clears.
		if m.session.Run.Running && !m.ticking {
			m.ticking = true
			if cmd != nil {
				return m, tea.Batch(cmd, tick())
			}
			return m, tick()
		}
		return m, cmd
	}
	switch msg := msg.(type) {
	case liveTurnFinishedMsg:
		return m, m.saveSessionCmd()
	case askpassPromptMsg:
		m.overlay.push(newAskpassDialog(msg.Prompt, msg.Reply))
		return m, nil
	case processKillResultMsg:
		return m, m.handleProcessKillResult(msg)
	case sessionSaveFailedMsg:
		m.session.SetErrorToast("session save: " + msg.Error)
		return m, m.toastExpiryCmd()
	case sessionClearFailedMsg:
		m.session.SetErrorToast("clear: " + msg.Error)
		return m, m.toastExpiryCmd()
	}
	if m.handleOAuthMsg(msg) {
		return m, nil
	}
	if m.handleCatalogEditMsg(msg) {
		return m, m.toastExpiryCmd()
	}
	if m.handleModelsMsg(msg) {
		return m, nil
	}
	if m.handlePRMsg(msg) {
		return m, nil
	}
	if m.handleMouse(msg) { // wheel scroll + scrollbar click on the transcript
		return m, nil
	}
	if m.handleRepointMsg(msg) {
		return m, m.toastExpiryCmd()
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.recomputeLayout()
	case tea.ModeReportMsg:
		// Mirror bubbletea's renderer: when the terminal reports mode 2027
		// (Unicode core) support, both it and the renderer use grapheme
		// width, so our canvas must too (cf. tea.go eventLoop).
		if msg.Mode == ansi.ModeUnicodeCore {
			switch msg.Value {
			case ansi.ModeReset, ansi.ModeSet, ansi.ModePermanentlySet:
				m.widthMethod = ansi.GraphemeWidth
			}
		}
	case tea.KeyPressMsg:
		cmd := m.handleKey(msg)
		m.recomputeLayout()
		return m, cmd
	case tea.PasteMsg:
		m.handlePaste(msg.Content)
		m.recomputeLayout()
	case tea.ClipboardMsg:
		m.handlePaste(msg.Content)
		m.recomputeLayout()
	}
	return m, nil
}

func (m *UI) recomputeLayout() {
	if m.width <= 0 || m.height <= 0 {
		m.layout = uiLayout{}
		return
	}
	m.layout = computeLayoutWithEditorLines(m.width, m.height, m.composer.displayLineCount(m.width))
}

// View implements tea.Model. It allocates a screen buffer, paints the
// pane rects into it via Draw, and hands the flattened content back to
// bubbletea. Alt-screen and mouse mode are set on the View — bubbletea
// v2 has no WithAltScreen program option; per-View fields replace it.
func (m *UI) View() tea.View {
	var v tea.View
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.WindowTitle = appDisplayName
	if m.width <= 0 || m.height <= 0 {
		return v
	}
	canvas := uv.NewScreenBuffer(m.width, m.height)
	canvas.Method = m.widthMethod // match the renderer's negotiated width method
	m.Draw(canvas, canvas.Bounds())
	v.Content = strings.ReplaceAll(canvas.Render(), "\r\n", "\n")
	return v
}
