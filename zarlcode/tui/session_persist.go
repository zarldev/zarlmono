package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"path"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zarlcode/draft"
	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/prefs"
)

const (
	activeSessionKey      = "active_session"
	sessionSaveCommandTTL = 2 * time.Second
)

type sessionSaveFailedMsg struct{ Error string }
type sessionClearFailedMsg struct{ Error string }

type sessionSummary struct {
	ID                 string
	Label              string
	LabelManual        bool
	Provider           string
	Model              string
	CreatedAt          time.Time
	SavedAt            time.Time
	Pinned             bool
	PinnedAt           time.Time
	AgentName          string
	ChangedFileCount   int
	PlanCompletedCount int
	PlanTotalCount     int
	HasDraft           bool
	Messages           int
}

var errSessionSnapshotEmpty = errors.New("session snapshot empty")

type sessionRestoreDiagnostic string

const (
	sessionRestorePlanCorrupt       sessionRestoreDiagnostic = "plan"
	sessionRestoreDiffBodiesCorrupt sessionRestoreDiagnostic = "diff bodies"
	sessionRestoreUsageCorrupt      sessionRestoreDiagnostic = "usage"
	sessionRestoreToolTraceCorrupt  sessionRestoreDiagnostic = "tool trace"
	sessionRestoreDraftCorrupt      sessionRestoreDiagnostic = "draft"
)

