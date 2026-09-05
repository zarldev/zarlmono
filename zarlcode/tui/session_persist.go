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

	"github.com/zarldev/zarlmono/zarlcode/draft"
	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/db"
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
	Workspace          string
}

var errSessionSnapshotEmpty = errors.New("session snapshot empty")

type sessionRestoreDiagnostic string

const (
	sessionRestorePlanCorrupt       sessionRestoreDiagnostic = "plan"
	sessionRestoreDiffBodiesCorrupt sessionRestoreDiagnostic = "diff bodies"
	sessionRestoreUsageCorrupt      sessionRestoreDiagnostic = "usage"
	sessionRestoreDraftCorrupt      sessionRestoreDiagnostic = "draft"
)

type savedSession struct {
	sessionSummary
	Plan               code.Plan
	DiffBodies         map[string]string
	Usage              SessionUsageSnapshot
	Context            []llm.Message
	Transcript         transcript.Thread
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
		if r.HasTranscript || r.HasDraft {
			out = append(out, savedSessionSummary(r))
		}
	}
	return out, nil
}

func savedSessionSummary(rec db.SessionRecord) sessionSummary {
	return sessionSummary{
		Workspace:          rec.Workspace,
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
		Messages:           rec.MessageCount,
	}
}

func (m *UI) resumeSession(ctx context.Context, id string) error {
	if m.settings == nil || m.settings.Store == nil {
		return errors.New("session store unavailable")
	}
	saved, err := loadSavedSession(ctx, m.settings.Store, id, m.settings.WorkspaceRoot())
	if err != nil {
		return err
	}
	m.completeResumeSession(saved, false)
	return nil
}

func (m *UI) resumeLatestSession(ctx context.Context) error {
	var ids []string
	if m.settings != nil && m.settings.Svc != nil {
		if active, err := m.settings.Svc.GetSetting(ctx, prefs.ScopeWorkspace, activeSessionKey); err == nil {
			ids = append(ids, active.Value)
		}
	}
	summaries, err := listSavedSessions(ctx, m.settings.Store, m.settings.WorkspaceRoot())
	if err != nil {
		return err
	}
	for _, summary := range summaries {
		ids = append(ids, summary.ID)
	}
	seen := make(map[string]struct{}, len(ids))
	var errs []error
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		if err := m.resumeSession(ctx, id); err == nil {
			return nil
		} else {
			errs = append(errs, fmt.Errorf("session %q: %w", id, err))
		}
	}
	if len(errs) == 0 {
		return errors.New("no resumable session in this workspace")
	}
	return fmt.Errorf("no valid resumable session in this workspace: %w", errors.Join(errs...))
}

func loadSavedSession(ctx context.Context, store *db.Store, id, workspace string) (*savedSession, error) {
	if store == nil {
		return nil, errors.New("session store unavailable")
	}
	rec, err := store.GetSession(ctx, id)
	if err != nil {
		return nil, err
	}
	if rec.Workspace != workspace {
		return nil, fmt.Errorf("session %q belongs to workspace %q, not %q", id, rec.Workspace, workspace)
	}
	saved, err := decodeSavedSession(rec)
	if err != nil {
		return nil, err
	}
	storedTranscript, err := store.GetSessionTranscript(ctx, id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			if rec.HasDraft {
				saved.Transcript = transcript.NewBuilder().Thread()
				return saved, nil
			}
			return nil, errors.New("session transcript not found")
		}
		return nil, err
	}
	if storedTranscript.FormatVersion > db.SessionTranscriptFormatVersion {
		return nil, fmt.Errorf("session transcript format %d is newer than supported format %d", storedTranscript.FormatVersion, db.SessionTranscriptFormatVersion)
	}
	thread, err := transcript.FromRecords(storedTranscript.Revision, dbTranscriptRecords(storedTranscript.Entries))
	if err != nil {
		return nil, fmt.Errorf("session transcript is corrupted: %w", err)
	}
	if thread.IsEmpty() {
		return nil, errors.New("session transcript is empty")
	}
	recovered, _ := thread.RecoverInterrupted()
	if recovered.Revision() > storedTranscript.Revision {
		update, updateErr := transcriptRecoveryUpdate(rec, storedTranscript.Revision, recovered)
		if updateErr != nil {
			return nil, updateErr
		}
		if updateErr = store.UpdateActiveTranscript(ctx, update); updateErr != nil {
			recoveryErr := updateErr
			storedTranscript, updateErr = store.GetSessionTranscript(ctx, id)
			if updateErr != nil {
				return nil, fmt.Errorf("read interrupted transcript recovery: %w", updateErr)
			}
			thread, updateErr = transcript.FromRecords(storedTranscript.Revision, dbTranscriptRecords(storedTranscript.Entries))
			if updateErr != nil {
				return nil, fmt.Errorf("session transcript is corrupted: %w", updateErr)
			}
			if pending, _ := thread.RecoverInterrupted(); pending.Revision() > thread.Revision() {
				return nil, fmt.Errorf("persist interrupted transcript recovery: %w", recoveryErr)
			}
		} else {
			thread = recovered
		}
	}
	saved.Transcript = thread
	return saved, nil
}

