package tui

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

type agentActivityScreen struct {
	timeline *timeline
	cursor   int
	scroll   int
}

func newAgentActivityScreen(tl *timeline) *agentActivityScreen {
	return &agentActivityScreen{timeline: tl}
}

func (*agentActivityScreen) fullScreen() bool { return true }

func (a *agentActivityScreen) agents() []*subAgentItem {
	if a.timeline == nil {
		return nil
	}
	var out []*subAgentItem
	var walk func([]item)
	walk = func(items []item) {
		for _, it := range items {
			switch x := it.(type) {
			case *subAgentItem:
				out = append(out, x)
				walk(x.children)
			case *groupItem:
				walk(x.children)
			}
		}
	}
	walk(a.timeline.items)
	return out
}

func (a *agentActivityScreen) handleKey(msg tea.KeyPressMsg) action {
	agents := a.agents()
	switch msg.String() {
	case "esc", "q":
		return actionClose{}
	case "up", "k":
		if a.cursor > 0 {
			a.cursor--
		}
	case "down", "j":
		if a.cursor < len(agents)-1 {
			a.cursor++
		}
	case "home", "g":
		a.cursor = 0
	case "end", "G":
		a.cursor = max(0, len(agents)-1)
	case "pgup":
		a.scroll = max(0, a.scroll-10)
	case "pgdown":
		a.scroll += 10
	}
	return actionNone{}
}

func (a *agentActivityScreen) draw(scr uv.Screen, area uv.Rectangle) {
	l, ok := drawSplitPane(scr, area, "AGENT ACTIVITY", 38)
	if !ok {
		return
	}
	agents := a.agents()
	drawLine(scr, l.Context, palette.Muted.On(fmt.Sprintf("%d delegated agents · live session activity", len(agents))))
	if len(agents) == 0 {
		drawLine(scr, l.Nav, palette.Muted.On("no delegated agents this session"))
	} else {
		a.cursor = min(a.cursor, len(agents)-1)
		start := max(0, a.cursor-l.Nav.Dy()+1)
		for row, agent := range agents[start:min(len(agents), start+l.Nav.Dy())] {
			status := "running"
			if agent.closed {
				status = "complete"
			}
			prefix := "  "
			if start+row == a.cursor {
				prefix = "› "
			}
			line := fmt.Sprintf("%s%s · %s · %s", prefix, agent.agentName, status, agentElapsed(agent))
			drawLine(scr, uv.Rect(l.Nav.Min.X, l.Nav.Min.Y+row, l.Nav.Dx(), 1), ansi.Truncate(line, l.Nav.Dx(), "…"))
		}
		agent := agents[a.cursor]
		lines := agent.render(max(1, l.Detail.Dx()))
		start = min(a.scroll, max(0, len(lines)-l.Detail.Dy()))
		for row, line := range lines[start:min(len(lines), start+l.Detail.Dy())] {
			drawLine(scr, uv.Rect(l.Detail.Min.X, l.Detail.Min.Y+row, l.Detail.Dx(), 1), line)
		}
	}
	drawLine(scr, l.Footer, palette.Muted.On("↑↓ agent  ·  pgup/pgdown detail  ·  esc close"))
}

func agentElapsed(agent *subAgentItem) string {
	if agent == nil || agent.startedAt.IsZero() {
		return "0s"
	}
	end := time.Now()
	if !agent.endedAt.IsZero() {
		end = agent.endedAt
	}
	return compactElapsed(end.Sub(agent.startedAt))
}
