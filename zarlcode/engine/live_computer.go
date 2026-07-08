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

	mu      sync.Mutex
	session computerSession
}

func (c *liveComputer) Observe(ctx context.Context, req model.ObserveRequest) (model.Observation, error) {
	s, err := c.sessionFor(ctx)
	if err != nil {
		return model.Observation{}, err
	}
	return s.Observe(ctx, req)
}

type computerSession interface {
	model.Observer
	model.Actor
	Close() error
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

func (c *liveComputer) sessionFor(ctx context.Context) (computerSession, error) {
	if c == nil || c.owner == nil {
		return nil, errors.New("computer browser backend is not configured")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session != nil {
		return c.session, nil
	}

	var opts []browser.Option
	if settings := c.owner.settings; settings != nil {
		if cp := settings.ChromeBinPath(c.owner.parentContext()); cp != "" {
			opts = append(opts, browser.WithChromePath(cp))
		}
	}
	s, err := browser.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("start computer browser backend: %w", err)
	}
	c.session = s
	return s, nil
}
