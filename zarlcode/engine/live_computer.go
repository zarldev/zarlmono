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
	return l.computer.Observe(ctx, req)
}

// ComputerAct performs an action in the reusable browser session owned by the runner.
func (l *LiveRunner) ComputerAct(ctx context.Context, req model.ActionRequest) (model.Observation, error) {
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
	c.mu.Unlock()
	if s != nil {
		return s.Close()
	}
	return nil
}

func (c *liveComputer) sessionFor(ctx context.Context) (ComputerSession, error) {
	if c == nil || c.owner == nil {
		return nil, errors.New("computer browser backend is not configured")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session != nil {
		return c.session, nil
	}

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
	// The owned session spans tool calls and is closed by LiveRunner.Close. Detach
	// only cancellation; request values remain available during session setup.
	s, err := newSession(context.WithoutCancel(ctx), opts...)
	if err != nil {
		return nil, fmt.Errorf("start computer browser backend: %w", err)
	}
	c.session = s
	return s, nil
}
