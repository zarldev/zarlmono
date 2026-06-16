package grpc

import znotify "github.com/zarldev/zarlmono/zkit/znotify"

// configChangeTool is the notification tool-name used for all
// "something in my configuration just changed" announcements. The
// frontend routes these through the usual notification→conversation
// path so the agent naturally narrates the change on the next idle
// turn.
const configChangeTool = "config_change"

// emitConfigChange broadcasts a notification describing an operator
// action. Kept narrow on purpose — only fires for user-facing
// lifecycle changes (new skill, prompt swap, LLM swap). Iteration-
// heavy edits like tool-description or template tweaks don't emit;
// they'd turn into constant chatter from the avatar.
func (a *AdminServer) emitConfigChange(message string) {
	if a.notifications == nil || message == "" {
		return
	}
	a.notifications.Push(znotify.Notification{
		ToolName:  configChangeTool,
		Content:   message,
		Broadcast: true,
	})
}
