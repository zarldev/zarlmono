package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zarlcode/draft"
	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
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
	DraftAttachments   []llm.ContentPart
	rejectedDraftJSON  []byte
	restoreDiagnostics []sessionRestoreDiagnostic
	settledTurnID      string // explicit exact-head identity; never inferred from transcript shape
	continuation       bool   // validated durable branch provenance, not transcript shape
	exactHead          *rewind.ResumeState
	contentVersion     db.SessionContentVersion
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

// exactSessionError retains the representation observed by the original load
// so a later read cannot authorize fallback to an unrelated conversation.
type exactSessionError struct{ cause error }

func (e *exactSessionError) Error() string { return e.cause.Error() }
func (e *exactSessionError) Unwrap() error { return e.cause }

func (m *UI) resumeSession(ctx context.Context, id string) error {
	if m.settings == nil || m.settings.Store == nil {
		return errors.New("session store unavailable")
	}
	saved, err := loadSavedSession(ctx, m.settings.Store, id, m.settings.WorkspaceRoot())
	if err != nil {
		return err
	}
	if saved.exactHead != nil {
		if err := m.resumeExactSession(ctx, saved); err != nil {
			return &exactSessionError{cause: err}
		}
		return nil
	}
	m.completeResumeSession(saved, false)
	return nil
}

func (m *UI) resumeLatestSession(ctx context.Context) error {
	var ids []string
	var activeID string
	if m.settings != nil && m.settings.Svc != nil {
		if active, err := m.settings.Svc.GetSetting(ctx, prefs.ScopeWorkspace, activeSessionKey); err == nil {
			ids = append(ids, active.Value)
			activeID = active.Value
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
			// Exact heads are authoritative even without branch provenance.
			// Never silently continue another conversation after exact rejection.
			var exactErr *exactSessionError
			if errors.As(err, &exactErr) {
				return errs[len(errs)-1]
			}
			record, readErr := m.settings.Store.GetSession(ctx, id)
			if (readErr != nil && !errors.Is(readErr, db.ErrNotFound)) ||
				(readErr == nil && strings.HasPrefix(strings.TrimSpace(string(record.ContextJSON)), "{")) {
				return errs[len(errs)-1]
			}
			if m.rewindRecovery != "" {
				return errs[len(errs)-1]
			}
			if id == activeID {
				// A committed continuation is authoritative. Missing/corrupt exact
				// state or unavailable credentials must not silently resume its source.
				_, branchErr := m.settings.Store.GetSessionBranch(ctx, id)
				if !errors.Is(branchErr, db.ErrNotFound) {
					return fmt.Errorf("active continuation requires recovery: %w", errors.Join(err, branchErr))
				}
			}
		}
	}
	if len(errs) == 0 {
		return errors.New("no resumable session in this workspace")
	}
	return fmt.Errorf("no valid resumable session in this workspace: %w", errors.Join(errs...))
}

func loadSavedSession(ctx context.Context, store *db.Store, id, workspace string) (*savedSession, error) {
	// Retry the whole snapshot, not just its transcript, after concurrent crash
	// recovery. The bound prevents a competing writer from stalling the UI.
	for range 3 {
		saved, err := loadSavedSessionSnapshot(ctx, store, id, workspace)
		if !errors.Is(err, db.ErrTranscriptConflict) && !errors.Is(err, db.ErrCheckpointConflict) {
			return saved, err
		}
	}
	return nil, db.ErrTranscriptConflict
}

