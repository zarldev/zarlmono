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

type savedSession struct {
	sessionSummary
	Plan       code.Plan
	DiffBodies map[string]string
	Usage      SessionUsageSnapshot
	History    []llm.Message
}

func listSavedSessions(ctx context.Context, store *db.Store, wsRoot string) ([]sessionSummary, error) {
	if store == nil {
		return nil, nil
	}
	rows, err := store.ListSessions(ctx, wsRoot)
	if err != nil {
		return nil, err
	}
	out := make([]sessionSummary, 0, len(rows))
	for _, r := range rows {
		s, err := decodeSavedSession(r)
		if err != nil {
			continue // one corrupt row should not hide the picker
		}
		out = append(out, s.sessionSummary)
	}
	return out, nil
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
	label := rec.Label
	if label == "" {
		label = rec.CreatedAt.Format("2006-01-02 15:04")
	}
	// The auxiliary blobs are best-effort: a corrupt plan/diff/usage
	// field must not block resuming the conversation itself, which lives
	// in HistoryJSON. Decode failures leave the zero value and are logged.
	s := &savedSession{
		sessionSummary: sessionSummary{
			ID:        rec.ID,
			Label:     label,
			Provider:  rec.Provider,
			Model:     rec.Model,
			CreatedAt: rec.CreatedAt,
			SavedAt:   rec.UpdatedAt,
			Messages:  len(history),
		},
		History: history,
	}
	decodeSessionBlob(rec.PlanJSON, &s.Plan, rec.ID, "plan")
	decodeSessionBlob(rec.DiffBodiesJSON, &s.DiffBodies, rec.ID, "diff bodies")
	decodeSessionBlob(rec.LastUsageJSON, &s.Usage, rec.ID, "usage")
	return s, nil
}

// decodeSessionBlob unmarshals an optional session blob into dst,
// treating empty / "null" as "absent" and logging a decode error
// without failing the load.
func decodeSessionBlob(blob []byte, dst any, id, what string) {
	if len(blob) == 0 || string(blob) == "null" {
		return
	}
	if err := json.Unmarshal(blob, dst); err != nil {
		slog.Warn("session restore: decode "+what, "session", id, "err", err)
	}
}

// encodeSessionJSON marshals v for a session blob column, falling back to
// the column's empty sentinel when v is empty or marshalling fails — a
// best-effort persist must never abort the whole save over an optional
// field.
func encodeSessionJSON(v any, fallback string) []byte {
	b, err := json.Marshal(v)
	if err != nil || len(b) == 0 || string(b) == "null" {
		return []byte(fallback)
	}
	return b
}

// encodePlanJSON serialises the session plan, storing "null" when there
// are no steps so an empty plan restores as no overlay.
func encodePlanJSON(p code.Plan) []byte {
	if len(p.Steps) == 0 {
		return []byte("null")
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
	m.intro = nil
	m.session.SetIdentity(s.ID, s.Label, s.CreatedAt)
	if m.live != nil {
		m.live.RestoreHistory(s.History)
	}
	m.timeline.restoreMessages(s.History)
	// Rehydrate the per-session working state so the plan overlay, Files
	// dock + diff viewer, and cockpit totals reflect the resumed session.
	m.session.Plan = s.Plan
	m.session.workingSet().RestoreDiffBodies(s.DiffBodies, s.SavedAt)
	m.session.Run.RestoreUsage(s.Usage)
	notice := fmt.Sprintf("resumed session %q — %d message(s)", s.Label, len(s.History))
	if !s.SavedAt.IsZero() {
		notice += ", saved " + formatAgo(time.Since(s.SavedAt))
	}
	m.session.SetSuccessToast(notice)
	if m.settings.Svc != nil {
		if err := m.settings.Svc.SetSetting(m.appContext(), prefs.ScopeWorkspace, activeSessionKey, s.ID); err != nil {
			slog.WarnContext(m.appContext(), "persist active session", "err", err, "session", s.ID)
		}
	}
	return m.toastExpiryCmd()
}

func (m *UI) SaveSession(ctx context.Context) error {
	if m.settings == nil || m.settings.Store == nil || m.live == nil {
		return nil
	}
	history := m.live.History()
	if len(history) == 0 {
		return nil
	}
	m.session.EnsureIdentity(uuid.NewString(), time.Now())
	historyJSON, err := json.Marshal(history)
	if err != nil {
		return fmt.Errorf("encode history: %w", err)
	}
	rec := db.SessionRecord{
		ID:             m.session.ID,
		Workspace:      m.settings.WorkspaceRoot(),
		Label:          m.session.Label,
		Provider:       m.session.Provider,
		Model:          m.session.Model,
		HistoryJSON:    historyJSON,
		PendingJSON:    []byte("[]"),
		LastUsageJSON:  encodeSessionJSON(m.session.Run.UsageSnapshot(), "null"),
		DiffBodiesJSON: encodeSessionJSON(m.session.WorkingSet.DiffBodies(), "{}"),
		PlanJSON:       encodePlanJSON(m.session.Plan),
		CreatedAt:      m.session.CreatedAt,
	}
	if err := m.settings.Store.SaveSession(ctx, rec); err != nil {
		return err
	}
	if m.settings.Svc != nil {
		if err := m.settings.Svc.SetSetting(ctx, prefs.ScopeWorkspace, activeSessionKey, m.session.ID); err != nil {
			return err
		}
	}
	return nil
}

func (m *UI) saveSessionCmd() tea.Cmd {
	return func() tea.Msg {
		if err := m.SaveSession(context.WithoutCancel(m.appContext())); err != nil {
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
	tl.items = nil
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
				tl.items = append(tl.items, asst)
			}
			if strings.TrimSpace(h.ReasoningContent) != "" {
				tl.items = append(tl.items, &thinkingItem{nested: true, text: h.ReasoningContent, done: true})
			}
			if len(h.ToolCalls) > 0 {
				g := &groupItem{kind: groupTools, nested: true, closed: true}
				for _, tc := range h.ToolCalls {
					name := tc.Function.Name
					if name == "" {
						name = "tool"
					}
					t := &toolItem{name: name, arg: toolCallArgHint(tc), state: toolOK}
					g.add(t)
					if tc.ID != "" {
						tl.toolIdx[tc.ID] = toolRef{group: g, tool: t}
					}
				}
				if len(g.children) > 0 {
					tl.items = append(tl.items, g)
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
