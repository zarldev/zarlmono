package teasink

import (
	"context"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
)

// RequestControlRecordedMsg projects a durably committed request-control
// occurrence onto the human timeline. It carries no executable authority.
type RequestControlRecordedMsg struct {
	TaskID  string
	Control runner.ReplayControl
}

// RequestControlRecorded forwards committed request observations through the
// same ordered event pump as the owning run's other transcript events.
func (s *Sink) RequestControlRecorded(ctx context.Context, control runner.ReplayControl) {
	s.flush()
	taskID := control.TurnID
	if taskID == "" {
		taskID = string(taskscope.IDFrom(ctx))
	}
	s.dispatch(RequestControlRecordedMsg{TaskID: taskID, Control: control.Clone()})
}
