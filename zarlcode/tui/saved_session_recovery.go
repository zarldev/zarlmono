package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zarlcode/draft"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/db"
)

type savedSessionInspection struct {
	id          string
	thread      transcript.Thread
	draft       string
	diagnostic  string
	checkpoints []db.CheckpointSummary
}

// OpenSavedSessionInspection opens history and recovery discovery without
// repairing records, changing active selection, building providers, or running
// tools. Unsupported canonical records are rejected without private diagnostics.
func (m *UI) OpenSavedSessionInspection(ctx context.Context, id string) error {
	if m.settings == nil || m.settings.Store == nil {
		return errors.New("session store unavailable")
	}
	state, err := m.settings.Store.GetSessionResumeState(ctx, id)
	if err != nil {
		return errors.New("saved history unavailable or integrity check rejected")
	}
	if state.Session.Workspace != m.settings.WorkspaceRoot() || state.Transcript == nil || state.Transcript.FormatVersion != db.SessionTranscriptFormatVersion {
		return rewind.ErrInvalid
	}
	records := dbTranscriptRecords(state.Transcript.Entries)
	thread, err := transcript.InspectRecords(state.Transcript.Revision, records)
	if err != nil {
		return err
	}
	inspection := savedSessionInspection{id: id, thread: thread, diagnostic: "Read-only saved history. No provider or tools have been started."}
	if _, err := transcript.CheckpointFromRecords(state.Transcript.Revision, records); errors.Is(err, transcript.ErrCheckpointUnsettled) {
		inspection.diagnostic = fmt.Sprintf("Saved transcript contains %d unfinished entries; exact resume is unavailable. The original history is preserved.", thread.UnfinishedEntryCount())
	} else if err != nil {
		return err
	}
	inspection.draft, err = draft.Decode(state.Session.PendingJSON)
	if err != nil {
		inspection.diagnostic += " Saved draft has an unsupported format; its bytes remain untouched."
	}
	inspection.checkpoints, err = m.settings.Store.ListSessionCheckpoints(ctx, id)
	if err != nil {
		inspection.diagnostic += " Checkpoint listing unavailable; retry after storage recovers."
	}
	m.overlay.push(&savedSessionRecoveryDialog{inspection: inspection})
	return nil
}

type savedSessionRecoveryDialog struct {
	inspection savedSessionInspection
	selecting  bool
	cursor     int
}

type actionViewSavedHistory struct {
	inspection savedSessionInspection
	draft      bool
}

func (actionViewSavedHistory) isAction() {}

type actionPreviewSavedRecovery struct{ sessionID, checkpointID string }

func (actionPreviewSavedRecovery) isAction() {}

type actionConfirmSavedRecovery struct{ selection savedRecoverySelection }

func (actionConfirmSavedRecovery) isAction() {}

type savedRecoverySelection struct {
	rewindSelection
	activeSession string
}

func (d *savedSessionRecoveryDialog) handleKey(msg tea.KeyPressMsg) action {
	switch msg.String() {
	case "esc", "q":
		return actionClose{}
	case "v":
		return actionViewSavedHistory{inspection: d.inspection}
	case "d":
		return actionViewSavedHistory{inspection: d.inspection, draft: true}
	case "r":
		d.selecting = true
	case "up", "k":
		d.cursor = max(0, d.cursor-1)
	case "down", "j":
		d.cursor = min(max(0, len(d.inspection.checkpoints)-1), d.cursor+1)
	case "enter":
		if d.selecting && len(d.inspection.checkpoints) > 0 {
			return actionPreviewSavedRecovery{sessionID: d.inspection.id, checkpointID: d.inspection.checkpoints[d.cursor].ID}
		}
	}
	return actionNone{}
}

