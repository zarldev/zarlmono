package tui

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
)

// ErrWorkbenchBusy means a command cannot be admitted until the current
// operation and its persistence have settled.
var ErrWorkbenchBusy = errors.New("session is busy")

// WorkbenchEntry is a bounded view of a canonical transcript occurrence.
type WorkbenchEntry struct {
	ID       string             `json:"id"`
	ParentID string             `json:"parentId"`
	TurnID   string             `json:"turnId"`
	Kind     string             `json:"kind"`
	Payload  transcript.Payload `json:"payload"`
}

// WorkbenchSnapshot describes actual execution and persistence, independently
// of terminal rendering. Display truncation never modifies the durable history.
type WorkbenchSnapshot struct {
	Revision        string               `json:"revision"`
	SessionID       string               `json:"sessionId"`
	Workspace       string               `json:"workspace"`
	Provider        string               `json:"provider"`
	Model           string               `json:"model"`
	Mode            string               `json:"mode"`
	State           string               `json:"state"`
	Error           string               `json:"error"`
	Draft           string               `json:"draft"`
	Entries         []WorkbenchEntry     `json:"entries"`
	Omitted         int                  `json:"omitted"`
	Unchanged       bool                 `json:"unchanged"`
	RetrySaveReason string               `json:"retrySaveReason"`
	ExportPath      string               `json:"exportPath"`
	RecoveryError   string               `json:"recoveryError"`
	Usage           SessionUsageSnapshot `json:"usage"`
}

