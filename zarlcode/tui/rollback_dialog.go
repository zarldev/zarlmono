package tui

import (
	"strings"
	"errors"
	"fmt"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

type rollbackDialog struct {
	plan RollbackPlan
	err  error
}

func newRollbackDialog(plan RollbackPlan, err error) *rollbackDialog {
	return &rollbackDialog{plan: plan, err: err}
}

func newRollbackDialogFor(checkpoints *Checkpoints, turnID, path string) *rollbackDialog {
	if checkpoints == nil {
		return newRollbackDialog(RollbackPlan{}, errors.New("no checkpoints recorded"))
	}
	var (
		plan RollbackPlan
		err  error
	)
	if path == "" {
		plan, err = checkpoints.PlanRestoreTurn(turnID)
	} else {
		plan, err = checkpoints.PlanRestoreFile(turnID, path)
	}
	return newRollbackDialog(plan, err)
}

func (d *rollbackDialog) handleKey(msg tea.KeyPressMsg) action {
	if d.err != nil || d.plan.Conflict {
		switch msg.String() {
		case "esc", "enter", "q", "y":
			return actionClose{}
		}
		return actionNone{}
	}
	switch msg.String() {
	case "y", "enter":
		return actionRollback{turnID: d.plan.TurnID, path: d.plan.Path}
	case "esc", "q", "n":
		return actionClose{}
	}
	return actionNone{}
}

func (d *rollbackDialog) draw(scr uv.Screen, area uv.Rectangle) {
	lines := append([]string{overlayTopBar("rollback", nil, 0, "confirm", 72), palette.Subtle.On(strings.Repeat("─", 72))}, d.lines()...)
	drawDialogBox(scr, area, "rollback", lines)
}

func (d *rollbackDialog) lines() []string {
	if d.err != nil {
		return []string{
			palette.Error.On("rollback unavailable"),
			"",
			palette.Muted.On(d.err.Error()),
			"",
			palette.Subtle.On("enter / esc") + palette.Muted.On("  close"),
		}
	}
	label := "turn " + d.plan.TurnID
	if d.plan.Path != "" {
		label += " · " + d.plan.Path
	}
	lines := []string{
		palette.Primary.On("Rollback " + label + "?"),
		palette.Muted.On(fmt.Sprintf("%d files affected", len(d.plan.Files))),
		"",
	}
	for _, file := range d.plan.Files {
		mark := palette.Success.On("✓")
		if file.Conflict {
			mark = palette.Error.On("!")
		}
		lines = append(lines, fmt.Sprintf("  %s %s  %s", mark, file.Action, file.Path))
	}
	lines = append(lines, "")
	if d.plan.Conflict {
		lines = append(lines,
			palette.Error.On("conflict detected"),
			palette.Muted.On("current file content no longer matches checkpoint output"),
			palette.Muted.On("rollback is refused in this version"),
			"",
			palette.Subtle.On("enter / esc")+palette.Muted.On("  close"),
		)
		return lines
	}
	lines = append(lines,
		palette.Subtle.On("y / enter")+palette.Muted.On("  rollback"),
		palette.Subtle.On("esc / q")+palette.Muted.On("  cancel"),
	)
	return lines
}

func (m *UI) rollback(turnID, path string) tea.Cmd {
	if m.session == nil || m.session.Checkpoints == nil {
		return nil
	}
	var err error
	if path == "" {
		err = m.session.Checkpoints.RestoreTurn(turnID)
	} else {
		err = m.session.Checkpoints.RestoreFile(turnID, path)
	}
	m.overlay.pop()
	label := "turn " + turnID
	if path != "" {
		label = path
	}
	if err != nil {
		msg := "rollback refused: " + err.Error()
		if !errors.Is(err, ErrCheckpointConflict) {
			msg = "rollback: " + err.Error()
		}
		m.session.SetErrorToast(msg)
		return m.toastExpiryCmd()
	}
	m.session.SetSuccessToast("rolled back " + label)
	return m.toastExpiryCmd()
}

func rollbackTargetFromMutations(mutations []WorkingSetMutation) (string, string) {
	if len(mutations) == 0 {
		return "", ""
	}
	latest := mutations[len(mutations)-1]
	return latest.TurnID, latest.Path
}
