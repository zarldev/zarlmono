package mcp

import (
	"context"
	"errors"
	"os/exec"
	"sync"
	"testing"
	"time"
)

// blockingWriteCloser blocks on Write until Close is called — a stand-in for a
// stdio pipe to a server that has stopped draining its stdin.
type blockingWriteCloser struct {
	closed chan struct{}
	once   sync.Once
}

func (b *blockingWriteCloser) Write([]byte) (int, error) {
	<-b.closed
	return 0, errors.New("stdin closed")
}

func (b *blockingWriteCloser) Close() error {
	b.once.Do(func() { close(b.closed) })
	return nil
}

// TestStdioWriteFrameUnblocksOnCtxCancel locks the wedge fix: a write that
// blocks (server not draining stdin) must honour the caller's ctx instead of
// hanging forever (and, via writeM, wedging every other Call/Notify).
func TestStdioWriteFrameUnblocksOnCtxCancel(t *testing.T) {
	t.Parallel()
	cmd := exec.Command("true") // exits immediately, so Close()'s cmd.Wait is fast
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start 'true': %v", err)
	}
	tr := &stdioTransport{
		cmd:     cmd,
		stdin:   &blockingWriteCloser{closed: make(chan struct{})},
		done:    make(chan struct{}),
		pending: make(map[string]chan rpcResponse),
	}

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- tr.writeFrame(ctx, map[string]string{"method": "ping"}) }()

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("writeFrame err = %v, want context.DeadlineExceeded", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("writeFrame hung on a stuck write instead of honouring ctx")
	}
}
