package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// Session holds the shared mutable state that runner events update and
// multiple panes read. It replaces the 15+ scattered fields on the old UI
// struct, giving each pane a single source of truth for workspace identity,
// run progress, plan state, and user preferences.
//
// The shell owns the Session; panes receive a pointer in Draw so they can
// read live state without holding their own copies. Runner events flow through
// handleRunnerMsg, which delegates state changes to Session mutation methods;
// panes then re-render on the next frame from the updated session.
type Session struct {
	// Workspace identity.
	Workspace    string // ~-shortened display path
	WorkspaceDir string // real path for file operations
	Branch       string // git branch, or ""

	// Active provider/model display state.
	Provider string
	Model    string
	// meta resolves the active model's per-token cost, provider class, and
	// capabilities from the registry. nil in tests built without settings —
	// the cost basis then stays at its defaults.
	meta modelMeta
	// Provider specs for repoint detection after settings change.
	ProvFallback engine.ProviderSpec // env-derived default
	ProvSpec     engine.ProviderSpec // currently-pointed selection
	// Model list cache — populated by async fetch, read by quick pick and
	// settings dialog. Keyed by provider name.
	ModelCache map[string][]string

	// Persisted session identity.
	StartedAt time.Time // when this TUI session launched
	ID        string    // persisted session id, empty until first save
	Label     string
	CreatedAt time.Time

	// Runtime modes and telemetry.
	PlanMode        bool // PLAN mode (shift+tab): read-only tools, planning prompt
	CockpitExpanded bool // full-width dashboard (ctrl+l)
	Run             RunState

	// User preferences loaded from the settings store.
	ConfirmQuit bool

	// Transient notification — shown at the right of the status bar.
	Toast     string
	ToastTone toastTone
	ToastAt   time.Time

	// Durable-ish per-session working state.

	// Plan is the latest structured plan from update_plan. The plan overlay
	// (ctrl+p) and transcript plan notices read this.
	Plan code.Plan

	// WorkingSet records file mutations observed during this TUI session.
	WorkingSet *WorkingSet

	// Checkpoints records per-turn pre-images for changed files so a later UI can
	// offer rollback without asking the runner to own TUI-shaped state.
	Checkpoints *Checkpoints

	// EventLog holds a ring of the most recent runner events for inspector
	// debugging.
	EventLog *EventRing

	// Event-derived state (populated by runner-event Session methods, read by panes).

	// LastToolResult holds the most recent tool's Result (any) for rich
	// rendering by the timeline pane.
	LastToolResult any

	// LastToolEffects holds the most recent tool's typed Effects, replacing
	// the old firstEffectSummary string flattening.
	LastToolEffects []tools.Effect

	// LastParentToolCallID is the ParentToolCallID from the most recent
	// ConversationStarted/Completed, linking sub-agents to their spawn call.
	LastParentToolCallID string

	// LastAgentName is the AgentName from ConversationStarted.
	LastAgentName string

	// Transactional event state (cleared per-turn).

	// PendingSkillNames tracks load_skill ToolStartedMsg parameters by ToolID
	// so skill names can be surfaced when the tool completes.
	PendingSkillNames map[string]string

	// SkipStartedPrompt suppresses the ConversationStarted user row for a
	// queued input already rendered while the previous turn was live.
	SkipStartedPrompt string
}

type toastTone int

const (
	toastInfo toastTone = iota
	toastSuccess
	toastError
)

// NewSession returns a Session with sensible defaults.
func NewSession(workspace, workspaceDir, branch string) *Session {
	return &Session{
		Workspace:    workspace,
		WorkspaceDir: workspaceDir,
		Branch:       branch,
		StartedAt:    time.Now(),
		WorkingSet:   NewWorkingSet(workspaceDir),
		Checkpoints:  NewCheckpoints(workspaceDir),
		EventLog:     NewEventRing(64),
		ModelCache:   make(map[string][]string),
	}
}

// SetToast records a status-bar notification with an expiry timestamp. The tone
// is inferred for existing callers so legacy "✓ ..." / "✗ ..." messages still
// render with semantic colours.
func (s *Session) SetToast(msg string) {
	s.SetToastTone(msg, inferToastTone(msg))
}

func (s *Session) SetSuccessToast(msg string) {
	s.SetToastTone(ensureToastPrefix(msg, "✓"), toastSuccess)
}

func (s *Session) SetErrorToast(msg string) {
	s.SetToastTone(ensureToastPrefix(msg, "✗"), toastError)
}

func (s *Session) SetToastTone(msg string, tone toastTone) {
	s.Toast = msg
	s.ToastTone = tone
	s.ToastAt = time.Now()
}

func (s *Session) SetCockpitExpanded(expanded bool) {
	s.CockpitExpanded = expanded
}

func (s *Session) TogglePlanMode() bool {
	s.PlanMode = !s.PlanMode
	return s.PlanMode
}

func (s *Session) SetSkipStartedPrompt(prompt string) {
	s.SkipStartedPrompt = prompt
}

func (s *Session) SetConfirmQuit(confirm bool) {
	s.ConfirmQuit = confirm
}