func (d *savedSessionRecoveryDialog) draw(scr uv.Screen, area uv.Rectangle) {
	width := min(96, area.Dx())
	text := d.inspection.diagnostic + "\n\nView history or saved draft without activating this session.\nRecovery requires selecting and confirming a verified BEFORE checkpoint."
	if d.selecting {
		if len(d.inspection.checkpoints) == 0 {
			text += "\n\nNo verified continuation available. View or copy the original history."
		} else {
			c := d.inspection.checkpoints[d.cursor]
			text += fmt.Sprintf("\n\nCheckpoint %d of %d · boundary revision %d\n%s\nEnter inspects this candidate; it does not create a continuation.", d.cursor+1, len(d.inspection.checkpoints), c.SourceRevision, c.ID)
		}
	}
	lines := strings.Split(ansi.Wrap(text, max(1, width-4), ""), "\n")
	lay, ok := drawDialogPane(scr, area, "saved session inspection", width, min(area.Dy(), len(lines)+4), palette.Border, palette.Primary)
	if !ok {
		return
	}
	drawLine(scr, lay.Context, palette.Subtle.On("read-only · source preserved"))
	for i, line := range lines[:min(len(lines), lay.Body.Dy())] {
		drawLine(scr, uv.Rect(lay.Body.Min.X, lay.Body.Min.Y+i, lay.Body.Dx(), 1), line)
	}
	drawLine(scr, lay.Footer, palette.Muted.On("v history · d draft · r recovery · ↑/↓ choose · enter inspect · esc close"))
}

func (m *UI) viewSavedHistory(intent actionViewSavedHistory) {
	source := &timeline{thread: transcript.NewReducer()}
	if intent.draft {
		text := intent.inspection.draft
		if text == "" {
			text = "No readable saved draft."
		}
		source.addNotice(text)
	} else {
		source.restoreThread(intent.inspection.thread)
	}
	reader := newTranscriptReader(source)
	reader.readOnly = true
	m.overlay.push(reader)
}

func (m *UI) previewSavedRecovery(intent actionPreviewSavedRecovery) tea.Cmd {
	preview := &rewindPreviewDialog{}
	m.overlay.push(preview)
	ctx, cancel := context.WithTimeout(m.appContext(), sessionSaveCommandTTL)
	defer cancel()
	store := m.settings.Store
	version, err := store.SessionVersion(ctx, intent.sessionID)
	if err != nil {
		preview.unavailable = "Source state unavailable."
		return nil
	}
	state, err := store.GetSessionResumeState(ctx, intent.sessionID)
	if err != nil || state.Transcript == nil || state.Session.Workspace != m.settings.WorkspaceRoot() {
		preview.unavailable = "Source history unavailable or integrity check rejected."
		return nil
	}
	record, err := store.GetSessionCheckpoint(ctx, intent.sessionID, intent.checkpointID)
	if err != nil || record.Workspace != m.settings.WorkspaceRoot() || record.SourceRevision > state.Transcript.Revision {
		preview.unavailable = "Saved checkpoint is missing, expired, or corrupt."
		return nil
	}
	checkpoint, err := rewind.Load(ctx, store, record)
	if err != nil {
		preview.unavailable = "No verified continuation available from this checkpoint."
		return nil
	}
	snapshot, err := checkpoint.Snapshot()
	if err != nil {
		preview.unavailable = "Checkpoint unavailable."
		return nil
	}
	active, err := store.GetSettingExact(ctx, m.settings.WorkspaceRoot(), activeSessionKey)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		preview.unavailable = "Active selection unavailable."
		return nil
	}
	observed, err := store.SessionVersion(ctx, intent.sessionID)
	if err != nil || observed != version {
		preview.unavailable = "Source changed; reopen inspection."
		return nil
	}
	selection := savedRecoverySelection{rewindSelection: rewindSelection{
		sessionID: intent.sessionID, checkpointID: record.ID, checksum: record.Checksum, workspace: record.Workspace,
		revision: state.Transcript.Revision, promptID: snapshot.Boundary.PromptID, sourceVersion: version,
	}, activeSession: active}
	prefix, err := snapshot.Transcript.Records()
	if err != nil {
		preview.unavailable = "Checkpoint unavailable."
		return nil
	}
	for _, entry := range state.Transcript.Entries {
		if entry.Sequence > uint64(len(prefix)) && entry.Kind == transcript.EntryKinds.ENTRYUSERMESSAGE.String() && entry.EntryID != snapshot.Boundary.PromptID {
			selection.laterPrompts++
		}
	}
	preview.selection = selection.rewindSelection
	preview.recovery = &selection
	preview.prompt, preview.target, preview.boundaryRevision = snapshot.Boundary.PromptText, snapshot.Target, record.SourceRevision
	if snapshot.Boundary.HasAttachments && len(snapshot.Boundary.Attachments) == 0 {
		preview.unavailable = rewind.ErrAttachments.Error()
	}
	return nil
}

