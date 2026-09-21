package rewind_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestScreenshotContextRoundTrip(t *testing.T) {
	for _, provider := range []string{"openai", "openai-codex", "anthropic"} {
		t.Run(provider, func(t *testing.T) {
			for _, role := range []string{llm.RoleUser, llm.RoleTool} {
				t.Run(role, func(t *testing.T) {
					image := llm.ImagePartFromDataURI("data:image/png;base64,cG5n", "image/png")
					message := llm.Message{Role: role, Parts: []llm.ContentPart{image}}
					var messages []llm.Message
					if role == llm.RoleTool {
						message.ToolCallID, message.Content = "screen", "screenshot metadata"
						messages = append(messages, llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{toolCall("screen")}})
					}
					messages = append(messages, message)
					encoded, err := rewind.EncodeResume(10, messages, rewind.Target{Provider: provider, Model: "gpt-5.6"}, "turn", 10, rewind.InitialContinuation{})
					if err != nil {
						t.Fatal(err)
					}
					decoded, err := rewind.DecodeResume(encoded)
					if err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(decoded.Context, messages) {
						t.Fatal("screenshot bytes or metadata changed during exact round trip")
					}
				})
			}
		})
	}
}

func TestScreenshotContextRejectsContradictoryMIME(t *testing.T) {
	for _, provider := range []string{"openai", "openai-codex", "anthropic"} {
		t.Run(provider, func(t *testing.T) {
			for _, uri := range []string{"data:image/png;base64,cG5n", "data:image/jpeg;base64,not-base64"} {
				messages := []llm.Message{{Role: llm.RoleUser, Parts: []llm.ContentPart{llm.ImagePartFromDataURI(uri, "image/jpeg")}}}
				if err := rewind.ValidateContext(messages, provider); !errors.Is(err, rewind.ErrInvalid) {
					t.Fatalf("invalid image admitted: %v", err)
				}
			}
		})
	}
}
