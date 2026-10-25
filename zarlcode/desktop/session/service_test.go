package session_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/desktop/session"
	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestWorkbenchServiceAdmission(t *testing.T) {
	t.Parallel()
	service := session.New()
	if _, err := service.Command(tui.WorkbenchCommand{Action: "submit", Text: "never execute"}); err == nil {
		t.Fatal("disconnected submit accepted")
	}
	if _, err := service.Connect(session.ConnectOptions{Passphrase: strings.Repeat("x", 4097)}); err == nil {
		t.Fatal("oversized passphrase accepted")
	}
	if err := service.CheckClose(); err != nil {
		t.Fatal(err)
	}
	if err := service.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Connect(session.ConnectOptions{}); err == nil {
		t.Fatal("connect after shutdown accepted")
	}
	if _, err := service.Command(tui.WorkbenchCommand{Action: "snapshot"}); err == nil {
		t.Fatal("command after shutdown accepted")
	}
	if err := service.Shutdown(); err != nil {
		t.Fatal(err)
	}
}
