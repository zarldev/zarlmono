package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	localservices "github.com/zarldev/zarlmono/zarlcode/services"
)

type serviceOp string

const (
	serviceOpRefresh serviceOp = "refresh"
	serviceOpInstall serviceOp = "install"
	serviceOpStart   serviceOp = "start"
	serviceOpStop    serviceOp = "stop"
	serviceOpLogs    serviceOp = "logs"
)

type serviceAction struct{ op serviceOp }

func (serviceAction) isAction() {}

type serviceResultMsg struct {
	op     serviceOp
	status localservices.Status
	output string
	err    error
}

type serviceDialog struct {
	ctx       context.Context
	status    localservices.Status
	selected  int
	busy      serviceOp
	output    string
	message   string
	messageAt time.Time
}

var serviceDialogRows = []struct {
	label string
	op    serviceOp
}{
	{label: "refresh status", op: serviceOpRefresh},
	{label: "install service files", op: serviceOpInstall},
	{label: "start SearXNG", op: serviceOpStart},
	{label: "stop SearXNG", op: serviceOpStop},
	{label: "show recent logs", op: serviceOpLogs},
}

func newServiceDialog(ctx context.Context) *serviceDialog {
	if ctx == nil {
		ctx = context.Background()
	}
	d := &serviceDialog{ctx: ctx}
	d.refresh()
	return d
}

func (d *serviceDialog) refresh() { d.status = localservices.Probe(d.ctx) }

func (d *serviceDialog) handleKey(msg tea.KeyPressMsg) action {
	if d.busy != "" {
		switch msg.String() {
		case "esc", "q":
			return actionClose{}
		}
		return actionNone{}
	}
	switch msg.String() {
	case "esc", "q", "left", "h":
		return actionClose{}
	case "up", "k":
		if d.selected > 0 {
			d.selected--
		}
	case "down", "j":
		if d.selected < len(serviceDialogRows)-1 {
			d.selected++
		}
	case "enter", "space", " ":
		return serviceAction{op: serviceDialogRows[d.selected].op}
	}
	return actionNone{}
}

const (
	serviceDialogW = 92
	serviceDialogH = 26
)

func (d *serviceDialog) draw(scr uv.Screen, area uv.Rectangle) {
	r := centerRect(area, min(serviceDialogW, area.Dx()), min(serviceDialogH, area.Dy()))
	inner := drawFrame(scr, r, frameStyle{Label: "local web_search service", Border: palette.Border, LabelColor: palette.Primary})
	if inner.Dx() < 4 || inner.Dy() < 1 {
		return
	}
	content := d.lines(inner.Dx() - 2)
	for i := range inner.Dy() {
		line := ""
		if i < len(content) {
			line = content[i]
		}
		drawPaddedLine(scr, uv.Rect(inner.Min.X+1, inner.Min.Y+i, inner.Dx()-2, 1), line)
	}
}

func (d *serviceDialog) lines(width int) []string {
	st := d.status
	lines := []string{
		palette.Assistant.On("SearXNG") + palette.Muted.On(" — optional local backend for web_search"),
		"",
		"url        " + st.URL,
		"files      " + st.Dir,
		"installed  " + boolLabel(st.Installed),
		"docker     " + boolLabel(st.Docker),
		"health     " + boolLabel(st.Healthy),
	}
	if st.Error != "" && !st.Healthy {
		lines = append(lines, "status     "+palette.Warning.On(st.Error))
	}
	lines = append(lines, "")
	lines = append(lines, renderPlain(width, "zarlcode can manage this tool service because it backs web_search. Model servers stay external; configure those under Providers.", withStyle(palette.Muted.On))...)
	lines = append(lines, "")
	for i, row := range serviceDialogRows {
		marker := "  "
		label := palette.Subtle.On(row.label)
		if i == d.selected {
			marker = palette.Primary.On("▸ ")
			label = palette.Assistant.On(row.label)
		}
		if d.busy == row.op {
			label += palette.Muted.On(" …")
		}
		lines = append(lines, marker+label)
	}
	if d.message != "" && time.Since(d.messageAt) < settingsToastTTL {
		lines = append(lines, "", palette.Muted.On(d.message))
	}
	if d.output != "" {
		lines = append(lines, "", palette.Subtle.On("last output"))
		for _, ln := range serviceOutputLines(d.output, width-2, 6) {
			lines = append(lines, "  "+ln)
		}
	}
	lines = append(lines, "", palette.Muted.On(keyLegend(keyHint{"↑↓", "move"}, keyHint{"enter", "run"}, keyHint{"esc", "back"})))
	return lines
}

func (d *serviceDialog) onResult(msg serviceResultMsg) {
	d.busy = ""
	d.status = msg.status
	d.output = msg.output
	if msg.err != nil {
		d.message = fmt.Sprintf("%s failed: %v", msg.op, msg.err)
	} else {
		d.message = string(msg.op) + " complete"
	}
	d.messageAt = time.Now()
}

func boolLabel(v bool) string {
	if v {
		return palette.Success.On("yes")
	}
	return palette.Muted.On("no")
}

func serviceOutputLines(output string, width, maxLines int) []string {
	if width < 8 {
		width = 8
	}
	raw := strings.Split(strings.TrimSpace(output), "\n")
	if len(raw) == 1 && raw[0] == "" {
		return nil
	}
	truncated := false
	if len(raw) > maxLines {
		raw = raw[len(raw)-maxLines:]
		truncated = true
	}
	out := make([]string, 0, len(raw)+1)
	if truncated {
		out = append(out, "… earlier output omitted")
	}
	for _, ln := range raw {
		ln = strings.TrimRight(ln, "\r")
		if len(ln) > width {
			ln = ln[:max(0, width-1)] + "…"
		}
		out = append(out, ln)
	}
	return out
}
