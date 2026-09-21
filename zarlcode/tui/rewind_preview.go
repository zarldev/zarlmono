package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/db"
)

// rewindSelection binds reader intent to canonical identity and the source
// generation at opening. Metadata is only discovery; preview reloads the payload.
type rewindSelection struct {
	sessionID       string
	revision        uint64
	generation      uint64
	promptID        string
	checkpointID    string
	checksum        string
	workspace       string
	laterPrompts    int
	runtimeChecksum string
	sourceVersion   db.SessionContentVersion
}

type actionPreviewRewind struct {
	selection   rewindSelection
	unavailable string
}

func (actionPreviewRewind) isAction() {}

func (m *UI) openTranscriptReader() {
	reader := newTranscriptReader(m.timeline)
	reader.rewindSource = rewindSelection{sessionID: m.session.ID,
		revision: m.timeline.transcriptThread().Revision(), generation: m.liveGeneration}
	if m.settings != nil {
		reader.rewindSource.workspace = m.settings.WorkspaceRoot()
	}
	reader.rewindUnavailable = "No saved checkpoint for this prompt (older, expired, or evicted history)."
	if m.settings != nil && m.settings.Store != nil && m.session.ID != "" {
		ctx, cancel := context.WithTimeout(m.appContext(), sessionSaveCommandTTL)
		defer cancel()
		records, err := m.settings.Store.ListSessionCheckpoints(ctx, m.session.ID)
		if err != nil {
			reader.rewindUnavailable = "Checkpoint listing unavailable; retry after storage recovers."
		} else {
			reader.rewindCandidates = make(map[string]db.CheckpointSummary, len(records))
			for _, record := range records {
				if record.Workspace == m.settings.WorkspaceRoot() {
					// Ambiguous bindings must not choose an arbitrary checkpoint.
					if _, exists := reader.rewindCandidates[record.BoundaryID]; exists {
						reader.rewindCandidates[record.BoundaryID] = db.CheckpointSummary{}
					} else {
						reader.rewindCandidates[record.BoundaryID] = record
					}
				}
			}
		}
	}
	m.overlay.push(reader)
}

func (r *transcriptReader) rewindIntent() action {
	selection := r.rewindSource
	if r.view.sel < 0 || r.view.sel >= len(r.view.items) {
		return actionPreviewRewind{unavailable: "Select a top-level user prompt with [ / ]."}
	}
	user, ok := r.view.items[r.view.sel].(*userItem)
	if !ok {
		return actionPreviewRewind{unavailable: "Select a top-level user prompt with [ / ]. Queued or nested input is not a rewind boundary."}
	}
	selection.promptID = user.entryID
	record, found := r.rewindCandidates[user.entryID]
	if !found {
		return actionPreviewRewind{unavailable: r.rewindUnavailable}
	}
	if record.ID == "" {
		return actionPreviewRewind{unavailable: "Checkpoint binding is ambiguous."}
	}
	selection.checkpointID, selection.checksum = record.ID, record.Checksum
	for _, item := range r.view.items[r.view.sel+1:] {
		switch item.(type) {
		case *userItem, *queuedUserItem:
			selection.laterPrompts++
		}
	}
	return actionPreviewRewind{selection: selection}
}

func (m *UI) previewRewind(intent actionPreviewRewind) tea.Cmd {
	preview := &rewindPreviewDialog{selection: intent.selection, unavailable: intent.unavailable}
	m.overlay.push(preview)
	if preview.unavailable != "" {
		return nil
	}
	selection := intent.selection
	if selection.sessionID != m.session.ID || selection.generation != m.liveGeneration || selection.revision != m.timeline.transcriptThread().Revision() {
		preview.unavailable = "Conversation changed since this reader opened; reopen Ctrl-R."
		return nil
	}
	ctx, cancel := context.WithTimeout(m.appContext(), sessionSaveCommandTTL)
	defer cancel()
	record, err := m.settings.Store.GetSessionCheckpoint(ctx, selection.sessionID, selection.checkpointID)
	if err != nil {
		preview.unavailable = "Saved checkpoint is missing, expired, or corrupt."
		return nil
	}
	if record.Checksum != selection.checksum || record.Workspace != m.settings.WorkspaceRoot() || record.SourceRevision > selection.revision {
		preview.unavailable = "Saved checkpoint changed; reopen Ctrl-R."
		return nil
	}
	checkpoint, err := rewind.Load(ctx, m.settings.Store, record)
	if err != nil {
		preview.unavailable = "Saved checkpoint cannot be restored exactly."
		return nil
	}
	snapshot, err := checkpoint.Snapshot()
	if err != nil || snapshot.Boundary.PromptID != selection.promptID {
		preview.unavailable = "Saved checkpoint does not match the selected prompt."
		return nil
	}
	preview.prompt, preview.target, preview.boundaryRevision = snapshot.Boundary.PromptText, snapshot.Target, record.SourceRevision
	if snapshot.Boundary.HasAttachments && len(snapshot.Boundary.Attachments) == 0 {
		preview.unavailable = rewind.ErrAttachments.Error()
	} else if err := m.rewindReady(); err != nil {
		preview.unavailable = err.Error()
	} else {
		target, err := m.buildRewindTarget(ctx, snapshot.Target)
		if err != nil {
			preview.unavailable = err.Error()
			return nil
		}
		reservation, err := m.live.ReserveRuntime()
		if err != nil {
			preview.unavailable = err.Error()
			return nil
		}
		defer reservation.Release()
		if err := reservation.ValidateRestoreTarget(snapshot.Target, target); err != nil {
			preview.unavailable = err.Error()
			return nil
		}
		preview.selection.sourceVersion = m.sourceBaseline
		preview.selection.runtimeChecksum, err = runtimeFingerprint(reservation)
		if err != nil {
			preview.unavailable = err.Error()
		}
	}
	preview.holdDraft = preview.unavailable == ""
	return nil
}

