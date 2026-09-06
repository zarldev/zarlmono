package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Close begins the one-way shutdown transition, cancels the active turn, and
// waits for the single owned shutdown operation. A caller deadline only bounds
// that caller's wait: dependencies remain open until the turn actually drains.
// Concurrent and repeated calls observe the same terminal cleanup result.
func (l *LiveRunner) Close(ctx context.Context) error {
	if l == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	l.mu.Lock()
	if !l.closing {
		l.closing = true
		l.shutdownDone = make(chan struct{})
		turnCancel := l.turnCancel
		turnDone := l.turnDone
		shutdownDone := l.shutdownDone
		go l.shutdown(turnDone, shutdownDone)
		if turnCancel != nil {
			turnCancel()
		}
	}
	shutdownDone := l.shutdownDone
	l.mu.Unlock()

	select {
	case <-shutdownDone:
		l.mu.Lock()
		err := l.shutdownErr
		l.mu.Unlock()
		return err
	case <-ctx.Done():
		return fmt.Errorf("wait for live runner shutdown: %w", ctx.Err())
	}
}

func (l *LiveRunner) shutdown(turnDone, shutdownDone chan struct{}) {
	if turnDone != nil {
		<-turnDone
	}

	l.mu.Lock()
	mcp := l.mcp
	computer := l.computer
	fetchTool := l.fetchTool
	truncator := l.truncator
	l.mcp, l.mcpHost = nil, nil
	l.computer = nil
	l.fetchTool = nil
	l.truncator = nil
	l.mu.Unlock()

	var errs []error
	if mcp != nil {
		if err := mcp.CloseAll(); err != nil {
			errs = append(errs, fmt.Errorf("close MCP connections: %w", err))
		}
	}
	if computer != nil {
		if err := computer.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close computer session: %w", err))
		}
	}
	if fetchTool != nil {
		if err := fetchTool.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close web fetch: %w", err))
		}
	}
	if truncator != nil {
		if err := truncator.Cleanup(); err != nil {
			errs = append(errs, fmt.Errorf("clean tool spills: %w", err))
		}
	}

	l.mu.Lock()
	l.shutdownErr = errors.Join(errs...)
	close(shutdownDone)
	l.mu.Unlock()
}

func (l *LiveRunner) beginTurn(ctx context.Context) (context.Context, func(), error) {
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	l.mu.Lock()
	if l.closing {
		l.mu.Unlock()
		cancel()
		return nil, nil, errors.New("live runner is closing")
	}
	l.turnCancel = cancel
	l.turnDone = done
	l.mu.Unlock()

	var once sync.Once
	finish := func() {
		once.Do(func() {
			cancel()
			close(done)
			l.mu.Lock()
			if l.turnDone == done {
				l.turnCancel = nil
				l.turnDone = nil
			}
			l.mu.Unlock()
		})
	}
	return runCtx, finish, nil
}
