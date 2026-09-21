package tui

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/db"
)

type actionApplyRewind struct{ selection rewindSelection }

func (actionApplyRewind) isAction() {}

func rewindTarget(target engine.RunTarget) rewind.Target {
	return rewind.Target{Provider: target.Spec.Name, Model: target.Model, Window: target.Window,
		Reserve: target.Reserve, PlanMode: target.Plan, CodexEffort: target.Spec.CodexEffort}
}

// A bounded synchronous transition keeps the Update loop the sole owner. No
// goroutine, queued command, or shutdown flush can publish half a branch.
func (m *UI) rewindReady() error {
	if m.live == nil || m.settings == nil || m.settings.Store == nil {
		return errors.New("live session storage unavailable")
	}
	if m.sourceConflict {
		return errors.New(sourceConflictNotice)
	}
	if m.rewindRecovery != "" {
		return errors.New("restart to recover the committed continuation")
	}
	if m.liveOperation != nil || m.session.Run.Running || m.sessionPersistRunning || len(m.sessionPersistQueue) != 0 {
		return errors.New("wait for the current turn and session writes to settle")
	}
	if len(m.pendingAttachments) != 0 {
		return errors.New("remove pending attachments before rewinding; they cannot be durably preserved")
	}
	if m.pendingTarget != nil {
		return errors.New("finish the pending provider transition first")
	}
	if m.persistedTranscriptRevision(m.session.ID) != m.timeline.transcriptThread().Revision() {
		return errors.New("wait for the conversation head to be saved, then reopen Ctrl-R")
	}
	return nil
}

func (m *UI) loadRewindSelection(ctx context.Context, selection rewindSelection) (rewind.Checkpoint, error) {
	if selection.sessionID != m.session.ID || selection.generation != m.liveGeneration ||
		selection.revision != m.timeline.transcriptThread().Revision() || selection.workspace != m.settings.WorkspaceRoot() {
		return rewind.Checkpoint{}, db.ErrCheckpointConflict
	}
	record, err := m.settings.Store.GetSessionCheckpoint(ctx, selection.sessionID, selection.checkpointID)
	if err != nil {
		return rewind.Checkpoint{}, err
	}
	if record.Checksum != selection.checksum || record.Workspace != selection.workspace || record.SourceRevision > selection.revision {
		return rewind.Checkpoint{}, db.ErrCheckpointConflict
	}
	checkpoint, err := rewind.Load(ctx, m.settings.Store, record)
	if err != nil {
		return rewind.Checkpoint{}, err
	}
	snapshot, err := checkpoint.Snapshot()
	if err != nil {
		return rewind.Checkpoint{}, err
	}
	if snapshot.Boundary.PromptID != selection.promptID {
		return rewind.Checkpoint{}, db.ErrCheckpointConflict
	}
	if snapshot.Boundary.HasAttachments && len(snapshot.Boundary.Attachments) == 0 {
		return rewind.Checkpoint{}, rewind.ErrAttachments
	}
	return checkpoint, nil
}

