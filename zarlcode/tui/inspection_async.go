package tui

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
)

// inspectionView is owned by the Update/View loop. Worker commands publish
// values through messages; they never read or modify session or view state.
type inspectionView struct {
	snapshot InspectorSnapshot
	ready    bool
	key      inspectionKey
	prompt   inspectionLines
}

type inspectionLines struct {
	width int
	theme uint64
	lines []string
}

type inspectionKey struct {
	live       *engine.LiveRunner
	plan       bool
	model      string
	provider   string
	generation uint64
	settled    string
	surface    string
}

type inspectionOperation struct {
	view    *inspectionView
	key     inspectionKey
	ctx     context.Context
	cancel  context.CancelFunc
	claimed atomic.Bool
	done    chan struct{}
}

// foregroundInspectionDemand is retained only while an advisory inspector drains.
// All fields are owned by the Update loop, never by the inspection command.
type foregroundInspectionDemand struct {
	kind       foregroundInspectionKind
	text       string
	sessionID  string
	generation uint64
}

type inspectionLoadedMsg struct {
	op       *inspectionOperation
	snapshot InspectorSnapshot
}

func (op *inspectionOperation) command(load func(context.Context) InspectorSnapshot) tea.Cmd {
	return func() tea.Msg {
		if !op.claimed.CompareAndSwap(false, true) {
			return nil
		}
		defer close(op.done)
		if op.ctx.Err() != nil {
			return inspectionLoadedMsg{op: op}
		}
		return inspectionLoadedMsg{op: op, snapshot: load(op.ctx)}
	}
}

func (op *inspectionOperation) stop() {
	op.cancel()
	// Claim a command Bubble Tea has not started, so shutdown need not wait
	// for a command that may never run. A late invocation becomes a no-op.
	if op.claimed.CompareAndSwap(false, true) {
		close(op.done)
	}
}

func (m *UI) activeInspectionView() *inspectionView {
	if m.overlay.active() {
		if d, ok := m.overlay.top().(*inspector); ok {
			return d.inspectionView
		}
		return nil
	}
	if !m.session.CockpitExpanded || (m.contextView.tab != contextViewTabPrompt && m.contextView.tab != contextViewTabTools) {
		return nil
	}
	if m.contextView.inspection == nil {
		m.contextView.inspection = &inspectionView{}
	}
	return m.contextView.inspection
}