func loadSavedSessionSnapshot(ctx context.Context, store *db.Store, id, workspace string) (_ *savedSession, resultErr error) {
	if store == nil {
		return nil, errors.New("session store unavailable")
	}
	state, err := store.GetSessionResumeState(ctx, id)
	if err != nil {
		return nil, err
	}
	rec := state.Session
	if strings.HasPrefix(strings.TrimSpace(string(rec.ContextJSON)), "{") {
		defer func() {
			if resultErr != nil {
				resultErr = &exactSessionError{cause: resultErr}
			}
		}()
	}
	if rec.Workspace != workspace {
		return nil, fmt.Errorf("session %q belongs to workspace %q, not %q", id, rec.Workspace, workspace)
	}
	saved, err := decodeSavedSession(rec)
	if err != nil {
		return nil, err
	}
	saved.continuation = state.Branch != nil
	saved.contentVersion = state.ContentVersion
	if saved.exactHead != nil && saved.exactHead.InitialContinuation != (rewind.InitialContinuation{}) {
		if state.Branch == nil || !saved.exactHead.InitialContinuation.Matches(*state.Branch) {
			return nil, rewind.ErrInvalid
		}
	}
	if state.Branch != nil && (saved.exactHead == nil ||
		(saved.exactHead.SettledTurnID == "" && saved.exactHead.InitialContinuation == (rewind.InitialContinuation{}))) {
		return nil, rewind.ErrInvalid
	}
	if state.Transcript == nil {
		if saved.exactHead != nil {
			return nil, rewind.ErrInvalid
		}
		if rec.HasDraft {
			saved.Transcript = transcript.NewBuilder().Thread()
			return saved, nil
		}
		return nil, errors.New("session transcript not found")
	}
	storedTranscript := *state.Transcript
	if storedTranscript.FormatVersion > db.SessionTranscriptFormatVersion {
		return nil, fmt.Errorf("session transcript format %d is newer than supported format %d", storedTranscript.FormatVersion, db.SessionTranscriptFormatVersion)
	}
	checkpoint, strictErr := transcript.CheckpointFromRecords(storedTranscript.Revision, dbTranscriptRecords(storedTranscript.Entries))
	var thread transcript.Thread
	if strictErr == nil {
		thread, err = checkpoint.Restore()
		if err != nil {
			return nil, err
		}
	} else {
		if saved.exactHead != nil {
			return nil, fmt.Errorf("%w: %w; view history or inspect recovery options", rewind.ErrInvalid, strictErr)
		}
		// Legacy sessions remain browsable/resumable, but crash repair is never
		// used as proof of an exact historical settled boundary.
		thread, err = transcript.FromRecords(storedTranscript.Revision, dbTranscriptRecords(storedTranscript.Entries))
		if err != nil {
			return nil, fmt.Errorf("session transcript is corrupted: %w", err)
		}
	}
	if saved.exactHead != nil && saved.exactHead.Revision != storedTranscript.Revision {
		return nil, rewind.ErrInvalid
	}
	if saved.exactHead != nil {
		saved.settledTurnID = saved.exactHead.SettledTurnID
	}
	if thread.IsEmpty() && (storedTranscript.Revision != 0 || !rec.HasDraft || (string(rec.ContextJSON) != "[]" && saved.exactHead == nil)) {
		return nil, errors.New("session transcript is empty")
	}
	recovered, _ := thread.RecoverInterrupted()
	if recovered.Revision() > storedTranscript.Revision {
		update, updateErr := transcriptRecoveryUpdate(rec, storedTranscript.Revision, recovered)
		if updateErr != nil {
			return nil, updateErr
		}
		saved.contentVersion, updateErr = store.UpdateActiveTranscriptVersioned(ctx, update, saved.contentVersion)
		if updateErr != nil {
			// A competing write invalidates the entire read snapshot, including
			// context. Never combine it with a newer transcript; retry resume.
			return nil, fmt.Errorf("persist interrupted transcript recovery: %w", updateErr)
		}
		thread = recovered
	}
	saved.Transcript = thread
	return saved, nil
}