// WorkbenchCommand is a frontend request. Supported actions are submit, stop,
// mode, model, retry-save and snapshot. It is parsed at the owner boundary.
type WorkbenchCommand struct {
	Action   string `json:"action"`
	Text     string `json:"text"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Since    string `json:"since"`
}

type workbenchRequest struct {
	command WorkbenchCommand
	reply   chan workbenchReply
}
type workbenchReply struct {
	snapshot WorkbenchSnapshot
	err      error
}

// Workbench serializes desktop commands through the exact TUI controller and
// Tea command scheduler. It never exposes mutable UI state to callers.
type Workbench struct {
	program *tea.Program
	done    chan struct{}
	ui      *UI
}

// NewWorkbench prepares a detached session. Run must be called once and joined
// before the Launch composition's resources are closed.
func (z *Zarlcode) NewWorkbench(ctx context.Context) *Workbench {
	return NewWorkbench(ctx, z.model, z.sink)
}

// NewWorkbench adapts an assembled session and its live sink for nonterminal
// use. The caller retains all dependency ownership and must join Run before
// closing them. Only this facade may access model after Run starts.
func NewWorkbench(ctx context.Context, model *UI, sink *teasink.Sink) *Workbench {
	w := &Workbench{done: make(chan struct{}), ui: model}
	w.program = tea.NewProgram(&workbenchModel{ui: model}, tea.WithContext(ctx), tea.WithInput(strings.NewReader("")), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithoutSignalHandler())
	sink.SetSend(w.program.Send)
	return w
}

// Run owns the detached event loop and final session persistence. Close must
// be requested while the loop is alive so cancellation can settle normally.
func (w *Workbench) Run(ctx context.Context) error {
	defer close(w.done)
	_, err := w.program.Run()
	flushErr := w.ui.FlushSessionPersistence(ctx)
	return errors.Join(err, flushErr)
}

// Request sends a command to the state owner and waits for admission/application,
// not LLM completion. Context cancellation only cancels this wait, not a turn.
func (w *Workbench) Request(ctx context.Context, command WorkbenchCommand) (WorkbenchSnapshot, error) {
	reply := make(chan workbenchReply, 1)
	w.program.Send(workbenchRequest{command: command, reply: reply})
	select {
	case response := <-reply:
		return response.snapshot, response.err
	case <-ctx.Done():
		return WorkbenchSnapshot{}, ctx.Err()
	case <-w.done:
		return WorkbenchSnapshot{}, errors.New("session closed")
	}
}

// Close cancels active work, lets the existing controller drain and save it,
// then waits for Run. The caller keeps shared resources open until this returns.
func (w *Workbench) Close() { w.program.Send(workbenchClose{}); <-w.done }

type workbenchClose struct{}
type workbenchModel struct {
	ui            *UI
	closing       bool
	cancelling    bool
	switching     bool
	exporting     bool
	exportPath    string
	recoveryError string
}

func (m *workbenchModel) Init() tea.Cmd {
	return tea.Batch(m.ui.startupCmd, m.ui.startupMCPCmd, m.ui.transcriptPersistenceCmd(), m.ui.initialModeTarget())
}
func (m *workbenchModel) stop() {
	if m.ui.isSwarmOpen() {
		m.ui.stopForegroundSwarm()
		return
	}
	m.cancelling = m.ui.liveOperation != nil
	// beforeTurn.stop is a teardown-only operation: it suppresses the FIFO
	// completion marker. Keep that marker flowing while this owner is alive;
	// if admission is pending, cancel again when its start event is applied.
	m.ui.live.CancelTurn()
}
func (m *workbenchModel) View() tea.View { return tea.NewView("") }
func (m *workbenchModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	var command tea.Cmd
	switch msg := message.(type) {
	case workbenchClose:
		m.closing = true
		m.stop()
		if m.ui.composer.text() != m.ui.durableDraftText {
			command = m.ui.enqueueDraftPersist(m.ui.draftGeneration)
		}
	case workbenchRequest:
		command, err := m.apply(msg.command)
		msg.reply <- workbenchReply{snapshot: m.snapshot(msg.command.Since), err: err}
		return m, command
	case sessionExportedMsg:
		m.exporting = false
		m.exportPath = msg.Path
		m.recoveryError = ""
	case sessionExportFailedMsg:
		m.exporting = false
		m.recoveryError = "Export failed; check the workspace export directory is writable."
	default:
		_, command = m.ui.Update(message)
	}
	if m.cancelling && m.ui.liveOperation != nil {
		m.ui.live.CancelTurn()
	}
	if m.ui.liveOperation == nil {
		m.cancelling = false
	}
	if m.closing && !m.active() {
		return m, tea.Quit
	}
	return m, command
}
func (m *workbenchModel) busy() bool {
	return m.closing || m.active()
}
func (m *workbenchModel) active() bool {
	return m.exporting || m.switching || m.ui.liveOperation != nil || m.ui.session.Run.Running || m.ui.sessionPersistRunning || len(m.ui.sessionPersistQueue) != 0 || m.ui.pendingTarget != nil || m.ui.sessionRetry != nil || m.ui.modeTarget.building
}

// A nil tea.Cmd means synchronous success; it is not an invalid result.
//
//nolint:nilnil // Bubble Tea's command contract explicitly permits nil.
func (m *workbenchModel) apply(command WorkbenchCommand) (tea.Cmd, error) {
	switch command.Action {
	case "snapshot":
		return nil, nil
	case "stop":
		m.stop()
		return nil, nil
	case "draft":
		if m.closing {
			return nil, ErrWorkbenchBusy
		}
		if len(command.Text) > 256*1024 {
			return nil, errors.New("draft exceeds 262144 bytes")
		}
		m.ui.composer.setText(command.Text)
		return m.ui.scheduleDraftSave(), nil
	case "submit":
		text := strings.TrimSpace(command.Text)
		if text == "" || len(text) > 256*1024 {
			return nil, errors.New("prompt must contain 1–262144 bytes")
		}
		if m.ui.isSwarmOpen() {
			return nil, ErrWorkbenchBusy
		}
		if m.busy() {
			return nil, ErrWorkbenchBusy
		}
		if m.ui.unsavedTurnError != nil || m.ui.sourceConflict || m.ui.rewindRecovery != "" {
			return nil, errors.New("save the pending session before submitting")
		}
		m.ui.composer.setText("")
		m.ui.rememberInput(text)
		return m.ui.runLiveTurn(text, nil), nil
	case "retry-save":
		if m.busy() {
			return nil, ErrWorkbenchBusy
		}
		if reason := m.ui.sessionSaveRetryUnavailable(); reason != "" {
			return nil, errors.New(reason)
		}
		return m.ui.retrySessionSave(), nil
	case "export-recovery":
		if m.busy() {
			return nil, ErrWorkbenchBusy
		}
		m.exporting = true
		m.recoveryError = ""
		m.exportPath = ""
		directory, filename, avoidCollision := sessionExportTarget(m.ui.session.WorkspaceDir, "", m.ui.session.Label, m.ui.session.ID)
		content := transcript.Markdown(transcript.MarkdownMetadata{Title: m.ui.session.Label, SessionID: m.ui.session.ID, Workspace: m.ui.session.WorkspaceDir}, m.ui.timeline.transcriptThread())
		if draft := m.ui.composer.text(); draft != "" {
			content += "\n\n## Unsent draft\n\n" + draft + "\n"
		}
		return writeSessionExportCmd(directory, filename, content, avoidCollision), nil
	case "mode":
		if m.busy() {
			return nil, ErrWorkbenchBusy
		}
		if command.Text != "plan" && command.Text != "build" {
			return nil, errors.New("mode must be plan or build")
		}
		if m.ui.isSwarmOpen() {
			return nil, ErrWorkbenchBusy
		}
		if m.ui.live.AppliedMode().Plan == (command.Text == "plan") && m.ui.modeTarget.err == nil {
			return nil, nil
		}
		m.ui.live.SetPlanMode(command.Text == "plan")
		m.ui.session.PlanMode = m.ui.live.AppliedMode().Plan
		mode := m.ui.live.AppliedMode()
		return m.ui.requestModeTarget(mode.Plan, mode.Generation), nil
	case "model":
		if m.busy() {
			return nil, ErrWorkbenchBusy
		}
		if command.Provider == "" || command.Model == "" || len(command.Provider)+len(command.Model) > 512 {
			return nil, errors.New("provider and model are required")
		}
		if m.ui.isSwarmOpen() {
			return nil, ErrWorkbenchBusy
		}
		m.switching = true
		return m.ui.switchTarget(prefs.ModelSelection{Provider: command.Provider, Model: command.Model}, func(error) { m.switching = false }), nil
	case "swarm-start":
		if err := m.ui.foregroundSwarmAdmissionError(); err != nil {
			return nil, err
		}
		return m.ui.StartSwarm(command.Text), nil
	default:
		return nil, fmt.Errorf("unknown workbench action %q", command.Action)
	}
}
func (m *workbenchModel) snapshot(since string) WorkbenchSnapshot {
	ui := m.ui
	thread := ui.timeline.transcriptThread()
	spec := ui.session.ProvSpec
	state := "ready"
	switch {
	case m.closing:
		state = "closing"
	case ui.foreground != nil && ui.foreground.stopping:
		state = "swarm-stopping"
	case ui.isSwarmOpen():
		state = "swarm"
	case m.cancelling:
		state = "cancelling"
	case ui.modeTarget.err != nil:
		state = "route-error"
	case ui.liveOperation != nil || ui.session.Run.Running:
		state = "running"
	case ui.sessionPersistRunning || len(ui.sessionPersistQueue) != 0:
		state = "saving"
	case m.exporting:
		state = "exporting"
	case ui.unsavedTurnError != nil || ui.sourceConflict || ui.rewindRecovery != "":
		state = "unsaved"
	case m.switching || ui.pendingTarget != nil || ui.modeTarget.building || ui.modeTarget.prepared != nil:
		state = "configuring"
	}
	mode := "build"
	if ui.session.PlanMode {
		mode = "plan"
	}
	out := WorkbenchSnapshot{Revision: strconv.FormatUint(thread.Revision(), 10), SessionID: ui.session.ID, Workspace: ui.session.WorkspaceDir, Provider: spec.Name, Model: spec.Model, Mode: mode, State: state, Entries: []WorkbenchEntry{}}
	out.Draft = ui.composer.text()
	out.RetrySaveReason = ui.sessionSaveRetryUnavailable()
	out.ExportPath = m.exportPath
	out.RecoveryError = m.recoveryError
	out.Usage = ui.UsageSnapshot()
	if ui.modeTarget.err != nil {
		out.Error = ui.modeTarget.err.Error()
	}
	if ui.session.ToastTone == toastError {
		out.Error = ui.session.Toast
	}
	if ui.unsavedTurnError != nil {
		out.Error = ui.sessionSaveFailureSummary()
	}
	// The token includes operational state, not only transcript revision, so
	// a save result, mode change or error cannot disappear behind a cache hit.
	metadata, _ := json.Marshal(out)
	out.Revision = fmt.Sprintf("%x", sha256.Sum256(metadata))
	if since == out.Revision {
		out.Unchanged = true
		out.Draft = ""
		return out
	}
	entries := thread.Entries()
	if len(entries) > 2000 {
		out.Omitted = len(entries) - 2000
		entries = entries[out.Omitted:]
	}
	// Newest evidence wins when the bounded display window is full. Durable
	// history remains intact; never serialize an unbounded tool result to JS.
	budget := 1024 * 1024
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		p := entry.Payload
		p.Text = workbenchPreview(p.Text)
		p.Effect = workbenchPreview(p.Effect)
		p.Argument = workbenchPreview(p.Argument)
		p.Diff = workbenchPreview(p.Diff)
		p.Prompt = workbenchPreview(p.Prompt)
		projected := WorkbenchEntry{ID: entry.ID, ParentID: entry.ParentID, TurnID: entry.TurnID, Kind: entry.Kind.String(), Payload: p}
		encoded, _ := json.Marshal(projected)
		if len(encoded) > budget {
			out.Omitted += i + 1
			break
		}
		budget -= len(encoded)
		out.Entries = append(out.Entries, projected)
	}
	slices.Reverse(out.Entries)
	return out
}
func workbenchPreview(text string) string {
	const limit = 32 * 1024
	if len(text) <= limit {
		return text
	}
	return strings.ToValidUTF8(text[:limit], "") + "\n… [display truncated]"
}
