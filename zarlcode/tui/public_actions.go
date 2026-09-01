package tui

import (
	"github.com/zarldev/zarlmono/zarlcode/askpass"
)

// OpenAskpass opens the password prompt used when a running command requests
// sudo authentication.
func (m *UI) OpenAskpass(prompt string, reply chan askpass.Response) {
	m.overlay.push(newAskpassDialog(prompt, reply))
}