func runtimeFingerprint(reservation *engine.RuntimeReservation) (string, error) {
	messages, target, err := reservation.Snapshot()
	if err != nil {
		return "", err
	}
	plan, err := reservation.PlanSnapshot()
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(struct {
		Context []llm.Message `json:"context"`
		Target  rewind.Target `json:"target"`
		Plan    any           `json:"plan"`
	}{messages, target, plan})
	if err != nil {
		return "", rewind.ErrInvalid
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

func (m *UI) buildRewindTarget(ctx context.Context, saved rewind.Target) (engine.RunTarget, error) {
	target := m.live.RunTarget() // retain today's tool/security/iteration policy
	spec := engine.ProviderSpec{Name: saved.Provider, Model: saved.Model, CodexEffort: saved.CodexEffort}
	// Embedders may supply their provider directly without a registry. Such a
	// provider can only restore its own already-built route, never another target.
	if m.settings.Registry == nil {
		if target.Spec.Name != spec.Name || target.Spec.Model != spec.Model || target.Spec.BaseURL != "" || target.Spec.CodexEffort != spec.CodexEffort {
			return engine.RunTarget{}, rewind.ErrTarget
		}
	} else {
		provider, err := engine.BuildProvider(ctx, m.settings.Registry, m.settings.Svc, spec)
		if err != nil {
			return engine.RunTarget{}, errors.New("saved provider/model unavailable with current credentials")
		}
		target.Provider = provider
	}
	target.Spec, target.Model = spec, saved.Model
	target.Window, target.Reserve, target.Plan = saved.Window, saved.Reserve, saved.PlanMode
	return target, nil
}

func (m *UI) applyRewind(selection rewindSelection) tea.Cmd {
	if err := m.commitRewind(selection); err != nil {
		if m.overlay.active() {
			if dialog, ok := m.overlay.top().(*rewindPreviewDialog); ok {
				dialog.unavailable = err.Error()
			}
		}
		m.session.SetErrorToast("rewind: " + err.Error())
		return m.toastExpiryCmd()
	}
	return tea.Batch(m.saveSessionCmd(), m.toastExpiryCmd())
}

func (m *UI) commitRewind(selection rewindSelection) error {
	if err := m.rewindReady(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(m.appContext(), repointTimeout)
	defer cancel()
	checkpoint, err := m.loadRewindSelection(ctx, selection)
	if err != nil {
		return err
	}
	snapshot, err := checkpoint.Snapshot()
	if err != nil {
		return err
	}
	target, err := m.buildRewindTarget(ctx, snapshot.Target)
	if err != nil {
		return err
	}
	reservation, err := m.live.ReserveRuntime()
	if err != nil {
		return err
	}
	defer reservation.Release()
	fingerprint, err := runtimeFingerprint(reservation)
	if err != nil {
		return err
	}
	if fingerprint != selection.runtimeChecksum {
		return errors.New("runtime changed since preview; reopen Ctrl-R")
	}
	if err := reservation.ValidateRestoreTarget(snapshot.Target, target); err != nil {
		return err
	}
	// All FIFO results have been applied. Save the current context and draft
	// without changing canonical bytes, under an authoritative source-head check.
	sourceContext, _, err := reservation.Snapshot()
	if err != nil {
		return err
	}
	source, err := m.sessionSnapshotWithContext(sourceContext)
	if err != nil {
		return err
	}
	version, err := m.settings.Store.SaveCheckpointSourceVersioned(ctx, source.record, selection.revision, selection.sourceVersion)
	if err != nil {
		if errors.Is(err, db.ErrCheckpointConflict) {
			m.sourceConflict = true
		}
		return err
	}
	m.sourceBaseline = version
	childID := uuid.NewString()
	branch, err := checkpoint.PrepareBranch(childID, "Continuation · "+m.session.Label, selection.revision, snapshot.Target)
	if err != nil {
		return err
	}
	// Preserve the exact historical prefix, then append truthful new metadata.
	messages := append(llm.CloneMessages(snapshot.Context), llm.Message{Role: llm.RoleUser, Content: rewind.FilesUnchangedNotice})
	watermark := snapshot.Boundary.EventWatermark
	var initial rewind.InitialContinuation
	if snapshot.Boundary.SettledTurnID == "" {
		watermark = 0
		initial = rewind.InitialContinuation{CheckpointID: branch.CheckpointID, CheckpointChecksum: branch.CheckpointChecksum}
	}
	branch.Child.ContextJSON, err = rewind.EncodeResume(snapshot.Transcript.Revision(), messages, snapshot.Target, snapshot.Boundary.SettledTurnID, watermark, initial)
	if err != nil {
		return err
	}
	thread, err := snapshot.Transcript.Restore()
	if err != nil {
		return err
	}
	childVersion, err := m.settings.Store.CreateCheckpointBranchVersioned(ctx, branch, version)
	if err != nil {
		return err
	}
	if err := reservation.RestoreConversation(messages, snapshot.Target, snapshot.Plan, target); err != nil {
		m.rewindRecovery = childID
		m.SetStartupFailure(selection.workspace, "Continuation saved; restart required", "The child branch is durable and active. Restart with -continue to restore it. The source was preserved.")
		return errors.New("child committed but runtime restoration was rejected; restart with -continue")
	}
	saved := &savedSession{sessionSummary: sessionSummary{ID: childID, Label: branch.Child.Label, CreatedAt: time.Now()},
		Transcript: thread, Context: messages, Plan: snapshot.Plan, DraftText: snapshot.Boundary.PromptText, settledTurnID: snapshot.Boundary.SettledTurnID,
		DraftAttachments: llm.CloneContentParts(snapshot.Boundary.Attachments),
		contentVersion:   childVersion,
		continuation:     true, exactHead: &rewind.ResumeState{Version: 1, Revision: snapshot.Transcript.Revision(),
			Context: messages, Target: snapshot.Target, SettledTurnID: snapshot.Boundary.SettledTurnID, EventWatermark: watermark, InitialContinuation: initial}}
	m.publishExactSession(saved, target)
	m.session.SetToastTone("New continuation created; original history preserved. Files unchanged.", toastWarn)
	return nil
}

func (m *UI) publishExactSession(saved *savedSession, target engine.RunTarget) {
	for m.overlay.active() {
		m.overlay.pop()
	}
	m.intro = nil
	m.session.resetSession()
	m.prRefreshPending = false
	m.liveGeneration++
	atomic.AddUint64(&m.repointSeq, 1) // old provider-build results cannot repoint the child
	m.draftGeneration++
	m.transcriptGeneration++
	m.resetTranscriptPersistence()
	m.sourceBaseline = saved.contentVersion
	m.exactResume = true
	m.session.SetIdentity(saved.ID, saved.Label, saved.LabelManual, saved.CreatedAt)
	m.initialActive, m.initialActiveErr = saved.ID, nil
	m.timeline.restoreThread(saved.Transcript)
	m.transcriptPersisted, m.transcriptPersistedSessionID = saved.Transcript.Revision(), saved.ID
	m.settledTurnID = saved.settledTurnID
	m.settledWatermark = saved.exactHead.EventWatermark
	m.initialContinuation = saved.exactHead.InitialContinuation
	m.pendingAttachments, m.rejectedDraftJSON = nil, nil
	for _, part := range llm.CloneContentParts(saved.DraftAttachments) {
		m.pendingAttachments = append(m.pendingAttachments, restoredAttachment(part))
	}
	m.composer.setText(saved.DraftText)
	m.durableDraftText = saved.DraftText
	m.resetInputHistoryBrowse()
	m.session.Plan = saved.Plan
	m.session.workingSet().RestoreDiffBodies(saved.DiffBodies, saved.SavedAt)
	m.session.Run.RestoreUsage(saved.Usage)
	m.session.SetActiveProviderSpec(target.Spec)
	m.session.ApplyProviderCostBasis(target.Spec)
	m.session.PlanMode = target.Plan
	m.session.SetContextWindow(target.Window)
	m.SetPressureConfig(target.Window, target.Reserve)
	m.appliedReasoning, m.appliedWindow = activeProviderPolicy(m.settings, target.Spec.Name)
	if !saved.continuation {
		return
	}
	// Initial child commit contains the immutable prefix only. The continuation
	// notice is new history, saved by the ordinary FIFO; restart repeats it only
	// when that save did not happen.
	found := false
	for _, entry := range saved.Transcript.Entries() {
		if entry.Kind == transcript.EntryKinds.ENTRYNOTICE && entry.Payload.Text == rewind.FilesUnchangedNotice {
			found = true
			break
		}
	}
	if !found {
		m.timeline.addNotice(rewind.FilesUnchangedNotice)
	}
}

func (m *UI) resumeExactSession(ctx context.Context, saved *savedSession) error {
	if m.live == nil || m.liveOperation != nil || m.sessionPersistRunning || len(m.sessionPersistQueue) != 0 {
		return engine.ErrRuntimeBusy
	}
	ctx, cancel := context.WithTimeout(ctx, repointTimeout)
	defer cancel()
	target, err := m.buildRewindTarget(ctx, saved.exactHead.Target)
	if err != nil {
		return err
	}
	reservation, err := m.live.ReserveRuntime()
	if err != nil {
		return err
	}
	defer reservation.Release()
	if err := reservation.ValidateRestoreTarget(saved.exactHead.Target, target); err != nil {
		return err
	}
	if err := m.settings.Svc.SetSetting(ctx, prefs.ScopeWorkspace, activeSessionKey, saved.ID); err != nil {
		return err
	}
	if err := reservation.RestoreConversation(saved.Context, saved.exactHead.Target, saved.Plan, target); err != nil {
		m.rewindRecovery = saved.ID
		m.SetStartupFailure(m.settings.WorkspaceRoot(), "Continuation selected; restart required", "The durable continuation is active but runtime restoration was rejected. Restart with -continue after restoring its provider availability.")
		return err
	}
	m.publishExactSession(saved, target)
	if saved.continuation {
		m.session.SetToastTone("Resumed continuation. Files unchanged; recheck workspace assumptions.", toastWarn)
	} else {
		m.session.SetSuccessToast("Resumed exact conversation.")
	}
	return nil
}
