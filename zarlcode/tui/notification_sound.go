package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

const terminalBell = "\a"

func notificationSoundCmd() tea.Cmd {
	return tea.Raw(terminalBell)
}

func notificationSoundMode(ctx context.Context, settings *engine.Settings) string {
	if settings == nil {
		return "completion"
	}
	return settings.NotificationSounds(ctx)
}

func completionSoundCmd(ctx context.Context, settings *engine.Settings) tea.Cmd {
	if notificationSoundMode(ctx, settings) == "off" {
		return nil
	}
	return notificationSoundCmd()
}

func planProgressSoundCmd(ctx context.Context, settings *engine.Settings, before, after code.Plan) tea.Cmd {
	if notificationSoundMode(ctx, settings) != "all" || newlyCompletedPlanSteps(before, after) == 0 {
		return nil
	}
	return notificationSoundCmd()
}

func newlyCompletedPlanSteps(before, after code.Plan) int {
	beforeCompleted := make(map[string]int)
	for _, step := range before.Steps {
		if step.Status == code.StepStatuses.COMPLETED {
			beforeCompleted[step.Text]++
		}
	}
	count := 0
	for _, step := range after.Steps {
		if step.Status != code.StepStatuses.COMPLETED {
			continue
		}
		if beforeCompleted[step.Text] > 0 {
			beforeCompleted[step.Text]--
			continue
		}
		count++
	}
	return count
}
