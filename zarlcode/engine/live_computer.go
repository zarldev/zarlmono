package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"

	model "github.com/zarldev/zarlmono/zkit/agent/computer"
	"github.com/zarldev/zarlmono/zkit/agent/computer/browser"
)

type liveComputer struct {
	owner *LiveRunner

	mu            sync.Mutex
	session       ComputerSession
	newSession    ComputerSessionFactory
	forceHeadless bool
	starting      chan struct{}
	startupCancel context.CancelCauseFunc
	sessionCancel context.CancelCauseFunc
	stopping      bool
}

func (c *liveComputer) Observe(ctx context.Context, req model.ObserveRequest) (model.Observation, error) {
	s, err := c.sessionFor(ctx)
	if err != nil {
		return model.Observation{}, err
	}
	return s.Observe(ctx, req)
}

// ComputerSession is the reusable browser session owned by a LiveRunner.
// Implementations must support observation, actions, and deterministic cleanup.
type ComputerSession interface {
	model.Observer
	model.Actor
	Close() error
}

// ComputerSessionFactory starts a browser session for a LiveRunner.
type ComputerSessionFactory func(context.Context, ...browser.Option) (ComputerSession, error)

func newBrowserSession(ctx context.Context, opts ...browser.Option) (ComputerSession, error) {
	return browser.New(ctx, opts...)
}

// ComputerObserve observes the reusable browser session owned by the runner.
func (l *LiveRunner) ComputerObserve(ctx context.Context, req model.ObserveRequest) (model.Observation, error) {
	if !l.admission.enter() {
		return model.Observation{}, l.admission.rejection()
	}
	defer l.admission.leave()
	return l.computer.Observe(ctx, req)
}

// ComputerAct performs an action in the reusable browser session owned by the runner.
func (l *LiveRunner) ComputerAct(ctx context.Context, req model.ActionRequest) (model.Observation, error) {
	if !l.admission.enter() {
		return model.Observation{}, l.admission.rejection()
	}
	defer l.admission.leave()
	return l.computer.Act(ctx, req)
}

func (c *liveComputer) Act(ctx context.Context, req model.ActionRequest) (model.Observation, error) {
	s, err := c.sessionFor(ctx)
	if err != nil {
		return model.Observation{}, err
	}
	return s.Act(ctx, req)
}

func (c *liveComputer) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	s := c.session
	c.session = nil
	if c.sessionCancel != nil {
		defer c.sessionCancel(ErrRuntimeClosed)
		c.sessionCancel = nil
	}
	c.mu.Unlock()
	if s != nil {
		return s.Close()
	}
	return nil
}

// stopStartup cancels construction, not an established session: admitted
// operations must drain before Close releases their shared dependency.
func (c *liveComputer) stopStartup() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopping = true
	if c.startupCancel != nil {
		c.startupCancel(ErrRuntimeClosed)
	}
}

func (c *liveComputer) sessionFor(ctx context.Context) (ComputerSession, error) {
	if c == nil || c.owner == nil {
		return nil, errors.New("computer browser backend is not configured")
	}
	for {
		if err := context.Cause(ctx); err != nil {
			return nil, err
		}
		c.mu.Lock()
		if c.stopping {
			c.mu.Unlock()
			return nil, ErrRuntimeClosed
		}
		if c.session != nil {
			s := c.session
			c.mu.Unlock()
			return s, nil
		}
		if pending := c.starting; pending != nil {
			c.mu.Unlock()
			select {
			case <-pending:
				continue
			case <-ctx.Done():
				return nil, context.Cause(ctx)
			}
		}
		c.starting = make(chan struct{})
		sessionCtx, cancel := context.WithCancelCause(context.WithoutCancel(ctx))
		c.startupCancel = cancel
		c.mu.Unlock()

		// Bridge request cancellation only during construction; stop and join
		// the callback before handing the successful session to its owner.
		bridged := make(chan struct{})
		stop := context.AfterFunc(ctx, func() {
			cancel(context.Cause(ctx))
			close(bridged)
		})
		s, err := c.startSession(sessionCtx)
		if !stop() {
			<-bridged
		}
		if cause := context.Cause(ctx); cause != nil {
			cancel(cause)
		}
		c.mu.Lock()
		if cause := context.Cause(sessionCtx); cause != nil {
			err = cause
		}
		if err != nil {
			cancel(err)
			// Retain startup ownership while cleaning up, but never hold the
			// state lock across an external Close: shutdown must reach its wait.
			c.mu.Unlock()
			if s != nil {
				if closeErr := s.Close(); closeErr != nil {
					err = errors.Join(err, fmt.Errorf("close unpublished computer session: %w", closeErr))
				}
			}
			c.mu.Lock()
		} else {
			c.session, c.sessionCancel = s, cancel
		}
		c.startupCancel = nil
		close(c.starting)
		c.starting = nil
		c.mu.Unlock()
		if err != nil {
			return nil, fmt.Errorf("start computer browser backend: %w", err)
		}
		return s, nil
	}
}

func (c *liveComputer) startSession(ctx context.Context) (ComputerSession, error) {
	headless := true
	var opts []browser.Option
	if settings := c.owner.settings; settings != nil {
		headless = c.forceHeadless || !settings.ComputerBrowserVisible(ctx)
		if cp := settings.ChromeBinPath(ctx); cp != "" {
			opts = append(opts, browser.WithChromePath(cp))
		}
	}
	opts = append([]browser.Option{browser.WithHeadless(headless)}, opts...)
	newSession := c.newSession
	if newSession == nil {
		newSession = newBrowserSession
	}
	return newSession(ctx, opts...)
}