func decodeSavedSession(rec db.SessionRecord) (*savedSession, error) {
	var contextCache []llm.Message
	if len(rec.ContextJSON) > 0 {
		if err := json.Unmarshal(rec.ContextJSON, &contextCache); err != nil {
			return nil, fmt.Errorf("decode context cache: %w", err)
		}
	}
	s := &savedSession{
		sessionSummary: savedSessionSummary(rec),
		Context:        contextCache,
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
	draftText, err := draft.Decode(rec.PendingJSON)
	if err != nil {
		s.addRestoreDiagnostic(sessionRestoreDraftCorrupt)
	} else {
		s.DraftText = draftText
	}
	return s, nil
}

// decodeSessionBlob unmarshals an optional session blob into dst. Empty and
// "null" blobs are absent; malformed blobs are reported to the resume boundary.
func decodeSessionBlob(blob []byte, dst any) bool {
	if len(blob) == 0 || string(blob) == "null" {
		return true
	}
	return json.Unmarshal(blob, dst) == nil
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
	m.session.resetSession()
	m.draftGeneration++
	m.composer.setText("")
	m.pendingAttachments = nil
	m.transcriptGeneration++
	m.resetTranscriptPersistence()
	if m.live != nil {
		m.live.RestoreContext(nil)
	}
	m.timeline.Clear()
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
	s, err := loadSavedSession(m.appContext(), m.settings.Store, id, m.settings.WorkspaceRoot())
	if err != nil {
		if m.intro != nil {
			m.intro.err = err.Error()
		}
		return nil
	}
	if s.Workspace != m.settings.WorkspaceRoot() {
		if m.intro != nil {
			m.intro.err = fmt.Sprintf("session belongs to workspace %q", s.Workspace)
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
	m.session.resetSession()
	m.draftGeneration++
	m.transcriptGeneration++
	m.resetTranscriptPersistence()
	m.pendingAttachments = nil
	m.session.SetIdentity(s.ID, s.Label, s.LabelManual, s.CreatedAt)
	if m.live != nil {
		m.live.RestoreContext(s.Context)
	}
	m.timeline.restoreThread(s.Transcript)
	m.transcriptPersisted = s.Transcript.Revision()
	m.transcriptPersistedSessionID = s.ID
	m.composer.setText(s.DraftText)
	m.resetInputHistoryBrowse()
	// Rehydrate the per-session working state so the plan overlay, Files
	// dock + diff viewer, and cockpit totals reflect the resumed session.
	m.session.Plan = s.Plan
	m.session.workingSet().RestoreDiffBodies(s.DiffBodies, s.SavedAt)
	m.session.Run.RestoreUsage(s.Usage)
	noticeLabel := introSessionDisplayLabel(s.sessionSummary)
	notice := fmt.Sprintf("resumed session %q — %d message(s)", noticeLabel, s.Messages)
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
	record     db.SessionRecord
	transcript db.TranscriptUpdate
	allEntries []db.TranscriptEntry
}

func (m *UI) sessionSnapshot() (*sessionSnapshot, error) {
	if m.settings == nil || m.settings.Store == nil || m.live == nil {
		return nil, errSessionSnapshotEmpty
	}
	contextCache := m.live.ContextSnapshot()
	if len(contextCache) == 0 || m.timeline.transcriptThread().IsEmpty() {
		return nil, errSessionSnapshotEmpty
	}
	m.session.EnsureIdentity(uuid.NewString(), time.Now())

	contextJSON, err := json.Marshal(contextCache)
	if err != nil {
		return nil, fmt.Errorf("encode context cache: %w", err)
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
	changedFileCount := len(m.session.WorkingSet.FilesChangedThisSession())
	planCompletedCount := 0
	for _, step := range m.session.Plan.Steps {
		if step.Status == code.StepStatuses.COMPLETED {
			planCompletedCount++
		}
	}

	thread := m.timeline.transcriptThread()
	messageCount := thread.MessageCount()
	transcriptUpdate, allEntries, err := m.transcriptUpdate(thread, m.persistedTranscriptRevision(m.session.ID))
	if err != nil {
		return nil, err
	}
	return &sessionSnapshot{record: db.SessionRecord{
		ID:                 m.session.ID,
		Workspace:          m.settings.WorkspaceRoot(),
		Label:              m.session.Label,
		LabelManual:        m.session.LabelManual,
		Provider:           m.session.Provider,
		Model:              m.session.Model,
		ContextJSON:        contextJSON,
		PendingJSON:        pendingJSON,
		LastUsageJSON:      usageJSON,
		DiffBodiesJSON:     diffBodiesJSON,
		PlanJSON:           planJSON,
		ChangedFileCount:   changedFileCount,
		PlanCompletedCount: planCompletedCount,
		PlanTotalCount:     len(m.session.Plan.Steps),
		MessageCount:       messageCount,
		CreatedAt:          m.session.CreatedAt,
	}, transcript: transcriptUpdate, allEntries: allEntries}, nil
}

func saveSessionSnapshot(ctx context.Context, settings *engine.Settings, snapshot *sessionSnapshot) error {
	if settings == nil || settings.Store == nil || snapshot == nil {
		return nil
	}
	if err := settings.Store.CommitCompletedTurn(ctx, snapshot.record, snapshot.transcript); err != nil {
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
	var firstErr error
	if m.sessionPersistRunning && m.sessionPersistCurrent != nil {
		select {
		case result := <-m.sessionPersistCurrent.done:
			if result.err != nil {
				firstErr = fmt.Errorf("flush in-flight persistence: %w", result.err)
			} else if result.sessionID == m.session.ID {
				m.transcriptPersistedSessionID = result.sessionID
				if result.revision > m.transcriptPersisted {
					m.transcriptPersisted = result.revision
				}
			}
			m.sessionPersistRunning = false
			m.sessionPersistCurrent = nil
		case <-ctx.Done():
			return fmt.Errorf("flush in-flight persistence: %w", ctx.Err())
		}
	}
	for _, op := range m.sessionPersistQueue {
		if (op.kind == sessionPersistTranscript || op.kind == sessionPersistFull) && op.sessionID() == m.transcriptPersistedSessionID {
			op.rebaseTranscript(m.transcriptPersisted)
			if op.kind == sessionPersistTranscript && op.transcriptRevision() <= m.transcriptPersisted {
				continue
			}
		}
		err := m.executeSessionPersist(ctx, op)
		if err != nil && (op.kind == sessionPersistTranscript || op.kind == sessionPersistFull) {
			err = retrySessionTranscriptPersist(ctx, m.settings, &op, err)
		}
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("flush queued persistence: %w", err)
			}
		} else if revision := op.transcriptRevision(); op.sessionID() == m.session.ID {
			m.transcriptPersistedSessionID = op.sessionID()
			if revision > m.transcriptPersisted {
				m.transcriptPersisted = revision
			}
		}
	}
	m.sessionPersistQueue = nil
	finalErr := m.SaveSession(ctx)
	if firstErr != nil && finalErr != nil {
		return errors.Join(firstErr, finalErr)
	}
	if finalErr != nil {
		return finalErr
	}
	return firstErr
}

func (m *UI) executeSessionPersist(ctx context.Context, op sessionPersistOp) error {
	switch op.kind {
	case sessionPersistDraft:
		return m.settings.Store.SaveSessionDraft(ctx, op.draft)
	case sessionPersistClearDraft:
		return m.settings.Store.ClearSessionDraft(ctx, op.oldID)
	case sessionPersistTranscript:
		return saveTranscriptSnapshot(ctx, m.settings.Store, op.transcript)
	case sessionPersistFull:
		return saveSessionSnapshot(ctx, m.settings, op.snapshot)
	case sessionPersistDelete:
		return clearPersistedSession(ctx, m.settings, op.oldID)
	default:
		return fmt.Errorf("unknown persistence operation %d", op.kind)
	}
}

func (m *UI) saveSessionCmd() tea.Cmd {
	snapshot, err := m.sessionSnapshot()
	if errors.Is(err, errSessionSnapshotEmpty) {
		return nil
	}
	if err != nil {
		return func() tea.Msg { return sessionSaveFailedMsg{Error: err.Error()} }
	}
	return m.enqueueSessionPersist(sessionPersistOp{kind: sessionPersistFull, generation: m.transcriptGeneration, snapshot: snapshot})
}

func (m *UI) clearContextAndTimeline() tea.Cmd {
	if m.session.Run.Running {
		m.session.SetErrorToast("stop current turn before clearing")
		return m.toastExpiryCmd()
	}
	oldID := m.session.ID
	m.startupPrompt = ""
	m.startupAttachments = nil
	m.startupAttachmentMetadata = nil
	m.session.SetSubmittedAttachments(nil)
	m.draftGeneration++
	m.composer.setText("")
	m.pendingAttachments = nil
	m.transcriptGeneration++
	m.resetTranscriptPersistence()
	if m.live != nil {
		m.live.ClearContext()
	}
	m.timeline.Clear()
	m.session.resetSession()
	m.session.SetSuccessToast("conversation cleared")
	return tea.Batch(m.toastExpiryCmd(), m.enqueueSessionPersist(sessionPersistOp{kind: sessionPersistDelete, generation: m.transcriptGeneration, oldID: oldID}))
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
		active, activeErr := settings.Svc.GetSetting(ctx, prefs.ScopeWorkspace, activeSessionKey)
		if activeErr == nil && active.Value == oldID {
			activeErr = settings.Svc.DeleteSetting(ctx, prefs.ScopeWorkspace, activeSessionKey)
		}
		if activeErr != nil && !errors.Is(activeErr, prefs.ErrNotFound) {
			if err != nil {
				err = fmt.Errorf("%w; clear active session: %w", err, activeErr)
			} else {
				err = fmt.Errorf("clear active session: %w", activeErr)
			}
		}
	}
	return err
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

type transcriptSnapshot struct {
	update     db.TranscriptUpdate
	allEntries []db.TranscriptEntry
}

func (m *UI) transcriptSnapshot() (*transcriptSnapshot, error) {
	if m.settings == nil || m.settings.Store == nil || m.timeline.transcriptThread().IsEmpty() {
		return nil, db.ErrNotFound
	}
	m.session.EnsureIdentity(uuid.NewString(), time.Now())
	thread := m.timeline.transcriptThread()
	update, allEntries, err := m.transcriptUpdate(thread, m.persistedTranscriptRevision(m.session.ID))
	if err != nil {
		return nil, err
	}
	return &transcriptSnapshot{update: update, allEntries: allEntries}, nil
}

func saveTranscriptSnapshot(ctx context.Context, store *db.Store, snapshot *transcriptSnapshot) error {
	if snapshot == nil {
		return nil
	}
	return store.UpdateActiveTranscript(ctx, snapshot.update)
}

func (m *UI) transcriptUpdate(thread transcript.Thread, expected uint64) (db.TranscriptUpdate, []db.TranscriptEntry, error) {
	records, err := thread.RecordsSince(0)
	if err != nil {
		return db.TranscriptUpdate{}, nil, err
	}
	entries := make([]db.TranscriptEntry, len(records))
	for i, record := range records {
		entries[i] = db.TranscriptEntry{
			Sequence: record.Sequence, EntryID: record.ID, ParentID: record.ParentID,
			TurnID: record.TurnID, Kind: record.Kind, PayloadJSON: record.Payload, Revision: record.Revision,
		}
	}
	update := db.TranscriptUpdate{
		SessionID: m.session.ID, Workspace: m.settings.WorkspaceRoot(), Label: m.session.Label,
		LabelManual: m.session.LabelManual, AgentName: m.session.LastAgentName,
		Provider: m.session.Provider, Model: m.session.Model, MessageCount: thread.MessageCount(),
		CreatedAt: m.session.CreatedAt, Revision: thread.Revision(), Entries: entries,
	}
	rebaseTranscriptUpdate(&update, entries, expected)
	return update, entries, nil
}

func rebaseTranscriptUpdate(update *db.TranscriptUpdate, allEntries []db.TranscriptEntry, revision uint64) {
	update.ExpectedRevision = revision
	entries := make([]db.TranscriptEntry, 0, len(allEntries))
	for _, entry := range allEntries {
		if entry.Revision > revision {
			entries = append(entries, entry)
		}
	}
	update.Entries = entries
	if len(entries) == 0 {
		update.Revision = revision
	}
}

func dbTranscriptRecords(entries []db.TranscriptEntry) []transcript.Record {
	records := make([]transcript.Record, len(entries))
	for i, entry := range entries {
		records[i] = transcript.Record{
			Sequence: entry.Sequence, ID: entry.EntryID, ParentID: entry.ParentID,
			TurnID: entry.TurnID, Kind: entry.Kind, Revision: entry.Revision, Payload: entry.PayloadJSON,
		}
	}
	return records
}

func transcriptRecoveryUpdate(record db.SessionRecord, expected uint64, thread transcript.Thread) (db.TranscriptUpdate, error) {
	records, err := thread.RecordsSince(expected)
	if err != nil {
		return db.TranscriptUpdate{}, err
	}
	entries := make([]db.TranscriptEntry, len(records))
	for i, saved := range records {
		entries[i] = db.TranscriptEntry{
			Sequence: saved.Sequence, EntryID: saved.ID, ParentID: saved.ParentID,
			TurnID: saved.TurnID, Kind: saved.Kind, PayloadJSON: saved.Payload, Revision: saved.Revision,
		}
	}
	return db.TranscriptUpdate{
		SessionID: record.ID, Workspace: record.Workspace, Label: record.Label,
		LabelManual: record.LabelManual, AgentName: record.AgentName,
		Provider: record.Provider, Model: record.Model, MessageCount: thread.MessageCount(),
		CreatedAt: record.CreatedAt, ExpectedRevision: expected, Revision: thread.Revision(), Entries: entries,
	}, nil
}
