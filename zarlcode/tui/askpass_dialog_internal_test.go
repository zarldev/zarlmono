package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/askpass"
)

func TestAskpassDialogUsesSharedActionRegionsAndMasksSecret(t *testing.T) {
	reply := make(chan askpass.Response, 1)
	d := newAskpassDialog("Password for root:", reply)
	for _, r := range "sëcret" {
		d.handleKey(tkey(string(r)))
	}
	buf := uv.NewScreenBuffer(100, 20)
	d.draw(buf, buf.Bounds())
	out := ansi.Strip(buf.Render())

	for _, want := range []string{"[sudo]", "password required", "Password for root:", "••••••", "enter send", "esc cancel"} {
		if !strings.Contains(out, want) {
			t.Errorf("askpass dialog missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "sëcret") {
		t.Fatalf("askpass dialog leaked password:\n%s", out)
	}
	if strings.Count(out, "[sudo]") != 1 {
		t.Fatalf("askpass should render one framed title:\n%s", out)
	}
}

func TestAskpassDialogSubmitAndCancel(t *testing.T) {
	reply := make(chan askpass.Response, 1)
	d := newAskpassDialog("", reply)
	if d.prompt != "sudo password:" {
		t.Fatalf("default prompt = %q", d.prompt)
	}
	d.handleKey(tkey("p"))
	d.handleKey(tkey("w"))

	submit, ok := d.handleKey(skey(tea.KeyEnter)).(actionAskpassReply)
	if !ok || submit.Password != "pw" || submit.Cancel || submit.Reply != reply {
		t.Fatalf("submit action = %#v", submit)
	}
	cancel, ok := d.handleKey(skey(tea.KeyEscape)).(actionAskpassReply)
	if !ok || !cancel.Cancel || cancel.Password != "" || cancel.Reply != reply {
		t.Fatalf("cancel action = %#v", cancel)
	}
}
