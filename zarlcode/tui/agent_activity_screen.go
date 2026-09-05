package tui

import (
	"fmt"
	"strings"
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
	case "esc", "q", "ctrl+a":
		return actionClose{}
	case "up", "k":
		if a.cursor > 0 {
			a.cursor--
			a.scroll = 0
		}
	case "down", "j":
		if a.cursor < len(agents)-1 {
			a.cursor++
			a.scroll = 0
		}
	case "home", "g":
		a.cursor = 0
		a.scroll = 0
	case "end", "G":
		a.cursor = max(0, len(agents)-1)
		a.scroll = 0
	case "pgup":
		a.scroll = max(0, a.scroll-10)
	case "pgdown":
		a.scroll += 10
	}
	return actionNone{}
}

func (a *agentActivityScreen) draw(scr uv.Screen, area uv.Rectangle) {
	l, ok := drawUtilitySplitPane(scr, area, 38)
	if !ok {
		return
	}
	agents := a.agents()
	header := overlayTopBar("agent activity", nil, 0, agentActivitySummary(agents), l.Context.Dx())
	drawOverlayContext(scr, l, header, palette.Border)
	drawLine(scr, uv.Rect(l.Nav.Min.X, l.Nav.Min.Y, l.Nav.Dx(), 1), palette.Muted.On(" agents · oldest first"))
	drawLine(scr, uv.Rect(l.Nav.Min.X, l.Nav.Min.Y+1, l.Nav.Dx(), 1), palette.Border.On(strings.Repeat("─", l.Nav.Dx())))
	if len(agents) == 0 {
		drawLine(scr, uv.Rect(l.Nav.Min.X, l.Nav.Min.Y+2, l.Nav.Dx(), 1), palette.Muted.On("  no delegated agents this session"))
		drawLine(scr, l.Detail, palette.Muted.On(" delegate a task to see its live output and activity here"))
	} else {
		a.cursor = min(a.cursor, len(agents)-1)
		navY := l.Nav.Min.Y + min(2, l.Nav.Dy())
		navRows := max(1, (l.Nav.Dy()-2)/2)
		start, end := windowAroundCursor(a.cursor, len(agents), navRows)
		for i := start; i < end; i++ {
			agent := agents[i]
			screenY := navY + (i-start)*2
			name := agent.agentName
			if name == "" {
				name = "agent"
			}
			label := agent.statusBadge() + "  " + name
			if target := agent.targetLabel(); target != "" {
				label += " · " + target
			}
			drawListRow(scr, uv.Rect(l.Nav.Min.X, screenY, l.Nav.Dx(), 1), label, i == a.cursor, true)
			if screenY+1 < l.Nav.Max.Y {
				meta := fmt.Sprintf("    %s · %d %s", agentElapsed(agent), agent.toolCount(), plural(agent.toolCount(), "tool", "tools"))
				if agent.prompt != "" {
					meta += " · " + agent.prompt
				}
				drawLine(scr, uv.Rect(l.Nav.Min.X, screenY+1, l.Nav.Dx(), 1), ansi.Truncate(palette.Subtle.On(meta), l.Nav.Dx(), "…"))
			}
		}

		cw := max(1, l.Detail.Dx()-scrollbarWidth)
		lines := agentActivityDetailLines(agents[a.cursor], cw)
		a.scroll = clampScrollOffset(a.scroll, len(lines), l.Detail.Dy())
		for i := a.scroll; i < len(lines) && i-a.scroll < l.Detail.Dy(); i++ {
			if strings.HasPrefix(ansi.Strip(lines[i]), "├") {
				drawSectionRule(scr, l.Detail, l.Detail.Min.Y+i-a.scroll, lines[i])
				continue
			}
			drawLine(scr, uv.Rect(l.Detail.Min.X, l.Detail.Min.Y+i-a.scroll, cw, 1), ansi.Truncate(lines[i], cw, ""))
		}
		drawPaneScrollbar(scr, l.Detail.Max.X-1, l.Detail.Min.Y, l.Detail.Dy(), len(lines), a.scroll)
	}
	footer := keyLegend(keyHint{"↑↓/jk", "select"}, keyHint{"pgup/pgdn", "scroll detail"}, keyHint{"ctrl+a / esc", "close"})
	if l.Footer.Dx() < 60 {
		footer = keyLegend(keyHint{"↑↓", "agent"}, keyHint{"pgup/dn", "detail"}, keyHint{"ctrl+a/esc", "close"})
	}
	drawLine(scr, l.Footer, palette.Muted.On(" "+footer))
}

func agentActivitySummary(agents []*subAgentItem) string {
	counts := make(map[string]int)
	for _, agent := range agents {
		counts[agent.status()]++
	}
	parts := []string{fmt.Sprintf("%d delegated", len(agents))}
	for _, status := range []string{"starting", "running", "failed", "interrupted", "complete"} {
		if counts[status] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[status], status))
		}
	}
	return strings.Join(parts, " · ")
}

func agentActivityDetailLines(agent *subAgentItem, width int) []string {
	if agent == nil {
		return []string{palette.Muted.On(" no agent selected")}
	}
	name := agent.agentName
	if name == "" {
		name = "agent"
	}
	heading := " " + agent.statusBadge() + "  " + palette.Primary.On(name)
	if target := agent.targetLabel(); target != "" {
		heading += palette.Subtle.On(" · " + target)
	}
	lines := []string{
		heading,
		fmt.Sprintf(" elapsed: %s · %d %s", agentElapsed(agent), agent.toolCount(), plural(agent.toolCount(), "tool", "tools")),
	}
	if target := agent.targetLabel(); target != "" {
		lines = append(lines, " target: "+palette.Info.On(target))
	}
	if agent.taskID != "" {
		lines = append(lines, " task: "+palette.Subtle.On(agent.taskID))
	}
	lines = append(lines, "", sectionHead("assignment", width))
	if agent.prompt == "" {
		lines = append(lines, palette.Muted.On("  no assignment text recorded"))
	} else {
		lines = append(lines, renderPlain(width, agent.prompt, withFirstPrefix("  ", "  "))...)
	}
	lines = append(lines, "", sectionHead("activity", width))
	if len(agent.children) == 0 {
		return append(lines, palette.Muted.On("  waiting for agent activity…"))
	}
	activity := renderChildBlock(agent.children, max(1, width-2))
	return append(lines, activity.lines...)
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
