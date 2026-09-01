package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/askpass"
	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestAskpassDialogMasksAndSubmitsPassword(t *testing.T) {
	reply := make(chan askpass.Response, 1)
	m := tui.New()
	step(t, m, window(100, 20))
	m.OpenAskpass("Password for root:", reply)
	for _, r := range "sëcret" {
		step(t, m, tea.KeyPressMsg{Text: string(r), Code: r})
	}
	out := ansi.Strip(m.View().Content)
	for _, want := range []string{"[sudo]", "password required", "Password for root:", "••••••", "enter send", "esc cancel"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "sëcret") {
		t.Fatalf("password leaked:\n%s", out)
	}
	step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := <-reply; got.Password != "sëcret" || got.Error != "" {
		t.Fatalf("response = %#v", got)
	}
	if strings.Contains(ansi.Strip(m.View().Content), "password required") {
		t.Fatal("submitted dialog remained open")
	}
}

func TestAskpassDialogDefaultsPromptAndCancels(t *testing.T) {
	reply := make(chan askpass.Response, 1)
	m := tui.New()
	step(t, m, window(80, 15))
	m.OpenAskpass("  ", reply)
	if out := ansi.Strip(m.View().Content); !strings.Contains(out, "sudo password:") {
		t.Fatalf("default prompt missing:\n%s", out)
	}
	step(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if got := <-reply; got.Error != "cancelled" {
		t.Fatalf("cancel response = %#v", got)
	}
}
