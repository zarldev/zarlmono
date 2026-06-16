package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/askpass"
	"github.com/zarldev/zarlmono/zkit/filesystem"
)

type askpassServer struct {
	ctx    context.Context
	cancel context.CancelFunc
	ln     net.Listener
	sock   string
	script string

	mu   sync.RWMutex
	send func(tea.Msg)
}

type askpassPromptMsg struct {
	Prompt string
	Reply  chan askpass.Response
}

func newAskpassServer(ctx context.Context, root string) (*askpassServer, error) {
	runDir := filepath.Join(root, ".zarlcode", "run")
	if err := os.MkdirAll(runDir, filesystem.ModePrivateDir); err != nil {
		return nil, fmt.Errorf("askpass run dir: %w", err)
	}
	sock := filepath.Join(runDir, "askpass.sock")
	_ = os.Remove(sock)
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "unix", sock)
	if err != nil {
		return nil, fmt.Errorf("askpass listen: %w", err)
	}
	if err := os.Chmod(sock, filesystem.ModePrivateFile); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("askpass socket permissions: %w", err)
	}
	exe, err := os.Executable()
	if err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("askpass executable: %w", err)
	}
	script := filepath.Join(runDir, "askpass.sh")
	body := fmt.Sprintf("#!/bin/sh\nexec %q --askpass \"$@\"\n", exe)
	//nolint:gosec // G306: the askpass helper must be executable (0700) so SUDO_ASKPASS/git can invoke it.
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("askpass helper: %w", err)
	}
	childCtx, cancel := context.WithCancel(ctx)
	s := &askpassServer{ctx: childCtx, cancel: cancel, ln: ln, sock: sock, script: script}
	go s.serve()
	return s, nil
}

func (s *askpassServer) SetSend(send func(tea.Msg)) {
	s.mu.Lock()
	s.send = send
	s.mu.Unlock()
}

func (s *askpassServer) Env() map[string]string {
	if s == nil {
		return nil
	}
	return map[string]string{
		"SUDO_ASKPASS":  s.script,
		askpass.EnvSock: s.sock,
	}
}

func (s *askpassServer) Close() error {
	if s == nil {
		return nil
	}
	s.cancel()
	err := s.ln.Close()
	_ = os.Remove(s.sock)
	return err
}

func (s *askpassServer) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || s.ctx.Err() != nil {
				return
			}
			continue
		}
		go s.handle(conn)
	}
}

func (s *askpassServer) handle(conn net.Conn) {
	defer conn.Close()
	var req askpass.Request
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		//nolint:gosec // G117: askpass.Response returns the requested credential to a local-only unix-socket client by design.
		_ = json.NewEncoder(conn).Encode(askpass.Response{Error: "invalid askpass request"})
		return
	}
	reply := make(chan askpass.Response, 1)
	s.mu.RLock()
	send := s.send
	s.mu.RUnlock()
	if send == nil {
		//nolint:gosec // G117: askpass.Response returns the requested credential to a local-only unix-socket client by design.
		_ = json.NewEncoder(conn).Encode(askpass.Response{Error: "askpass UI is not ready"})
		return
	}
	send(askpassPromptMsg{Prompt: req.Prompt, Reply: reply})
	select {
	case resp := <-reply:
		//nolint:gosec // G117: askpass.Response returns the requested credential to a local-only unix-socket client by design.
		_ = json.NewEncoder(conn).Encode(resp)
	case <-s.ctx.Done():
		//nolint:gosec // G117: askpass.Response returns the requested credential to a local-only unix-socket client by design.
		_ = json.NewEncoder(conn).Encode(askpass.Response{Error: "askpass cancelled"})
	}
}
