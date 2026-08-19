package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/prefs"
)

const activeSessionKey = "active_session"

type sessionSaveFailedMsg struct{ Error string }
type sessionClearFailedMsg struct{ Error string }

type sessionSummary struct {
	ID        string
	Label     string
	Provider  string
	Model     string
	CreatedAt time.Time
	SavedAt   time.Time
	Messages  int
}

var errSessionSnapshotEmpty = errors.New("session snapshot empty")

type sessionRestoreDiagnostic string

const (
	sessionRestorePlanCorrupt       sessionRestoreDiagnostic = "plan"
	sessionRestoreDiffBodiesCorrupt sessionRestoreDiagnostic = "diff bodies"
	sessionRestoreUsageCorrupt      sessionRestoreDiagnostic = "usage"
	sessionRestoreToolTraceCorrupt  sessionRestoreDiagnostic = "tool trace"
)

type savedSession struct {
	sessionSummary
	Plan               code.Plan
	DiffBodies         map[string]string
	Usage              SessionUsageSnapshot
	ToolTraceRaw       []byte
	History            []llm.Message
	restoreDiagnostics []sessionRestoreDiagnostic
}

func (s *savedSession) addRestoreDiagnostic(diagnostic sessionRestoreDiagnostic) {
	s.restoreDiagnostics = append(s.restoreDiagnostics, diagnostic)
}

func (s *savedSession) consumeRestoreDiagnostics() []sessionRestoreDiagnostic {
	diagnostics := s.restoreDiagnostics
	s.restoreDiagnostics = nil
	return diagnostics
}

func listSavedSessions(ctx context.Context, store *db.Store, wsRoot string) ([]sessionSummary, error) {
	if store == nil {
		return nil, nil
	}
	rows, err := store.ListSessionSummaries(ctx, wsRoot)
	if err != nil {
		return nil, err
	}
	out := make([]sessionSummary, 0, len(rows))
	for _, r := range rows {
		s := savedSessionSummary(r)
		out = append(out, s)
	}
	return out, nil
}

func savedSessionSummary(rec db.SessionRecord) sessionSummary {
	label := rec.Label
	if label == "" {
		label = rec.CreatedAt.Format("2006-01-02 15:04")
	}
	return sessionSummary{
		ID:        rec.ID,
		Label:     label,
		Provider:  rec.Provider,
		Model:     rec.Model,
		CreatedAt: rec.CreatedAt,
		SavedAt:   rec.UpdatedAt,
		Messages:  sessionMessageCount(rec, 0),
	}
}

func loadSavedSession(ctx context.Context, store *db.Store, id string) (*savedSession, error) {
	if store == nil {
		return nil, errors.New("session store unavailable")
	}
	rec, err := store.GetSession(ctx, id)
	if err != nil {
		return nil, err
	}
	return decodeSavedSession(rec)
}

func decodeSavedSession(rec db.SessionRecord) (*savedSession, error) {
	var history []llm.Message
	if len(rec.HistoryJSON) > 0 {
		if err := json.Unmarshal(rec.HistoryJSON, &history); err != nil {
			return nil, err
		}
	}
	summary := savedSessionSummary(rec)
	summary.Messages = sessionMessageCount(rec, len(history))
	// The auxiliary blobs are best-effort: a corrupt plan/diff/usage
	// field must not block resuming the conversation itself, which lives
	// in HistoryJSON. Decode failures leave the zero value for the resume
	// boundary to report once.
	s := &savedSession{
		sessionSummary: summary,
		History:        history,
		ToolTraceRaw:   rec.ToolTraceJSON,
	}
	if !decodeSessionBlob(rec.PlanJSON, &s.Plan) {
		s.addRestoreDiagnostic(sessionRestorePlanCorrupt)
	}
	if !decodeSessionBlob(rec.DiffBodiesJSON, &s.DiffBodies) {
		s.addRestoreDiagnostic(sessionRestoreDiffBodiesCorrupt)
	}
	if !decodeSessionBlob(rec.LastUsageJSON, &s.Usage) {
		s.addRestoreDiagnostic(sessionRestoreUsageCorrupt)
	}
	if toolTraceMalformed(rec.ToolTraceJSON) {
		s.addRestoreDiagnostic(sessionRestoreToolTraceCorrupt)
	}
	return s, nil
}

