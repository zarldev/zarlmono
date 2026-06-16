package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/zarldev/zarlmono/zarlcode/askpass"
)

type actionAskpassReply struct {
	Reply    chan askpass.Response
	Password string
	Cancel   bool
}

func (actionAskpassReply) isAction() {}

type askpassDialog struct {
	prompt string
	reply  chan askpass.Response
	input  composer
}

func newAskpassDialog(prompt string, reply chan askpass.Response) *askpassDialog {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		prompt = "sudo password:"
	}
	return &askpassDialog{prompt: prompt, reply: reply}
}

func (d *askpassDialog) handleKey(msg tea.KeyPressMsg) action {
	switch msg.String() {
	case "esc", "ctrl+c":
		return actionAskpassReply{Reply: d.reply, Cancel: true}
	case "enter":
		return actionAskpassReply{Reply: d.reply, Password: d.input.text()}
	case "backspace":
		d.input.backspace()
	case "left":
		d.input.left()
	case "right":
		d.input.right()
	default:
		if msg.Text != "" {
			d.input.insert(msg.Text)
		}
	}
	return actionNone{}
}

func (d *askpassDialog) draw(scr uv.Screen, area uv.Rectangle) {
	masked := strings.Repeat("•", len([]rune(d.input.text())))
	if masked == "" {
		masked = palette.Subtle.On("(empty)")
	} else {
		masked += palette.Primary.On("▏")
	}
	lines := []string{
		palette.Primary.On("sudo password requested"),
		palette.Muted.On(d.prompt),
		"",
		masked,
		"",
		palette.Subtle.On("enter") + palette.Muted.On("  send password") + "   " +
			palette.Subtle.On("esc") + palette.Muted.On("  cancel"),
	}
	drawDialogBox(scr, area, "sudo", lines)
}
