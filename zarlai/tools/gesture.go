package tools

import (
	"context"
	"encoding/json"
	"fmt"

	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
	znotify "github.com/zarldev/zarlmono/zkit/znotify"

	"github.com/zarldev/zarlmono/zarlai/service"
)

// GestureTool lets the conversation or background task trigger a gesture /
// mood on the avatar. The payload is pushed as a notification; the frontend's
// TalkingHead consumes it once (via `gestureCue`).
type GestureTool struct {
	notifications *znotify.NotificationStore
}

func NewGestureTool(notifications *znotify.NotificationStore) *GestureTool {
	return &GestureTool{notifications: notifications}
}

var validGestures = []string{
	// Built-in talkinghead library gestures.
	"handup", "index", "ok", "thumbup", "thumbdown", "side", "shrug", "namaste",
	// Custom templates (see frontend/src/talkingHeadGestures.ts).
	"wave", "peace", "stop", "pointself", "fistpump", "beckon",
}
var validMoods = []string{"neutral", "happy", "angry", "sad", "fear", "disgust", "love", "sleep"}

func (t *GestureTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name: "gesture",
		Description: "CALL EVERY TURN. You are an on-screen embodied presence — a silent face is uncanny. Invoke this at the start of every spoken response, in parallel with any other tool calls. Pick a gesture+mood pair that matches what you're about to say:\n" +
			"- greeting / hello → wave+happy (or namaste+happy for formal)\n" +
			"- farewell → wave\n" +
			"- agreement / confirming → thumbup+happy, ok, or fistpump for excitement\n" +
			"- refusal / declining → thumbdown or stop\n" +
			"- uncertainty / 'I don't know' → shrug\n" +
			"- explaining / making a point → index or peace for 'two things'\n" +
			"- referring to self / 'I' / 'me' → pointself\n" +
			"- inviting user in / 'come here' → beckon\n" +
			"- 'wait' / 'hold on' → stop\n" +
			"- casual acknowledgement → ok\n" +
			"- asking / interjecting → handup\n" +
			"- gesturing toward on-screen content → side\n" +
			"Mood alone (no gesture) is for emotional reactions: sad on bad news, love for warmth, angry/fear/disgust for strong reactions, sleep when idling, neutral for routine info. Fires once per turn — skipping it leaves you appearing mute.",
		Parameters: service.Parameters{
			{Name: "gesture", Type: service.ParamString, Description: "Body gesture to play. Pick the one that fits your reply.", Required: false, Enum: validGestures},
			{Name: "mood", Type: service.ParamString, Description: "Facial mood to hold briefly. Pick based on the emotional tone of your reply.", Required: false, Enum: validMoods},
		}.ToJSONSchema(),
	}
}

type gestureCuePayload struct {
	Gesture string `json:"gesture,omitempty"`
	Mood    string `json:"mood,omitempty"`
}

func (t *GestureTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	gesture := call.Arguments.String("gesture", "")
	mood := call.Arguments.String("mood", "")
	if gesture == "" && mood == "" {
		return tools.Failure(call.ID, tools.Validation("gesture", "one of gesture or mood is required")), nil
	}

	sessionID := service.SessionIDFromCtx(ctx)
	if t.notifications == nil || sessionID == "" {
		return tools.Success(call.ID, "Gesture acknowledged (no active session to animate)."), nil
	}

	payload, err := json.Marshal(gestureCuePayload{Gesture: gesture, Mood: mood})
	if err != nil {
		return tools.Failure(call.ID, tools.Transient("gesture", err)), nil
	}
	t.notifications.Push(znotify.Notification{
		SessionID: sessionID,
		ToolName:  "gesture",
		Content:   string(payload),
		Broadcast: true,
	})

	return tools.Success(call.ID, fmt.Sprintf("Gesture played (gesture=%q mood=%q).", gesture, mood)), nil
}