func sessionMessageCount(rec db.SessionRecord, fallback int) int {
	if rec.MessageCount > 0 || len(rec.HistoryJSON) == 0 {
		return rec.MessageCount
	}
	return fallback
}

// decodeSessionBlob unmarshals an optional session blob into dst. Empty and
// "null" blobs are absent; malformed blobs are reported to the resume boundary.
func decodeSessionBlob(blob []byte, dst any) bool {
	if len(blob) == 0 || string(blob) == "null" {
		return true
	}
	return json.Unmarshal(blob, dst) == nil
}

func toolTraceMalformed(raw []byte) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return false
	}
	var trace savedToolTrace
	return json.Unmarshal(raw, &trace) != nil
}

// encodeSessionJSON marshals one independently snapshotted session value.
// A serialization error is not equivalent to an empty value and aborts the
// save rather than overwriting a previously valid blob with an empty sentinel.
func encodeSessionJSON(v any, fallback string) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if len(b) == 0 || string(b) == "null" {
		return []byte(fallback), nil
	}
	return b, nil
}

// encodePlanJSON serialises the session plan, storing "null" when there
// are no steps so an empty plan restores as no overlay.
func encodePlanJSON(p code.Plan) ([]byte, error) {
	if len(p.Steps) == 0 {
		return []byte("null"), nil
	}
	return encodeSessionJSON(p, "null")
}

func (m *UI) ActivateIntro(ctx context.Context) {
	if m.settings == nil {
		m.intro = newIntroPane(m.session.Workspace, nil, "", "")
		return
	}
	sessions, err := listSavedSessions(ctx, m.settings.Store, m.settings.WorkspaceRoot())
	m.intro = newIntroPane(shortenHome(m.settings.WorkspaceRoot()), sessions, m.session.Provider, m.session.Model)
	if err != nil {
		m.intro.err = err.Error()
	}
}

func (m *UI) dismissIntroFresh(prompt string) tea.Cmd {
	m.intro = nil
	m.session.ClearIdentity()
	if m.live != nil {
		m.live.RestoreHistory(nil)
	}
	m.timeline.restoreMessages(nil)
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil
	}
	return m.submit(prompt)
}

func (m *UI) resumeIntroSession(id string) tea.Cmd {
	if m.settings == nil {
		return nil
	}
	s, err := loadSavedSession(m.appContext(), m.settings.Store, id)
	if err != nil {
		if m.intro != nil {
			m.intro.err = err.Error()
		}
		return nil
	}
	if m.resumeTargetDiffers(s) {
		m.overlay.push(newResumeTargetDialog(s, m.session.Provider, m.session.Model))
		return nil
	}
	return m.completeResumeSession(s, false)
}

func (m *UI) resumeTargetDiffers(s *savedSession) bool {
	if s == nil || s.Provider == "" || s.Model == "" {
		return false
	}
	active := m.session.ActiveProviderSpec()
	return s.Provider != active.Name || s.Model != active.Model
}

func (m *UI) completeResumeSession(s *savedSession, useSavedTarget bool) tea.Cmd {
	if s == nil {
		return nil
	}
	m.intro = nil
	m.session.SetIdentity(s.ID, s.Label, s.CreatedAt)
	if m.live != nil {
		m.live.RestoreHistory(s.History)
	}
	m.timeline.restoreMessages(s.History)
	restoreToolTrace(m.timeline, s.ToolTraceRaw)
	// Rehydrate the per-session working state so the plan overlay, Files
	// dock + diff viewer, and cockpit totals reflect the resumed session.
	m.session.Plan = s.Plan
	m.session.workingSet().RestoreDiffBodies(s.DiffBodies, s.SavedAt)
	m.session.Run.RestoreUsage(s.Usage)
	notice := fmt.Sprintf("resumed session %q — %d message(s)", s.Label, len(s.History))
	if !s.SavedAt.IsZero() {
		notice += ", saved " + formatAgo(time.Since(s.SavedAt))
	}
	if useSavedTarget && s.Provider != "" && s.Model != "" {
		notice += "; switching to saved target " + providerModelLabel(s.Provider, s.Model)
		m.persistResumeTarget(s.Provider, s.Model)
	}
	diagnostics := s.consumeRestoreDiagnostics()
	if len(diagnostics) > 0 {
		slog.WarnContext(m.appContext(), "resume session with incomplete saved details", "session", s.ID, "details", diagnostics)
		notice += "; some saved details were unavailable"
		m.session.SetToastTone(notice, toastWarn)
	} else {
		m.session.SetSuccessToast(notice)
	}
	if m.settings.Svc != nil {
		if err := m.settings.Svc.SetSetting(m.appContext(), prefs.ScopeWorkspace, activeSessionKey, s.ID); err != nil {
			slog.WarnContext(m.appContext(), "persist active session", "err", err, "session", s.ID)
		}
	}
	cmd := m.toastExpiryCmd()
	if useSavedTarget {
		cmd = tea.Batch(cmd, m.maybeRepoint())
	}
	return cmd
}