type savedSession struct {
	sessionSummary
	Plan               code.Plan
	DiffBodies         map[string]string
	Usage              SessionUsageSnapshot
	ToolTraceRaw       []byte
	History            []llm.Message
	DraftText          string
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
	return sessionSummary{
		ID:                 rec.ID,
		Label:              rec.Label,
		LabelManual:        rec.LabelManual,
		Provider:           rec.Provider,
		Model:              rec.Model,
		CreatedAt:          rec.CreatedAt,
		SavedAt:            rec.UpdatedAt,
		Pinned:             rec.Pinned,
		PinnedAt:           rec.PinnedAt,
		AgentName:          rec.AgentName,
		ChangedFileCount:   rec.ChangedFileCount,
		PlanCompletedCount: rec.PlanCompletedCount,
		PlanTotalCount:     rec.PlanTotalCount,
		HasDraft:           rec.HasDraft,
		Messages:           sessionMessageCount(rec, 0),
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
	draftText, err := draft.Decode(rec.PendingJSON)
	if err != nil {
		s.addRestoreDiagnostic(sessionRestoreDraftCorrupt)
	} else {
		s.DraftText = draftText
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
	cmd, accepted := m.acceptSubmit(prompt)
	if !accepted {
		return cmd
	}
	return tea.Batch(cmd, m.clearDraftCmd())
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
	m.session.SetIdentity(s.ID, s.Label, s.LabelManual, s.CreatedAt)
	if m.live != nil {
		m.live.RestoreHistory(s.History)
	}
	m.RestoreTranscript(s.History)
	m.composer.setText(s.DraftText)
	m.resetInputHistoryBrowse()
	restoreToolTrace(m.timeline, s.ToolTraceRaw)
	// Rehydrate the per-session working state so the plan overlay, Files
	// dock + diff viewer, and cockpit totals reflect the resumed session.
	m.session.Plan = s.Plan
	m.session.workingSet().RestoreDiffBodies(s.DiffBodies, s.SavedAt)
	m.session.Run.RestoreUsage(s.Usage)
	noticeLabel := introSessionDisplayLabel(s.sessionSummary)
	notice := fmt.Sprintf("resumed session %q — %d message(s)", noticeLabel, len(s.History))
	if !s.SavedAt.IsZero() {
		notice += ", saved " + formatAgo(time.Since(s.SavedAt))
	}
	if useSavedTarget && s.Provider != "" && s.Model != "" {
		notice += "; switching to saved target " + providerModelLabel(s.Provider, s.Model)
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
			m.session.SetToastTone(notice+"; active session preference was not saved: "+err.Error(), toastWarn)
		}
	}
	if useSavedTarget && s.Provider != "" && s.Model != "" {
		m.persistResumeTarget(s.Provider, s.Model)
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
		m.session.SetErrorToast("resumed target: " + err.Error())
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
	pendingJSON, err := draft.Encode(m.composer.text())
	if err != nil {
		return nil, fmt.Errorf("encode draft: %w", err)
	}
	toolTraceJSON, err := encodeToolTraceJSON(m.timeline)
	if err != nil {
		return nil, fmt.Errorf("encode tool trace: %w", err)
	}
	changedFileCount := len(m.session.WorkingSet.FilesChangedThisSession())
	planCompletedCount := 0
	for _, step := range m.session.Plan.Steps {
		if step.Status == code.StepStatuses.COMPLETED {
			planCompletedCount++
		}
	}

	return &sessionSnapshot{record: db.SessionRecord{
		ID:                 m.session.ID,
		Workspace:          m.settings.WorkspaceRoot(),
		Label:              m.session.Label,
		LabelManual:        m.session.LabelManual,
		Provider:           m.session.Provider,
		Model:              m.session.Model,
		HistoryJSON:        historyJSON,
		PendingJSON:        pendingJSON,
		LastUsageJSON:      usageJSON,
		DiffBodiesJSON:     diffBodiesJSON,
		PlanJSON:           planJSON,
		ToolTraceJSON:      toolTraceJSON,
		ChangedFileCount:   changedFileCount,
		PlanCompletedCount: planCompletedCount,
		PlanTotalCount:     len(m.session.Plan.Steps),
		MessageCount:       len(history),
		CreatedAt:          m.session.CreatedAt,
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

// FlushSessionPersistence completes queued writes in FIFO order, then writes
// the final resumable snapshot. It is called after the Bubble Tea loop stops,
// when no command can concurrently mutate the queue.
func (m *UI) FlushSessionPersistence(ctx context.Context) error {
	if m.sessionPersistRunning {
		return errors.New("session persistence still running")
	}
	for _, op := range m.sessionPersistQueue {
		var err error
		switch op.kind {
		case sessionPersistDraft:
			err = m.settings.Store.SaveSessionDraft(ctx, op.draft)
		case sessionPersistClearDraft:
			err = m.settings.Store.ClearSessionDraft(ctx, op.oldID)
		case sessionPersistFull:
			err = saveSessionSnapshot(ctx, m.settings, op.snapshot)
		case sessionPersistDelete:
			err = clearPersistedSession(ctx, m.settings, op.oldID)
		}
		if err != nil {
			return fmt.Errorf("flush queued persistence: %w", err)
		}
	}
	m.sessionPersistQueue = nil
	return m.SaveSession(ctx)
}

func (m *UI) saveSessionCmd() tea.Cmd {
	snapshot, err := m.sessionSnapshot()
	if errors.Is(err, errSessionSnapshotEmpty) {
		return nil
	}
	if err != nil {
		return func() tea.Msg { return sessionSaveFailedMsg{Error: err.Error()} }
	}
	return m.enqueueSessionPersist(sessionPersistOp{kind: sessionPersistFull, snapshot: snapshot})
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
	return tea.Batch(m.toastExpiryCmd(), m.enqueueSessionPersist(sessionPersistOp{kind: sessionPersistDelete, oldID: oldID}))
}

func clearPersistedSession(ctx context.Context, settings *engine.Settings, oldID string) error {
	if settings == nil {
		return nil
	}
	var err error
	if oldID != "" && settings.Store != nil {
		if e := settings.Store.DeleteSession(ctx, oldID); e != nil {
			err = fmt.Errorf("delete session: %w", e)
		}
	}
	if settings.Svc != nil {
		if e := settings.Svc.DeleteSetting(ctx, prefs.ScopeWorkspace, activeSessionKey); e != nil {
			if err != nil {
				err = fmt.Errorf("%w; clear active session: %w", err, e)
			} else {
				err = fmt.Errorf("clear active session: %w", e)
			}
		}
	}
	return err
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

	var restoredThinking *thinkingItem
	for _, h := range history {
		switch h.Role {
		case "user":
			restoredThinking = nil
			timelineText := restoredUserContent(h)
			if strings.TrimSpace(timelineText) != "" {
				tl.addUser(timelineText)
			}
		case llm.RoleAssistant:
			if strings.TrimSpace(h.Content) != "" {
				tl.appendItem(&assistantItem{content: h.Content, done: true})
			}
			if reasoning := strings.TrimSpace(h.ReasoningContent); reasoning != "" {
				if restoredThinking == nil {
					restoredThinking = &thinkingItem{nested: true, done: true}
					tl.appendItem(restoredThinking)
				}
				if restoredThinking.text != "" {
					restoredThinking.text += "\n\n"
				}
				restoredThinking.text += reasoning
				restoredThinking.bump()
				tl.invalidateItem(restoredThinking)
			}
			if len(h.ToolCalls) > 0 {
				g := tl.ensureToolGroup(0)
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
	tl.closeGroups()
}

// RestoreTranscript replaces the visible conversation with persisted message
// history, including stable placeholders for multimodal user content.
func (m *UI) RestoreTranscript(history []llm.Message) {
	m.timeline.restoreMessages(history)
}

func restoredUserContent(message llm.Message) string {
	blocks := make([]string, 0, 1+len(message.Parts))
	content := strings.TrimSpace(message.Content)
	if content != "" {
		blocks = append(blocks, content)
	}
	for _, part := range message.Parts {
		switch part.Type {
		case llm.ContentTypeText:
			text := strings.TrimSpace(part.Text)
			if text != "" && text != content {
				blocks = append(blocks, text)
			}
		case llm.ContentTypeImage:
			blocks = append(blocks, restoredMediaPlaceholder("image", imagePartLabel(part.Image)))
		case llm.ContentTypeAudio:
			blocks = append(blocks, restoredMediaPlaceholder("audio", audioPartLabel(part.Audio)))
		case llm.ContentTypeVideo:
			blocks = append(blocks, restoredMediaPlaceholder("video", videoPartLabel(part.Video)))
		default:
			blocks = append(blocks, "[attachment]")
		}
	}
	return strings.Join(blocks, "\n\n")
}

func restoredMediaPlaceholder(kind, label string) string {
	if label == "" {
		return "[" + kind + "]"
	}
	return "[" + kind + ": " + label + "]"
}

func imagePartLabel(image *llm.ImageData) string {
	if image == nil {
		return ""
	}
	if name := mediaURLName(image.URL); name != "" {
		return name
	}
	return image.MIMEType
}

func audioPartLabel(audio *llm.AudioData) string {
	if audio == nil {
		return ""
	}
	return audio.Format
}

func videoPartLabel(video *llm.VideoData) string {
	if video == nil {
		return ""
	}
	if name := mediaURLName(video.URL); name != "" {
		return name
	}
	return video.MIMEType
}

func mediaURLName(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	name := path.Base(parsed.Path)
	if name == "." || name == "/" {
		return ""
	}
	return name
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
