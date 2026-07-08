package browser

import (
	"context"
	"errors"
	"fmt"

	"github.com/chromedp/chromedp"
	"github.com/zarldev/zarlmono/zkit/agent/computer"
)

// Act performs the requested browser action, honoring optional When and Until
// triggers, then returns a fresh observation of the browser surface.
func (s *Session) Act(ctx context.Context, req computer.ActionRequest) (computer.Observation, error) {
	if req.When != nil {
		if err := s.waitForTrigger(ctx, *req.When); err != nil {
			return computer.Observation{}, fmt.Errorf("wait for action precondition: %w", err)
		}
	}

	if err := s.act(ctx, req.Action); err != nil {
		return computer.Observation{}, err
	}

	if req.Until != nil {
		if err := s.waitForTrigger(ctx, *req.Until); err != nil {
			return computer.Observation{}, fmt.Errorf("wait for action completion: %w", err)
		}
	} else if s.settleWait > 0 {
		if err := s.run(ctx, chromedp.Sleep(s.settleWait)); err != nil {
			return computer.Observation{}, fmt.Errorf("settle browser surface: %w", err)
		}
	}

	return s.Observe(ctx, computer.ObserveRequest{IncludeText: true, IncludeTargets: true})
}

func (s *Session) act(ctx context.Context, action computer.Action) error {
	switch action.Kind {
	case computer.ActionKinds.NAVIGATE:
		if action.URL == "" {
			return errors.New("navigate action requires url")
		}
		if err := s.run(ctx,
			chromedp.Navigate(action.URL),
			chromedp.WaitReady("body"),
		); err != nil {
			return fmt.Errorf("navigate to %q: %w", action.URL, err)
		}
		return nil
	case computer.ActionKinds.CLICK:
		if err := s.runTargetScript(ctx, action.Target, "click", "", ""); err != nil {
			return fmt.Errorf("click target: %w", err)
		}
		return nil
	case computer.ActionKinds.FILL:
		if err := s.runTargetScript(ctx, action.Target, "fill", action.Value, ""); err != nil {
			return fmt.Errorf("fill target: %w", err)
		}
		return nil
	case computer.ActionKinds.PRESS:
		if action.Target != nil {
			if err := s.runTargetScript(ctx, action.Target, "focus", "", ""); err != nil {
				return fmt.Errorf("focus target before key press: %w", err)
			}
		}
		if action.Key == "" {
			return errors.New("press action requires key")
		}
		if err := s.run(ctx, chromedp.KeyEvent(action.Key)); err != nil {
			return fmt.Errorf("press key %q: %w", action.Key, err)
		}
		return nil
	case computer.ActionKinds.SCROLL:
		delta := computer.Point{Y: 600}
		if action.Delta != nil {
			delta = *action.Delta
		}
		if err := s.run(ctx, chromedp.Evaluate(fmt.Sprintf(`window.scrollBy(%d, %d)`, delta.X, delta.Y), nil)); err != nil {
			return fmt.Errorf("scroll browser surface: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported browser action kind %q", action.Kind.String())
	}
}