// An actionable preview starts only after the FIFO is idle. Hold subsequent
// debounced writes until cancel or successful apply, including after a failed
// apply. Apply preserves the current composer on the source transactionally;
// cancelling the dialog schedules a fresh draft save.
func (m *UI) actionableRewindPreview() bool {
	for _, dialog := range m.overlay.stack {
		if preview, ok := dialog.(*rewindPreviewDialog); ok && preview.holdDraft {
			return true
		}
	}
	return false
}

type rewindPreviewDialog struct {
	selection        rewindSelection
	prompt           string
	target           rewind.Target
	boundaryRevision uint64
	unavailable      string
	scroll           int
	width            int
	height           int
	lineCount        int
	reviewed         bool
	holdDraft        bool
	recovery         *savedRecoverySelection // explicit saved-session recovery, no live rewind
}

func (d *rewindPreviewDialog) handleKey(msg tea.KeyPressMsg) action {
	switch msg.String() {
	case "esc", "q":
		return actionClose{}
	case "up", "k":
		d.scrollLines(-1)
	case "down", "j":
		d.scrollLines(1)
	case "pgup":
		d.scrollLines(-max(1, d.height-4))
	case "pgdown":
		d.scrollLines(max(1, d.height-4))
	case "home", "g":
		d.scroll = 0
	case "end", "G":
		d.scroll = max(0, d.lineCount-max(1, d.height-4))
	case "enter":
		if d.unavailable != "" {
			return actionClose{}
		}
		if d.reviewed {
			if d.recovery != nil {
				return actionConfirmSavedRecovery{selection: *d.recovery}
			}
			return actionApplyRewind{selection: d.selection}
		}
	}
	return actionNone{}
}

func (d *rewindPreviewDialog) scrollLines(delta int) {
	d.scroll = min(max(0, d.scroll+delta), max(0, d.lineCount-max(1, d.height-4)))
}

func (d *rewindPreviewDialog) draw(scr uv.Screen, area uv.Rectangle) {
	width, height := min(96, area.Dx()), area.Dy()
	if d.width != width || d.height != height {
		d.width, d.height, d.scroll, d.reviewed = width, height, 0, false
	}
	paragraphs := []string{
		"Files were not restored.",
		rewind.FilesUnchangedNotice,
		"",
		"Create a new continuation before the selected prompt.",
		"The original conversation, later history, and existing draft are preserved.",
		"The selected text will be prefilled for editing, not submitted.",
	}
	if d.recovery != nil {
		paragraphs = append(paragraphs, "Recovery creates and selects a separate saved child. It does not start a provider turn or submit the draft. Resume the child separately.", "Provider availability is checked only after confirmation.")
	}
	if d.unavailable != "" {
		paragraphs = append([]string{"Unavailable: " + d.unavailable, ""}, paragraphs...)
	}
	if d.selection.checkpointID != "" {
		paragraphs = append(paragraphs,
			fmt.Sprintf("Later prompts retained on original: %d (plus the selected prompt).", d.selection.laterPrompts),
			"Checkpoint: "+d.selection.checkpointID,
			fmt.Sprintf("Boundary revision: %d · source head: %d", d.boundaryRevision, d.selection.revision))
	}
	if d.target.Provider != "" {
		paragraphs = append(paragraphs, "Saved target: "+providerModelLabel(d.target.Provider, d.target.Model), "Prompt: "+d.prompt)
	}
	lines := strings.Split(ansi.Wrap(strings.Join(paragraphs, "\n"), max(1, width-4), ""), "\n")
	d.lineCount = len(lines)
	lay, ok := drawDialogPane(scr, area, "conversation rewind", width, min(height, len(lines)+4), palette.Border, palette.Primary)
	if !ok {
		return
	}
	d.scroll = min(d.scroll, max(0, len(lines)-lay.Body.Dy()))
	if d.scroll+lay.Body.Dy() >= len(lines) {
		d.reviewed = true
	}
	drawLine(scr, lay.Context, palette.Subtle.On("preview · conversation only"))
	for i, line := range lines[d.scroll:min(len(lines), d.scroll+lay.Body.Dy())] {
		drawLine(scr, uv.Rect(lay.Body.Min.X, lay.Body.Min.Y+i, lay.Body.Dx(), 1), line)
	}
	footer := "↓/↑ review · esc cancel"
	if d.unavailable != "" {
		footer = "↓/↑ scroll · enter/esc back"
	} else if d.reviewed {
		footer = "enter create · esc cancel · ↑/↓ scroll"
	}
	drawLine(scr, lay.Footer, palette.Muted.On(footer))
}
