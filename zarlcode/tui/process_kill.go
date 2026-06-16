package tui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

type processKillResultMsg struct {
	ProcessID string
	Signal    string
	ExitCode  int
	Command   string
	Error     string
	At        time.Time
}

func (m *UI) killProcessCmd(processID, signal string) tea.Cmd {
	return func() tea.Msg {
		msg := processKillResultMsg{ProcessID: processID, Signal: engine.NormalizeKillSignal(signal), At: time.Now()}
		info, exitCode, err := m.killProcess(processID, msg.Signal)
		msg.ExitCode = exitCode
		msg.Command = info.Command
		if err != nil {
			msg.Error = err.Error()
		}
		return msg
	}
}

func (m *UI) killProcess(processID, signal string) (code.ProcessInfo, int, error) {
	if strings.TrimSpace(processID) == "" {
		return code.ProcessInfo{}, 0, errors.New("process kill: process id required")
	}
	if m == nil || m.live == nil {
		return code.ProcessInfo{}, 0, errors.New("process kill: live runner unavailable")
	}
	return m.live.KillProcess(processID, signal)
}

func (m *UI) handleProcessKillResult(msg processKillResultMsg) tea.Cmd {
	if msg.Error != "" {
		m.session.SetErrorToast("process kill: " + msg.Error)
		m.updateInspectorAfterProcessKill("kill error: " + msg.Error)
		return m.toastExpiryCmd()
	}

	text := processKilledAgentMessage(msg)
	queued := false
	if m.session.Run.Running && m.live != nil {
		m.live.QueueInput(text)
		queued = true
	}
	state := "sent to agent"
	if queued {
		state = "queued to agent"
	}
	m.timeline.addNotice(palette.Muted.On("process killed · " + msg.ProcessID + " · " + state))
	m.session.SetSuccessToast("process killed: " + msg.ProcessID)
	m.updateInspectorAfterProcessKill("killed " + msg.ProcessID + " · exit " + strconv.Itoa(msg.ExitCode) + " · " + state)
	cmd := m.toastExpiryCmd()
	if !queued && m.runFn != nil {
		cmd = tea.Batch(cmd, m.runFn(text))
	}
	return cmd
}

func (m *UI) updateInspectorAfterProcessKill(status string) {
	for i := range m.overlay.stack {
		ins, ok := m.overlay.stack[i].(*inspector)
		if !ok {
			continue
		}
		ins.snapshot = BuildInspectorSnapshot(m.session, m.live, nil)
		ins.status = status
		if ins.processCursor >= len(ins.snapshot.Processes) {
			ins.processCursor = max(0, len(ins.snapshot.Processes)-1)
		}
	}
}

func processKilledAgentMessage(msg processKillResultMsg) string {
	cmd := strings.TrimSpace(msg.Command)
	if cmd == "" {
		cmd = "(command unavailable)"
	}
	return fmt.Sprintf("System event from zarlcode inspector: background process %s was killed with SIG%s at %s. Exit code: %d. Command: %s",
		msg.ProcessID,
		engine.NormalizeKillSignal(msg.Signal),
		msg.At.Format(time.RFC3339),
		msg.ExitCode,
		cmd,
	)
}
