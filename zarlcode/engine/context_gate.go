package engine

import (
	"context"
	"sync"
)

// contextGate serializes the entire context transition, including lifecycle
// finalization. Its zero value is ready for use; waiters need no goroutine.
type contextGate struct {
	once sync.Once
	held chan struct{}
}

func (g *contextGate) acquire(ctx context.Context) error {
	g.once.Do(func() { g.held = make(chan struct{}, 1) })
	select {
	case g.held <- struct{}{}:
		if err := ctx.Err(); err != nil {
			g.Unlock()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *contextGate) Lock()   { _ = g.acquire(context.Background()) }
func (g *contextGate) Unlock() { <-g.held }