func (s *Session) SetWorkspace(root, model string) {
	s.Workspace = shortenHome(root)
	s.WorkspaceDir = root
	s.workingSet().SetWorkspaceDir(root)
	s.checkpoints().SetWorkspaceDir(root)
	s.Branch = gitBranch(root)
	s.Model = model
	s.ApplyModelPricing(model)
}

func (s *Session) CacheModels(provider string, models []string) {
	if s.ModelCache == nil {
		s.ModelCache = make(map[string][]string)
	}
	s.ModelCache[provider] = append([]string(nil), models...)
}

func (s *Session) ClearIdentity() {
	s.ID = ""
	s.Label = ""
	s.CreatedAt = time.Time{}
}

func (s *Session) SetIdentity(id, label string, createdAt time.Time) {
	s.ID = id
	s.Label = label
	s.CreatedAt = createdAt
}

func (s *Session) EnsureIdentity(id string, now time.Time) {
	if s.ID == "" {
		s.ID = id
		s.CreatedAt = now
	}
	if s.Label == "" {
		s.Label = s.CreatedAt.Format("2006-01-02 15:04")
	}
}

func (s *Session) SetProviderContext(fallback, current engine.ProviderSpec) {
	s.ProvFallback = fallback
	s.ProvSpec = current
}

func (s *Session) ProviderContext() (engine.ProviderSpec, engine.ProviderSpec) {
	return s.ProvFallback, s.ProvSpec
}

func (s *Session) ActiveProviderSpec() engine.ProviderSpec {
	return s.ProvSpec
}

func (s *Session) SetActiveProviderSpec(spec engine.ProviderSpec) {
	s.ProvSpec = spec
	s.Provider = spec.Name
	s.Model = spec.Model
}

func (s *Session) SetActiveModel(model string) {
	s.Model = model
	s.ProvSpec.Model = model
}

// modelMeta is the slice of the provider registry the Session needs to
// resolve a model's cost basis and provider class. Satisfied by
// *backends.ProviderRegistry. Defined consumer-side so the cockpit stays
// decoupled from the registry's full surface.
type modelMeta interface {
	// Cost returns the per-1k USD (input, output) rate; ok=false when the
	// backend isn't metered per token (local / subscription / unknown).
	Cost(provider, model string) (float64, float64, bool)
	IsLocal(provider string) bool
	IsSubscription(provider string) bool
}

// SetModelMeta wires the registry-backed resolver and refreshes the basis.
// Called once when settings open; nil leaves the cost basis at its defaults.
func (s *Session) SetModelMeta(m modelMeta) {
	s.meta = m
	s.refreshCostBasis()
}

// refreshCostBasis recomputes the cockpit's per-token cost and provider
// class from the live Provider/Model via the registry — the single place
// pricing and local/subscription classing are resolved, replacing the
// duplicated substring price table and name-literal classing.
func (s *Session) refreshCostBasis() {
	if s.meta == nil {
		return
	}
	s.Run.local = s.meta.IsLocal(s.Provider)
	s.Run.subscription = s.meta.IsSubscription(s.Provider)
	if in, out, ok := s.meta.Cost(s.Provider, s.Model); ok {
		s.Run.inCostPer1k, s.Run.outCostPer1k = in, out
	} else {
		s.Run.inCostPer1k, s.Run.outCostPer1k = 0, 0
	}
}

func (s *Session) SetProviderDisplay(name string) {
	s.Provider = name
	s.refreshCostBasis()
}

func (s *Session) ApplyModelPricing(model string) {
	s.Model = model
	s.refreshCostBasis()
}

func (s *Session) SetPricing(inPer1k, outPer1k float64) {
	s.Run.inCostPer1k, s.Run.outCostPer1k = inPer1k, outPer1k
}

func (s *Session) SetContextWindow(tokens int) {
	if tokens > 0 {
		s.Run.window = tokens
	}
}

func (s *Session) SetPressureConfig(window, reserve int) {
	s.Run.pressureWindow = window
	s.Run.pressureReserve = reserve
}

func (s *Session) ApplyProviderCostBasis(spec engine.ProviderSpec) {
	s.Provider = spec.Name
	s.Model = spec.Model
	s.refreshCostBasis()
}

// ToastExpiryCmd returns a command that wakes the Update loop when the
// current toast is due to expire. Returns nil when no toast is active.
func (s *Session) ToastExpiryCmd() tea.Cmd {
	if s.Toast == "" {
		return nil
	}
	return tea.Tick(mainToastTTL, func(time.Time) tea.Msg { return mainToastMsg{} })
}

func (s *Session) checkpoints() *Checkpoints {
	if s.Checkpoints == nil {
		s.Checkpoints = NewCheckpoints(s.WorkspaceDir)
	}
	return s.Checkpoints
}

func (s *Session) logEvent(kind, detail string) {
	if s.EventLog == nil {
		s.EventLog = NewEventRing(64)
	}
	s.EventLog.Add(EventRingEntry{Kind: kind, Detail: detail, At: time.Now()})
}

func (s *Session) workingSet() *WorkingSet {
	if s.WorkingSet == nil {
		s.WorkingSet = NewWorkingSet(s.WorkspaceDir)
	}
	return s.WorkingSet
}