func (m *UI) persistResumeTarget(provider, model string) {
	if m.settings == nil || m.settings.Svc == nil {
		return
	}
	ctx := m.appContext()
	selection := prefs.ModelSelection{Provider: provider, Model: model}
	if err := m.settings.Svc.SetModelSelection(ctx, prefs.ScopeWorkspace, selection); err != nil {
		slog.WarnContext(ctx, "persist resumed session target", "err", err, "provider", provider, "model", model)
	}
}

type sessionSnapshot struct {
	record db.SessionRecord
}

func (m *UI) sessionSnapshot() (*sessionSnapshot, error) {
	if m.settings == nil || m.settings.Store == nil || m.live == nil {
		return nil, errSessionSnapshotEmpty
	}
	history := m.live.History()
	if len(history) == 0 {
		return nil, errSessionSnapshotEmpty
	}
	m.session.EnsureIdentity(uuid.NewString(), time.Now())

	historyJSON, err := json.Marshal(history)
	if err != nil {
		return nil, fmt.Errorf("encode history: %w", err)
	}
	usageJSON, err := encodeSessionJSON(m.session.Run.UsageSnapshot(), "null")
	if err != nil {
		return nil, fmt.Errorf("encode usage: %w", err)
	}
	diffBodiesJSON, err := encodeSessionJSON(m.session.WorkingSet.DiffBodies(), "{}")
	if err != nil {
		return nil, fmt.Errorf("encode diff bodies: %w", err)
	}
	planJSON, err := encodePlanJSON(m.session.Plan)
	if err != nil {
		return nil, fmt.Errorf("encode plan: %w", err)
	}
	toolTraceJSON, err := encodeToolTraceJSON(m.timeline)
	if err != nil {
		return nil, err
	}

	return &sessionSnapshot{record: db.SessionRecord{
		ID:             m.session.ID,
		Workspace:      m.settings.WorkspaceRoot(),
		Label:          m.session.Label,
		Provider:       m.session.Provider,
		Model:          m.session.Model,
		HistoryJSON:    historyJSON,
		PendingJSON:    []byte("[]"),
		LastUsageJSON:  usageJSON,
		DiffBodiesJSON: diffBodiesJSON,
		PlanJSON:       planJSON,
		ToolTraceJSON:  toolTraceJSON,
		MessageCount:   len(history),
		CreatedAt:      m.session.CreatedAt,
	}}, nil
}

func saveSessionSnapshot(ctx context.Context, settings *engine.Settings, snapshot *sessionSnapshot) error {
	if settings == nil || settings.Store == nil || snapshot == nil {
		return nil
	}
	if err := settings.Store.SaveActiveSession(ctx, snapshot.record); err != nil {
		return fmt.Errorf("save session snapshot: %w", err)
	}
	return nil
}

func (m *UI) SaveSession(ctx context.Context) error {
	snapshot, err := m.sessionSnapshot()
	if errors.Is(err, errSessionSnapshotEmpty) {
		return nil
	}
	if err != nil {
		return err
	}
	return saveSessionSnapshot(ctx, m.settings, snapshot)
}

func (m *UI) saveSessionCmd() tea.Cmd {
	snapshot, err := m.sessionSnapshot()
	settings := m.settings
	ctx := context.WithoutCancel(m.appContext())
	return func() tea.Msg {
		if errors.Is(err, errSessionSnapshotEmpty) {
			return nil
		}
		if err != nil {
			return sessionSaveFailedMsg{Error: err.Error()}
		}
		if err := saveSessionSnapshot(ctx, settings, snapshot); err != nil {
			return sessionSaveFailedMsg{Error: err.Error()}
		}
		return nil
	}
}

