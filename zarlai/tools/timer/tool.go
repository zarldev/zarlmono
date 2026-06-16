package timer

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
	znotify "github.com/zarldev/zarlmono/zkit/znotify"

	"github.com/zarldev/zarlmono/zarlai/service"
)

type activeTimer struct {
	Label    string
	Deadline time.Time
}

// TimerTool sets a timer that notifies the session when complete.
type TimerTool struct {
	ns     *znotify.NotificationStore
	mu     sync.Mutex
	active map[string][]activeTimer // keyed by session ID
}

// NewTimerTool creates a TimerTool backed by the given notification store.
func NewTimerTool(ns *znotify.NotificationStore) *TimerTool {
	return &TimerTool{
		ns:     ns,
		active: make(map[string][]activeTimer),
	}
}

func (t *TimerTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "timer",
		Description: "Set a countdown timer tied to the current session. Call this when the user says \"remind me in…\", \"set a timer for…\", \"tell me when X minutes is up\", \"wake me in…\", or asks to time cooking/workouts/breaks. You'll be notified automatically when it fires — do NOT promise to wait or poll; just set it and move on. Include a label when the purpose is obvious (e.g. \"pasta\", \"pomodoro\", \"tea steeping\") so the notification is meaningful.",
		Parameters: service.Parameters{
			{Name: "duration", Type: service.ParamString, Description: `Go duration string — "30s", "5m", "1h30m", "2h". No bare numbers; always include the unit.`, Required: true},
			{Name: "label", Type: service.ParamString, Description: "Short label for what the timer is for (e.g. \"pasta\", \"break ends\"). Omit if generic.", Required: false},
		}.ToJSONSchema(),
	}
}

func (t *TimerTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	raw := call.Arguments.String("duration", "")
	if raw == "" {
		return tools.Failure(call.ID, tools.Validation("timer", "duration is required")), nil
	}

	d, err := time.ParseDuration(raw)
	if err != nil {
		return tools.Failure(call.ID, tools.Validation("timer", fmt.Sprintf("duration %q: %v", raw, err))), nil
	}
	if d <= 0 {
		return tools.Failure(call.ID, tools.Validation("timer", fmt.Sprintf("duration must be positive, got %s", raw))), nil
	}

	label := call.Arguments.String("label", "")
	sessionID := service.SessionIDFromCtx(ctx)
	deadline := time.Now().Add(d)

	t.mu.Lock()
	t.active[sessionID] = append(t.active[sessionID], activeTimer{Label: label, Deadline: deadline})
	t.mu.Unlock()

	go func() {
		time.Sleep(d)

		// Remove from active
		t.mu.Lock()
		timers := t.active[sessionID]
		for i, at := range timers {
			if at.Deadline.Equal(deadline) && at.Label == label {
				t.active[sessionID] = append(timers[:i], timers[i+1:]...)
				break
			}
		}
		if len(t.active[sessionID]) == 0 {
			delete(t.active, sessionID)
		}
		t.mu.Unlock()

		content := fmt.Sprintf("Timer complete: %s", label)
		if label == "" {
			content = fmt.Sprintf("Timer complete (%s)", raw)
		}
		// Broadcast: a timer set ten minutes ago is meant for the user
		// who set it, not the session id from that point in time. After
		// a page reload the original session has no live subscriber and
		// the user would never hear it. Fan out to whichever frontend
		// is currently subscribed.
		t.ns.Push(znotify.Notification{
			SessionID: sessionID,
			ToolName:  "timer",
			Content:   content,
			Broadcast: true,
		})
	}()

	return tools.Success(call.ID, fmt.Sprintf("Timer set for %s: %s", raw, label)), nil
}

// StatusTool reports remaining time on active timers.
type StatusTool struct {
	timerTool *TimerTool
}

// NewStatusTool creates a timer status tool linked to the timer tool.
func NewStatusTool(tt *TimerTool) *StatusTool {
	return &StatusTool{timerTool: tt}
}

func (t *StatusTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "timer_status",
		Description: "List any active timers for the current session with their remaining time. Call this when the user asks \"how long left\", \"how much time on my timer\", \"what's still running\", or to check before setting a new timer. Returns \"No active timers.\" if nothing is running.",
		Parameters:  service.Parameters{}.ToJSONSchema(),
	}
}

func (t *StatusTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	sessionID := service.SessionIDFromCtx(ctx)

	t.timerTool.mu.Lock()
	timers := make([]activeTimer, len(t.timerTool.active[sessionID]))
	copy(timers, t.timerTool.active[sessionID])
	t.timerTool.mu.Unlock()

	if len(timers) == 0 {
		return tools.Success(call.ID, "No active timers."), nil
	}

	now := time.Now()
	var sb strings.Builder
	for _, at := range timers {
		remaining := at.Deadline.Sub(now).Round(time.Second)
		name := at.Label
		if name == "" {
			name = "unnamed"
		}
		fmt.Fprintf(&sb, "- %s: %s remaining\n", name, remaining)
	}
	return tools.Success(call.ID, sb.String()), nil
}
