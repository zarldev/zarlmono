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

type AskpassServer struct {
	ctx    context.Context
	cancel context.CancelFunc
	ln     net.Listener
	sock   string
	script string

	mu   sync.RWMutex
	send func(tea.Msg)

	serveDone chan struct{}
	handlers  sync.WaitGroup
	connMu    sync.Mutex
	conns     map[net.Conn]struct{}
	closeOnce sync.Once
	closeErr  error
}

type askpassPromptMsg struct {
	Prompt string
	Reply  chan askpass.Response
}

func NewAskpassServer(ctx context.Context, root string) (*AskpassServer, error) {
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
	s := &AskpassServer{
		ctx: childCtx, cancel: cancel, ln: ln, sock: sock, script: script,
		serveDone: make(chan struct{}),
		conns:     make(map[net.Conn]struct{}),
	}
	go s.serve()
	return s, nil
}

func (s *AskpassServer) SetSend(send func(tea.Msg)) {
	s.mu.Lock()
	s.send = send
	s.mu.Unlock()
}

func (s *AskpassServer) Env() map[string]string {
	if s == nil {
		return nil
	}
	return map[string]string{
		"SUDO_ASKPASS":  s.script,
		askpass.EnvSock: s.sock,
	}
}

func (s *AskpassServer) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		s.cancel()
		if err := s.ln.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			s.closeErr = err
		}
		<-s.serveDone
		s.closeConnections()
		s.handlers.Wait()
		if err := os.Remove(s.sock); err != nil && !errors.Is(err, os.ErrNotExist) && s.closeErr == nil {
			s.closeErr = err
		}
		if err := os.Remove(s.script); err != nil && !errors.Is(err, os.ErrNotExist) && s.closeErr == nil {
			s.closeErr = err
		}
	})
	return s.closeErr
}

func (s *AskpassServer) serve() {
	defer close(s.serveDone)
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || s.ctx.Err() != nil {
				return
			}
			continue
		}
		s.connMu.Lock()
		s.conns[conn] = struct{}{}
		s.connMu.Unlock()
		s.handlers.Add(1)
		go func() {
			defer s.handlers.Done()
			s.handle(conn)
		}()
	}
}

func (s *AskpassServer) closeConnections() {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	for conn := range s.conns {
		_ = conn.Close()
	}
}

func (s *AskpassServer) handle(conn net.Conn) {
	defer func() {
		s.connMu.Lock()
		delete(s.conns, conn)
		s.connMu.Unlock()
		_ = conn.Close()
	}()
	var req askpass.Request
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		_ = json.NewEncoder(conn).Encode(askpass.Response{Error: "invalid askpass request"})
		return
	}
	reply := make(chan askpass.Response, 1)
	s.mu.RLock()
	send := s.send
	s.mu.RUnlock()
	if send == nil {
		_ = json.NewEncoder(conn).Encode(askpass.Response{Error: "askpass UI is not ready"})
		return
	}
	send(askpassPromptMsg{Prompt: req.Prompt, Reply: reply})
	select {
	case resp := <-reply:

		_ = json.NewEncoder(conn).Encode(resp)
	case <-s.ctx.Done():

		_ = json.NewEncoder(conn).Encode(askpass.Response{Error: "askpass cancelled"})
	}
}
