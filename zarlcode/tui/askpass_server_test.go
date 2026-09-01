package tui_test

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/askpass"
	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestAskpassServerCloseJoinsHandlersAndRemovesArtifacts(t *testing.T) {
	s, err := tui.NewAskpassServer(t.Context(), t.TempDir())
	if err != nil {
		t.Fatalf("NewAskpassServer: %v", err)
	}
	env := s.Env()
	started := make(chan struct{}, 1)
	s.SetSend(func(tea.Msg) { started <- struct{}{} })
	conn, err := net.Dial("unix", env[askpass.EnvSock])
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(askpass.Request{Prompt: "Password:"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}
	done := make(chan error, 1)
	go func() { done <- s.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not join handler")
	}
	for _, path := range []string{env[askpass.EnvSock], env["SUDO_ASKPASS"]} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("artifact %q remains: %v", path, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("repeated Close: %v", err)
	}
}

func TestAskpassServerCloseWithCanceledParent(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	s, err := tui.NewAskpassServer(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}