// inspectionCmd is called only from Update. There is at most one worker,
// including canceled workers whose results have not yet been acknowledged.
func (m *UI) inspectionCmd() tea.Cmd {
	if m.inspectionClosed || m.appContext().Err() != nil || m.inspectionDemand.kind != 0 || m.liveOperation != nil || m.isSwarmOpen() {
		return nil
	}
	view := m.activeInspectionView()
	if view != m.inspectionVisible {
		m.inspectionVisible = view
		if view != nil {
			view.ready = false // refresh after returning from settings or another tab
		}
	}
	key := inspectionKey{
		live: m.live, plan: m.session.PlanMode, model: m.session.Model,
		provider: m.session.Provider, generation: m.liveGeneration,
		settled: m.settledTurnID, surface: m.session.Run.toolSurface.Fingerprint,
	}
	if op := m.inspectionOperation; op != nil {
		if op.view == view && op.key == key && op.ctx.Err() == nil {
			return nil
		}
		op.stop()
		select {
		case <-op.done:
			m.inspectionOperation = nil
		default:
			return nil
		}
	}
	if view == nil || (view.ready && view.key == key) {
		return nil
	}
	view.ready = false
	view.key = key
	ctx, cancel := context.WithCancel(m.appContext())
	op := &inspectionOperation{view: view, key: key, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	m.inspectionOperation = op
	live := m.live
	return op.command(func(ctx context.Context) InspectorSnapshot {
		return BuildInspectorSnapshot(ctx, nil, live, nil)
	})
}

func (m *UI) receiveInspection(msg inspectionLoadedMsg) tea.Cmd {
	op := msg.op
	if m.inspectionOperation != op {
		return nil
	}
	m.inspectionOperation = nil
	defer op.cancel()
	if m.inspectionDemand.kind != 0 {
		return m.resumeInspectionDemand()
	}
	if m.inspectionClosed || op.ctx.Err() != nil || m.activeInspectionView() != op.view || op.view.key != op.key {
		return nil
	}
	msg.snapshot.EventLog = m.session.EventLog.Snapshot()
	msg.snapshot.ToolSurface = m.session.Run.toolSurface
	op.view.snapshot = msg.snapshot
	op.view.ready = true
	op.view.prompt = inspectionLines{}
	if m.overlay.active() {
		if d, ok := m.overlay.top().(*inspector); ok {
			d.processCursor = max(0, min(d.processCursor, len(msg.snapshot.Processes)-1))
		}
	}
	return nil
}

// waitForInspection cancels advisory work before a foreground admission attempt.
// It never waits on the Update loop; the worker's completion message retries it.
func (m *UI) waitForInspection(kind foregroundInspectionKind, text string) bool {
	if m.inspectionDemand.kind != 0 {
		m.session.SetToastTone("waiting for inspection to finish; previous foreground input retained", toastInfo)
		return true
	}
	op := m.inspectionOperation
	if op == nil {
		return false
	}
	op.stop()
	select {
	case <-op.done:
		m.inspectionOperation = nil
		op.cancel()
		return false
	default:
		m.inspectionDemand = foregroundInspectionDemand{kind: kind, text: text, sessionID: m.session.ID, generation: m.liveGeneration}
		m.session.SetToastTone("waiting for inspection to finish; input retained", toastInfo)
		return true
	}
}

func (m *UI) resumeInspectionDemand() tea.Cmd {
	demand := m.inspectionDemand
	m.inspectionDemand = foregroundInspectionDemand{}
	if m.inspectionClosed || m.appContext().Err() != nil {
		return nil
	}
	if m.session.ID != demand.sessionID || m.liveGeneration != demand.generation {
		m.session.SetErrorToast("foreground request cancelled after session changed; input was not dispatched")
		return nil
	}
	if demand.kind != foregroundInspectionTarget && m.modeTarget.prepared != nil {
		if err := m.applyPreparedModeTarget(nil); err != nil {
			m.session.SetErrorToast("apply mode target before foreground request: " + err.Error())
			return nil
		}
	}
	switch demand.kind {
	case foregroundInspectionDirect:
		return m.submit(demand.text)
	case foregroundInspectionSubmit:
		if strings.TrimSpace(m.composer.text()) != demand.text {
			return nil // the draft changed while inspection drained
		}
		cmd, accepted := m.acceptSubmit(demand.text)
		if accepted {
			m.composer.setText("")
			m.rememberInput(demand.text)
			m.draftScheduleSuppressed = true
			return tea.Batch(cmd, m.clearDraftCmd())
		}
		return cmd
	case foregroundInspectionQueue:
		return m.launchQueuedTurn()
	case foregroundInspectionSwarm:
		return m.startForegroundSwarm(demand.text)
	case foregroundInspectionTarget:
		if err := m.applyPreparedModeTarget(nil); err != nil {
			m.session.SetErrorToast("apply mode target: " + err.Error())
		}
		return nil
	}
	return nil
}

func (m *UI) closeInspection(ctx context.Context) error {
	m.inspectionClosed = true
	op := m.inspectionOperation
	if op == nil {
		return nil
	}
	op.stop()
	select {
	case <-op.done:
		m.inspectionOperation = nil
		return nil
	case <-ctx.Done():
		return fmt.Errorf("join inspection: %w", context.Cause(ctx))
	}
}

func inspectionLoadingLines() []string {
	return []string{palette.Muted.On("loading inspection…")}
}
