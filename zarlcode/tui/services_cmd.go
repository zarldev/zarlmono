package tui

import (
	"bytes"
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"

	localservices "github.com/zarldev/zarlmono/zarlcode/services"
)

func (m *UI) handleServiceAction(a serviceAction) tea.Cmd {
	if d := topServiceDialog(m); d != nil {
		d.busy = a.op
		d.output = ""
	}
	return serviceCmd(a.op)
}

func serviceCmd(op serviceOp) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		var out bytes.Buffer
		var err error
		switch op {
		case serviceOpRefresh:
		case serviceOpInstall:
			var res localservices.MaterialiseResult
			res, err = localservices.Materialise(ctx, false)
			out.WriteString(formatMaterialiseResult(res))
		case serviceOpStart:
			err = localservices.Start(ctx, &out, &out)
		case serviceOpStop:
			err = localservices.Stop(ctx, &out, &out)
		case serviceOpLogs:
			err = localservices.Logs(ctx, &out, &out)
		default:
			err = fmt.Errorf("unknown service action %q", op)
		}
		return serviceResultMsg{op: op, status: localservices.Probe(ctx), output: out.String(), err: err}
	}
}

func (m *UI) handleServiceResult(msg tea.Msg) bool {
	res, ok := msg.(serviceResultMsg)
	if !ok {
		return false
	}
	if d := topServiceDialog(m); d != nil {
		d.onResult(res)
	}
	return true
}

func topServiceDialog(m *UI) *serviceDialog {
	for i := len(m.overlay.stack) - 1; i >= 0; i-- {
		if d, ok := m.overlay.stack[i].(*serviceDialog); ok {
			return d
		}
	}
	return nil
}

func formatMaterialiseResult(res localservices.MaterialiseResult) string {
	return fmt.Sprintf("dir: %s\ncreated: %d\nexisted: %d\nskipped: %d\n", res.Dir, len(res.Created), len(res.Existed), len(res.Skipped))
}
