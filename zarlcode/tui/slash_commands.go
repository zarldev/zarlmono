package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/prompts"
)

type slashCommand struct {
	name string
	desc string
}

var slashCommands = []slashCommand{
	{name: "/clear", desc: "clear the conversation"},
	{name: "/help", desc: "open key help"},
	{name: "/init", desc: "create or update AGENTS.md"},
}

func slashStatusHint(input string) string {
	input = strings.TrimSpace(input)
	if !strings.HasPrefix(input, "/") {
		return ""
	}
	var parts []string
	for _, c := range slashCommands {
		if strings.HasPrefix(c.name, input) || input == "/" {
			parts = append(parts, c.name+" "+c.desc)
		}
	}
	if len(parts) == 0 {
		return " slash command not found  ·  enter to dismiss"
	}
	return " slash commands  ·  " + strings.Join(parts, "  ·  ")
}

func (m *UI) handleSlashSubmit(text string) tea.Cmd {
	name := strings.Fields(text)
	if len(name) == 0 || !strings.HasPrefix(name[0], "/") {
		return nil
	}
	switch name[0] {
	case "/clear":
		return m.clearContextAndTimeline()
	case "/help":
		m.overlay.push(m.newHelpDialog())
		return nil
	case "/init":
		if m.runFn != nil {
			return m.runFn(prompts.Init)
		}
		m.session.SetErrorToast("session not ready — run a turn first")
		return m.toastExpiryCmd()
	default:
		m.session.SetErrorToast("unknown slash command: " + name[0])
		return m.toastExpiryCmd()
	}
}