func decodeSavedSession(rec db.SessionRecord) (*savedSession, error) {
	var contextCache []llm.Message
	var exactHead *rewind.ResumeState
	if strings.HasPrefix(strings.TrimSpace(string(rec.ContextJSON)), "{") {
		state, err := rewind.DecodeResume(rec.ContextJSON)
		if err != nil {
			return nil, err
		}
		if state.Target.Provider != rec.Provider || state.Target.Model != rec.Model {
			return nil, rewind.ErrTarget
		}
		exactHead = &state
		contextCache = state.Context
	} else if len(rec.ContextJSON) > 0 {
		if err := json.Unmarshal(rec.ContextJSON, &contextCache); err != nil {
			return nil, fmt.Errorf("decode context cache: %w", err)
		}
		if err := rewind.ValidateLegacyContext(contextCache, rec.Provider); err != nil {
			return nil, fmt.Errorf("validate context cache: %w", err)
		}
	}
	s := &savedSession{
		sessionSummary: savedSessionSummary(rec),
		Context:        contextCache,
		exactHead:      exactHead,
	}
	if !decodeSessionBlob(rec.PlanJSON, &s.Plan) {
		if exactHead != nil {
			return nil, rewind.ErrInvalid
		}
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
		if exactHead != nil {
			return nil, rewind.ErrInvalid
		}
		s.rejectedDraftJSON = append([]byte(nil), rec.PendingJSON...)
		s.addRestoreDiagnostic(sessionRestoreDraftCorrupt)
	} else {
		s.DraftText = draftText
		s.DraftAttachments, err = draft.DecodeAttachments(rec.PendingJSON)
		if err != nil {
			return nil, fmt.Errorf("decode draft attachments: %w", err)
		}
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

// observeInitialActive captures source-selection ownership, not permission to
// overwrite whichever session happens to be active when a queued save executes.
func (m *UI) observeInitialActive() {
	m.initialActive, m.initialActiveErr = "", nil
	if m.settings == nil || m.settings.Store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(m.appContext(), sessionSaveCommandTTL)
	defer cancel()
	m.initialActive, m.initialActiveErr = m.settings.Store.GetSettingExact(ctx, m.settings.WorkspaceRoot(), activeSessionKey)
	if errors.Is(m.initialActiveErr, db.ErrNotFound) {
		m.initialActiveErr = nil
	}
}

func (m *UI) dismissIntroFresh(prompt string) tea.Cmd {
	m.observeInitialActive()
	m.intro = nil
	m.session.resetSession()
	m.draftGeneration++
	m.composer.setText("")
	m.pendingAttachments = nil
	m.rejectedDraftJSON = nil
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
		if errors.Is(err, transcript.ErrCheckpointUnsettled) {
			if inspectErr := m.OpenSavedSessionInspection(m.appContext(), id); inspectErr == nil {
				return nil
			}
		}
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
	if s != nil && s.exactHead != nil {
		if err := m.resumeExactSession(m.appContext(), s); err != nil {
			m.session.SetErrorToast("resume continuation: " + err.Error())
		}
		return m.toastExpiryCmd()
	}
	if m.liveOperation != nil || m.sessionRetry != nil {
		m.session.SetErrorToast("wait for the current turn to settle before switching sessions")
		return m.toastExpiryCmd()
	}
	if s == nil {
		return nil
	}
	m.intro = nil
	m.session.resetSession()
	m.draftGeneration++
	m.transcriptGeneration++
	m.resetTranscriptPersistence()
	m.sourceBaseline = s.contentVersion
	m.pendingAttachments = nil
	for _, part := range llm.CloneContentParts(s.DraftAttachments) {
		m.pendingAttachments = append(m.pendingAttachments, restoredAttachment(part))
	}
	m.session.SetIdentity(s.ID, s.Label, s.LabelManual, s.CreatedAt)
	m.initialActive, m.initialActiveErr = s.ID, nil
	if m.live != nil {
		m.live.RestoreContext(s.Context)
	}
	m.timeline.restoreThread(s.Transcript)
	m.transcriptPersisted = s.Transcript.Revision()
	m.transcriptPersistedSessionID = s.ID
	m.settledTurnID = s.settledTurnID
	m.rejectedDraftJSON = append(m.rejectedDraftJSON[:0], s.rejectedDraftJSON...)
	m.composer.setText(s.DraftText)
	m.durableDraftText = s.DraftText
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
	cmd := m.toastExpiryCmd()
	if useSavedTarget && s.Provider != "" && s.Model != "" {
		selection := prefs.ModelSelection{Provider: s.Provider, Model: s.Model}
		cmd = tea.Batch(cmd, m.switchTarget(selection, nil))
	}
	return cmd
}

type sessionSnapshot struct {
	record         db.SessionRecord
	transcript     db.TranscriptUpdate
	allEntries     []db.TranscriptEntry
	exact          bool
	history        *engine.LiveRunner       // borrowed until the full-save FIFO drains
	historyBatches []db.SessionHistoryBatch // owned immutable retry snapshot
}

func (m *UI) sessionSnapshot() (*sessionSnapshot, error) {
	if m.unsavedTurnError != nil {
		return nil, m.unsavedTurnError // only a newly settled turn may advance the exact head
	}
	if m.settings == nil || m.settings.Store == nil || m.live == nil {
		return nil, errSessionSnapshotEmpty
	}
	contextCache := m.live.ContextSnapshot()
	if len(contextCache) == 0 || m.timeline.transcriptThread().IsEmpty() {
		return nil, errSessionSnapshotEmpty
	}
	return m.sessionSnapshotWithContext(contextCache)
}

// sessionSnapshotWithContext also supports the initial empty BEFORE boundary.
// Normal shutdown/full saves retain sessionSnapshot's nonempty requirement.
func (m *UI) sessionSnapshotWithContext(contextCache []llm.Message) (*sessionSnapshot, error) {
	return m.sessionSnapshotForBoundary(contextCache, m.settledTurnID, m.settledWatermark, m.initialContinuation)
}

func (m *UI) sessionSnapshotForBoundary(contextCache []llm.Message, turnID string, watermark uint64, initial rewind.InitialContinuation) (*sessionSnapshot, error) {
	if m.settings == nil || m.settings.Store == nil || m.live == nil {
		return nil, errSessionSnapshotEmpty
	}
	m.session.EnsureIdentity(uuid.NewString(), time.Now())
	if contextCache == nil {
		contextCache = []llm.Message{}
	}

	thread := m.timeline.transcriptThread()
	target := m.live.RunTarget()
	contextJSON, err := json.Marshal(contextCache)
	if m.exactResume {
		// Admission checks the entire retained history, not just this turn's
		// delta. Serialize only the independently owned validated snapshot.
		checkpoint, captureErr := thread.CaptureCheckpoint()
		if captureErr != nil {
			return nil, fmt.Errorf("%w: %w", rewind.ErrInvalid, captureErr)
		}
		thread, err = checkpoint.Restore()
		if err != nil {
			return nil, err
		}
		contextJSON, err = rewind.EncodeResume(checkpoint.Revision(), contextCache, rewindTarget(target), turnID, watermark, initial)
	}
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
	pendingJSON, err := draft.EncodeWithAttachments(m.composer.text(), m.attachmentParts())
	if err != nil {
		return nil, fmt.Errorf("encode draft: %w", err)
	}
	if m.composer.text() == "" && len(m.rejectedDraftJSON) != 0 {
		pendingJSON = append([]byte(nil), m.rejectedDraftJSON...)
	}
	changedFileCount := len(m.session.WorkingSet.FilesChangedThisSession())
	planCompletedCount := 0
	for _, step := range m.session.Plan.Steps {
		if step.Status == code.StepStatuses.COMPLETED {
			planCompletedCount++
		}
	}

	messageCount := thread.MessageCount()
	provider, model := m.session.Provider, m.session.Model
	if m.exactResume {
		provider, model = target.Spec.Name, target.Model
	}
	transcriptUpdate, allEntries, err := m.transcriptUpdate(thread, m.persistedTranscriptRevision(m.session.ID))
	if err != nil {
		return nil, err
	}
	return &sessionSnapshot{record: db.SessionRecord{
		ID:                 m.session.ID,
		Workspace:          m.settings.WorkspaceRoot(),
		Label:              m.session.Label,
		LabelManual:        m.session.LabelManual,
		Provider:           provider,
		Model:              model,
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
	}, transcript: transcriptUpdate, allEntries: allEntries, exact: m.exactResume, history: m.live,
		historyBatches: m.live.RecordedHistory(m.session.ID)}, nil
}

func (s *sessionSnapshot) acknowledgeHistory() {
	if s.history != nil {
		s.history.AcknowledgeRecordedHistory(s.record.ID, s.historyBatches)
	}
}

func (s *sessionSnapshot) commitGuarded(ctx context.Context, store *db.Store, expected db.SessionContentVersion) error {
	if err := store.CommitCheckpointTurn(ctx, s.record, s.transcript, expected, s.historyBatches...); err != nil {
		return err
	}
	s.acknowledgeHistory()
	return nil
}

func saveSessionSnapshot(ctx context.Context, settings *engine.Settings, snapshot *sessionSnapshot) error {
	if settings == nil || settings.Store == nil || snapshot == nil {
		return nil
	}
	if err := settings.Store.CommitCompletedTurn(ctx, snapshot.record, snapshot.transcript, snapshot.historyBatches...); err != nil {
		return fmt.Errorf("save session snapshot: %w", err)
	}
	snapshot.acknowledgeHistory()
	return nil
}

func (m *UI) SaveSession(ctx context.Context) error {
	if m.sourceConflict {
		return fmt.Errorf("%s: %w", sourceConflictNotice, db.ErrCheckpointConflict)
	}
	if m.liveOperation != nil && (m.exactResume || m.durableDispatch()) {
		return nil // retain the last paired head until durable settlement
	}
	if m.rewindRecovery != "" {
		return errors.New("restart to recover committed continuation before saving")
	}
	snapshot, err := m.sessionSnapshot()
	if errors.Is(err, errSessionSnapshotEmpty) {
		return nil
	}
	if err != nil {
		return err
	}
	if m.settings == nil || m.settings.Store == nil || snapshot == nil {
		return nil
	}
	op := sessionPersistOp{kind: sessionPersistFull, snapshot: snapshot, guarded: snapshot.exact,
		sourceObserved: sourceObservation{version: m.sourceBaseline}, sourceWritten: new(atomic.Pointer[sourceWrite])}
	if err := executeVersionedSessionPersist(ctx, m.settings.Store, &op); err != nil {
		if errors.Is(err, db.ErrCheckpointConflict) {
			m.sourceConflict = true
		}
		return err
	}
	m.acknowledgeSource(&op)
	m.acknowledgeDurableDraft(&op)
	m.transcriptPersistedSessionID, m.transcriptPersisted = snapshot.record.ID, snapshot.transcript.Revision
	return nil
}

// FlushSessionPersistence completes queued writes in FIFO order, then writes
// the final resumable snapshot. It is called after the Bubble Tea loop stops,
// when no command can concurrently mutate the queue.
func (m *UI) FlushSessionPersistence(ctx context.Context) error {
	var firstErr error
	protectedInput := false
	// A timed-out flush retains queued reservation ownership for the lifecycle
	// closer's subsequent join; dependencies must remain open until then.
	for _, op := range m.sessionPersistQueue {
		if op.before != nil {
			op.before.stop()
		}
	}
	if m.sessionPersistRunning && m.sessionPersistCurrent != nil {
		if m.sessionPersistCurrent.cancel != nil {
			m.sessionPersistCurrent.cancel()
		}
		current := m.sessionPersistCurrent
		if current.before != nil {
			protectedInput = true
		}
		if current.claimed.CompareAndSwap(false, true) {
			// Bubble Tea may never start a returned command after it stops. Take
			// ownership synchronously; its late closure becomes a no-op.
			err := m.flushUnstartedSessionPersist(ctx, current)
			current.done <- sessionPersistedMsg{sessionID: current.sessionID(), revision: current.transcriptRevision(), err: err}
			close(current.done)
		}
		select {
		case result := <-m.sessionPersistCurrent.done:
			if result.err != nil {
				if result.sessionID == m.session.ID && errors.Is(result.err, db.ErrCheckpointConflict) {
					m.sourceConflict = true
				}
				firstErr = fmt.Errorf("flush in-flight persistence: %w", result.err)
			} else if result.sessionID == m.session.ID {
				m.acknowledgeSource(current)
				m.transcriptPersistedSessionID = result.sessionID
				if result.revision > m.transcriptPersisted {
					m.transcriptPersisted = result.revision
				}
			}
			if current.retry != nil {
				if result.err == nil && result.sessionID == m.session.ID {
					m.acknowledgeDurableDraft(current)
				}
				m.finishSessionRetry(current, result)
			}
			m.sessionPersistRunning = false
			m.sessionPersistCurrent = nil
		case <-ctx.Done():
			return fmt.Errorf("flush in-flight persistence: %w", ctx.Err())
		}
	}
	for len(m.sessionPersistQueue) != 0 {
		if err := ctx.Err(); err != nil {
			return errors.Join(firstErr, err)
		}
		op := m.sessionPersistQueue[0]
		m.sessionPersistQueue = m.sessionPersistQueue[1:]
		if (op.kind == sessionPersistTranscript || op.kind == sessionPersistFull) && op.sessionID() == m.transcriptPersistedSessionID {
			op.rebaseTranscript(m.transcriptPersisted)
			if op.kind == sessionPersistTranscript && op.transcriptRevision() <= m.transcriptPersisted {
				continue
			}
		}
		if op.before != nil {
			protectedInput = true
		}
		err := m.flushUnstartedSessionPersist(ctx, &op)
		if err != nil {
			if op.sessionID() == m.session.ID && errors.Is(err, db.ErrCheckpointConflict) {
				m.sourceConflict = true
			}
			if firstErr == nil {
				firstErr = fmt.Errorf("flush queued persistence: %w", err)
			}
		} else if revision := op.transcriptRevision(); op.sessionID() == m.session.ID {
			m.acknowledgeSource(&op)
			m.transcriptPersistedSessionID = op.sessionID()
			if revision > m.transcriptPersisted {
				m.transcriptPersisted = revision
			}
		}
		if op.retry != nil {
			if err == nil && op.sessionID() == m.session.ID {
				m.acknowledgeDurableDraft(&op)
			}
			m.finishSessionRetry(&op, sessionPersistedMsg{err: err})
		}
	}
	m.sessionPersistQueue = nil
	if protectedInput {
		return firstErr // do not overwrite the protected recovery prompt with an empty composer
	}
	// A successful retry advanced the trusted receipt and completed boundary.
	// Save any composer edits made after its capture; a failed retry still
	// blocks sessionSnapshot through unsavedTurnError.
	finalErr := m.SaveSession(ctx)
	if firstErr != nil && finalErr != nil {
		return errors.Join(firstErr, finalErr)
	}
	if finalErr != nil {
		return finalErr
	}
	return firstErr
}

// flushUnstartedSessionPersist runs only after shutdown has sole ownership of
// an unstarted operation. BEFORE work saves recovery state but never dispatches.
func (m *UI) flushUnstartedSessionPersist(ctx context.Context, op *sessionPersistOp) error {
	if m.sourceConflict && op.sessionID() == m.session.ID {
		if op.before != nil {
			op.before.reservation.Release()
		}
		return db.ErrCheckpointConflict
	}
	if op.before != nil {
		defer op.before.reservation.Release()
		return op.before.save(ctx, m.settings.Store, op.snapshot)
	}
	err := executeSessionPersist(ctx, m.settings, op)
	if err != nil && (op.kind == sessionPersistTranscript || op.kind == sessionPersistFull) {
		err = retrySessionTranscriptPersist(ctx, m.settings, op, err)
	}
	return err
}

func (m *UI) saveSessionCmd() tea.Cmd {
	if m.unsavedTurnError != nil || (m.liveOperation != nil && (m.exactResume || m.durableDispatch())) {
		return nil
	}
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
	if m.session.Run.Running || m.liveOperation != nil || m.sessionRetry != nil {
		m.session.SetErrorToast("stop current turn before clearing")
		return m.toastExpiryCmd()
	}
	if m.sourceConflict {
		m.session.SetErrorToast(sourceConflictNotice)
		return nil // never delete the competing source as conflict recovery
	}
	oldID := m.session.ID
	ctx, cancel := context.WithTimeout(m.appContext(), sessionSaveCommandTTL)
	defer cancel()
	source, sourceErr := m.observeSource(ctx, oldID)
	m.observeInitialActive()
	if m.initialActive == oldID {
		// The preceding FIFO deletion removes our active pointer. A foreign
		// pointer still fails the subsequent initial checkpoint's comparison.
		m.initialActive = ""
	}
	m.startupPrompt = ""
	m.startupAttachments = nil
	m.startupAttachmentMetadata = nil
	m.session.SetSubmittedAttachments(nil)
	m.draftGeneration++
	m.composer.setText("")
	m.pendingAttachments = nil
	m.rejectedDraftJSON = nil
	m.transcriptGeneration++
	m.resetTranscriptPersistence()
	if m.live != nil {
		m.live.ClearContext()
	}
	m.timeline.Clear()
	m.session.resetSession()
	m.session.SetSuccessToast("conversation cleared")
	return tea.Batch(m.toastExpiryCmd(), m.enqueueSessionPersist(sessionPersistOp{kind: sessionPersistDelete,
		generation: m.transcriptGeneration, oldID: oldID, sourceObserved: source, sourceErr: sourceErr, guarded: true}))
}

func clearPersistedSession(ctx context.Context, settings *engine.Settings, oldID string) error {
	if settings == nil {
		return nil
	}
	if oldID != "" && settings.Store != nil {
		if e := settings.Store.DeleteSession(ctx, oldID); e != nil {
			return fmt.Errorf("delete session: %w", e)
		}
	}
	return clearActiveSession(ctx, settings, oldID)
}

func clearActiveSession(ctx context.Context, settings *engine.Settings, oldID string) error {
	if settings == nil {
		return nil
	}
	if settings.Svc != nil {
		active, activeErr := settings.Svc.GetSetting(ctx, prefs.ScopeWorkspace, activeSessionKey)
		if activeErr == nil && active.Value == oldID {
			activeErr = settings.Svc.DeleteSetting(ctx, prefs.ScopeWorkspace, activeSessionKey)
		}
		if activeErr != nil && !errors.Is(activeErr, prefs.ErrNotFound) {
			return fmt.Errorf("clear active session: %w", activeErr)
		}
	}
	return nil
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
