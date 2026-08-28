package tui

import (
	"context"
	"net"
	"os"
	"testing"
	"time"
)

func TestAskpassServerCloseJoinsHandlersAndRemovesArtifacts(t *testing.T) {
	s, err := newAskpassServer(t.Context(), t.TempDir())
	if err != nil {
		t.Fatalf("newAskpassServer: %v", err)
	}

	conn, err := net.Dial("unix", s.sock)
	if err != nil {
		_ = s.Close()
		t.Fatalf("dial askpass socket: %v", err)
	}
	defer conn.Close()

	deadline := time.Now().Add(time.Second)
	for {
		s.connMu.Lock()
		accepted := len(s.conns) == 1
		s.connMu.Unlock()
		if accepted {
			break
		}
		if time.Now().After(deadline) {
			_ = s.Close()
			t.Fatal("server did not start connection handler")
		}
		time.Sleep(time.Millisecond)
	}

	done := make(chan error, 1)
	go func() { done <- s.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not join blocked handler")
	}

	for _, path := range []string{s.sock, s.script} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("artifact %q still exists: %v", path, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("repeated Close: %v", err)
	}
}

func TestAskpassServerCloseWithCanceledParent(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	s, err := newAskpassServer(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("newAskpassServer: %v", err)
	}
	cancel()
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