func (m *UI) confirmSavedRecovery(selection savedRecoverySelection) tea.Cmd {
	id, err := m.createSavedRecovery(selection)
	if err != nil {
		if preview, ok := m.overlay.top().(*rewindPreviewDialog); ok {
			preview.unavailable = "Recovery not created: " + err.Error() + ". Reopen inspection to retry."
		}
		return nil
	}
	for m.overlay.active() {
		m.overlay.pop()
	}
	m.intro.err = "Recovery child created; original preserved. Select the child to resume. No prompt or tools ran."
	sessions, listErr := listSavedSessions(m.appContext(), m.settings.Store, m.settings.WorkspaceRoot())
	if listErr != nil {
		m.intro.err = "Recovery child created and selected; restart with -continue to resume. Source preserved."
		return nil
	}
	m.intro.sessions = sessions
	m.intro.focus = introFocusSessions
	m.intro.refreshMatches("")
	for i, index := range m.intro.matches {
		if m.intro.sessions[index].ID == id {
			m.intro.cursor = i
			break
		}
	}
	return nil
}

func (m *UI) createSavedRecovery(selection savedRecoverySelection) (string, error) {
	if m.intro == nil || m.session.ID != "" || m.live == nil || m.liveOperation != nil || m.session.Run.Running || m.sessionPersistRunning || len(m.sessionPersistQueue) != 0 {
		return "", errors.New("restart at the session picker before recovery; current conversation remains loaded")
	}
	ctx, cancel := context.WithTimeout(m.appContext(), repointTimeout)
	defer cancel()
	record, err := m.settings.Store.GetSessionCheckpoint(ctx, selection.sessionID, selection.checkpointID)
	if err != nil {
		return "", errors.New("checkpoint unavailable")
	}
	if record.Checksum != selection.checksum || record.Workspace != selection.workspace || selection.workspace != m.settings.WorkspaceRoot() {
		return "", db.ErrCheckpointConflict
	}
	checkpoint, err := rewind.Load(ctx, m.settings.Store, record)
	if err != nil {
		return "", err
	}
	snapshot, err := checkpoint.Snapshot()
	if err != nil {
		return "", err
	}
	if snapshot.Boundary.PromptID != selection.promptID {
		return "", rewind.ErrInvalid
	}
	// Provider construction is intentionally deferred until explicit confirmation.
	target, err := m.buildRewindTarget(ctx, snapshot.Target)
	if err != nil {
		return "", err
	}
	reservation, err := m.live.ReserveRuntime()
	if err != nil {
		return "", errors.New("runtime is busy")
	}
	defer reservation.Release()
	if err := reservation.ValidateRestoreTarget(snapshot.Target, target); err != nil {
		return "", rewind.ErrTarget
	}
	childID := uuid.NewString()
	branch, err := checkpoint.PrepareBranch(childID, "Recovery continuation", selection.revision, snapshot.Target)
	if err != nil {
		return "", err
	}
	messages := append(llm.CloneMessages(snapshot.Context), llm.Message{Role: llm.RoleUser, Content: rewind.FilesUnchangedNotice})
	watermark := snapshot.Boundary.EventWatermark
	var initial rewind.InitialContinuation
	if snapshot.Boundary.SettledTurnID == "" {
		watermark = 0
		initial = rewind.InitialContinuation{CheckpointID: branch.CheckpointID, CheckpointChecksum: branch.CheckpointChecksum}
	}
	branch.Child.ContextJSON, err = rewind.EncodeResume(snapshot.Transcript.Revision(), messages, snapshot.Target, snapshot.Boundary.SettledTurnID, watermark, initial)
	if err != nil {
		return "", err
	}
	if err := m.settings.Store.CreateCheckpointRecoveryBranch(ctx, branch, selection.sourceVersion, selection.activeSession); err != nil {
		if errors.Is(err, db.ErrCheckpointConflict) {
			return "", db.ErrCheckpointConflict
		}
		return "", errors.New("storage rejected recovery; source preserved")
	}
	return childID, nil
}
