package browser

import (
	"context"
	"fmt"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/zarldev/zarlmono/zkit/agent/computer"
)

func (s *Session) waitForTrigger(ctx context.Context, trigger computer.Trigger) error {
	deadline := time.NewTimer(s.actionTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		ok, err := s.checkTrigger(ctx, trigger)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("timed out waiting for trigger %q", trigger.Kind.String())
		case <-ticker.C:
		}
	}
}

func (s *Session) checkTrigger(ctx context.Context, trigger computer.Trigger) (bool, error) {
	var ok bool
	var script string

	switch trigger.Kind {
	case computer.TriggerKinds.VISIBLE:
		script = targetPredicateScript(trigger.Target, "visible", "", "")
	case computer.TriggerKinds.HIDDEN:
		script = targetPredicateScript(trigger.Target, "hidden", "", "")
	case computer.TriggerKinds.FOCUSED:
		script = targetPredicateScript(trigger.Target, "focused", "", "")
	case computer.TriggerKinds.TEXTPRESENT:
		script = targetPredicateScript(trigger.Target, "text_present", trigger.Text, "")
	case computer.TriggerKinds.VALUEEQUALS:
		script = targetPredicateScript(trigger.Target, "value_equals", "", trigger.Value)
	case computer.TriggerKinds.URLMATCHES:
		script = fmt.Sprintf(`window.location.href.includes(%s)`, jsString(firstNonEmpty(trigger.URL, trigger.Value, trigger.Text)))
	case computer.TriggerKinds.NAVIGATIONCOMPLETE:
		script = `document.readyState === "complete"`
	case computer.TriggerKinds.SURFACESTABLE:
		if s.settleWait > 0 {
			if err := s.run(ctx, chromedp.Sleep(s.settleWait)); err != nil {
				return false, fmt.Errorf("wait for stable surface: %w", err)
			}
		}
		return true, nil
	default:
		return false, fmt.Errorf("unsupported browser trigger kind %q", trigger.Kind.String())
	}

	if err := s.run(ctx, chromedp.Evaluate(script, &ok)); err != nil {
		return false, fmt.Errorf("check trigger %q: %w", trigger.Kind.String(), err)
	}
	return ok, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