func (m *UI) clearContextAndTimeline() tea.Cmd {
	if m.session.Run.Running {
		m.session.SetErrorToast("stop current turn before clearing")
		return m.toastExpiryCmd()
	}
	oldID := m.session.ID
	if m.live != nil {
		m.live.ClearHistory()
	}
	m.timeline.Clear()
	m.session.ClearIdentity()
	m.session.Run.RestoreUsage(SessionUsageSnapshot{})
	m.session.Plan = code.Plan{}
	m.session.SetSuccessToast("conversation cleared")
	return tea.Batch(m.toastExpiryCmd(), m.clearPersistedSessionCmd(oldID))
}

func (m *UI) clearPersistedSessionCmd(oldID string) tea.Cmd {
	return func() tea.Msg {
		if m.settings == nil {
			return nil
		}
		ctx := context.WithoutCancel(m.appContext())
		var err error
		if oldID != "" && m.settings.Store != nil {
			if e := m.settings.Store.DeleteSession(ctx, oldID); e != nil {
				err = fmt.Errorf("delete session: %w", e)
			}
		}
		if m.settings.Svc != nil {
			if e := m.settings.Svc.DeleteSetting(ctx, prefs.ScopeWorkspace, activeSessionKey); e != nil {
				if err != nil {
					err = fmt.Errorf("%w; clear active session: %w", err, e)
				} else {
					err = fmt.Errorf("clear active session: %w", e)
				}
			}
		}
		if err != nil {
			return sessionClearFailedMsg{Error: err.Error()}
		}
		return nil
	}
}

func (tl *timeline) restoreMessages(history []llm.Message) {
	tl.clearItems()
	tl.toolIdx = make(map[string]toolRef)
	tl.turns = make(map[string]*openTurn)
	tl.cache = make(map[item]cacheEntry)
	tl.curTools = nil
	tl.curEdits = nil
	tl.browsing = false
	tl.scrollTop = 0
	tl.sel = 0
	tl.selLocal = 0

	for _, h := range history {
		switch h.Role {
		case "user":
			tl.addUser(h.Content)
		case llm.RoleAssistant:
			if strings.TrimSpace(h.Content) != "" || len(h.ToolCalls) > 0 {
				asst := &assistantItem{content: h.Content, done: true}
				if strings.TrimSpace(h.Content) == "" && len(h.ToolCalls) > 0 {
					asst.status = "called " + strings.Join(toolCallNames(h.ToolCalls), ", ")
				}
				tl.appendItem(asst)
			}
			if strings.TrimSpace(h.ReasoningContent) != "" {
				tl.appendItem(&thinkingItem{nested: true, text: h.ReasoningContent, done: true})
			}
			if len(h.ToolCalls) > 0 {
				g := &groupItem{kind: groupTools, nested: true, closed: true}
				for _, tc := range h.ToolCalls {
					name := tc.Function.Name
					if name == "" {
						name = "tool"
					}
					t := &toolItem{name: name, arg: toolCallArgHint(tc), state: toolOK, notify: g.bump}
					g.add(t)
					if tc.ID != "" {
						tl.toolIdx[tc.ID] = toolRef{group: g, tool: t}
					}
				}
				if len(g.children) > 0 {
					tl.appendItem(g)
				}
			}
		case "tool":
			body := firstLine(strings.TrimSpace(h.Content))
			if body == "" {
				body = "completed"
			}
			if ref, ok := tl.toolIdx[h.ToolCallID]; ok {
				ref.tool.state = toolOK
				ref.tool.result = strings.TrimSpace(h.Content)
				if ref.tool.result == "" {
					ref.tool.result = "completed"
				}
				ref.tool.bump()
				ref.group.bump()
				continue
			}
			tl.addNotice(palette.Muted.On("✓ tool — " + body))
		}
	}
}

func toolCallNames(calls []llm.ToolCall) []string {
	names := make([]string, 0, len(calls))
	for _, tc := range calls {
		if tc.Function.Name != "" {
			names = append(names, tc.Function.Name)
		}
	}
	if len(names) == 0 {
		return []string{"tool"}
	}
	return names
}

func toolCallArgHint(tc llm.ToolCall) string {
	args := strings.TrimSpace(tc.Function.Arguments)
	if args == "" {
		return ""
	}
	var params map[string]any
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return ""
	}
	return toolArgHint(tc.Function.Name, params)
}

func formatAgo(d time.Duration) string {
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}
