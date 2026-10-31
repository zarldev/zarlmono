package rewind_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func googleReplayHistory() []llm.Message {
	return []llm.Message{
		{Role: llm.RoleAssistant, ReasoningContent: "display only", ToolCalls: []llm.ToolCall{{ID: "call", Type: "function", OutputIndex: llm.OutputPosition(2), Function: llm.ToolCallFunction{Name: "read", Arguments: `{"path":"one","n":9007199254740993}`}}}, ContinuationItems: []llm.ContinuationItem{{Provider: "google", Format: "generate_content.part.v1", Kind: "function_call", ID: "call", OutputIndex: llm.OutputPosition(2), Data: []byte(`{"functionCall":{"name":"read","args":{"n":9007199254740993,"path":"one"}},"thoughtSignature":"c2ln"}`)}}},
		{Role: llm.RoleTool, ToolCallID: "call", Content: "ok"},
	}
}

func TestGoogleReplayBindsOneLogicalOutput(t *testing.T) {
	for _, route := range []string{"gemini", "google-vertex"} {
		t.Run(route, func(t *testing.T) {
			if err := rewind.ValidateLegacyContext(googleReplayHistory(), route); err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				name   string
				change func([]llm.Message)
			}{
				{"foreign", func(m []llm.Message) { m[0].ContinuationItems[0].Provider = "gemini" }},
				{"orphan", func(m []llm.Message) { m[0].ContinuationItems[0].ID = "orphan" }},
				{"name", func(m []llm.Message) { m[0].ToolCalls[0].Function.Name = "write" }},
				{"arguments", func(m []llm.Message) { m[0].ToolCalls[0].Function.Arguments = `{"path":"PRIVATE-canary"}` }},
				{"large integer mismatch", func(m []llm.Message) { m[0].ToolCalls[0].Function.Arguments = `{"path":"one","n":9007199254740992}` }},
				{"position", func(m []llm.Message) { m[0].ToolCalls[0].OutputIndex = llm.OutputPosition(3) }},
				{"missing position", func(m []llm.Message) { m[0].ToolCalls[0].OutputIndex = nil }},
				{"duplicate native", func(m []llm.Message) {
					m[0].ContinuationItems = append(m[0].ContinuationItems, m[0].ContinuationItems[0])
				}},
				{"collision", func(m []llm.Message) { m[0].Content = "answer"; m[0].ContentOutputIndex = llm.OutputPosition(2) }},
				{"missing signature", func(m []llm.Message) {
					m[0].ContinuationItems[0].Data = []byte(`{"functionCall":{"name":"read","args":{}}}`)
				}},
				{"unsupported part field", func(m []llm.Message) {
					m[0].ContinuationItems[0].Data = []byte(`{"text":"PRIVATE-canary","functionCall":{"name":"read","args":{}},"thoughtSignature":"c2ln"}`)
				}},
				{"duplicate JSON", func(m []llm.Message) {
					m[0].ContinuationItems[0].Data = []byte(`{"functionCall":{"name":"read","args":{"path":"one","path":"one"}},"thoughtSignature":"c2ln"}`)
				}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					m := googleReplayHistory()
					tc.change(m)
					err := rewind.ValidateLegacyContext(m, route)
					if !errors.Is(err, rewind.ErrInvalid) {
						t.Fatalf("got %v", err)
					}
					if strings.Contains(err.Error(), "PRIVATE-canary") {
						t.Fatal("private payload leaked")
					}
				})
			}
		})
	}
	if err := rewind.ValidateLegacyContext(googleReplayHistory(), "google"); !errors.Is(err, rewind.ErrInvalid) {
		t.Fatal("native owner admitted as saved route")
	}
}

func TestGoogleReplayRejectsUnsupportedImagesAndPromptMismatch(t *testing.T) {
	for _, image := range []llm.ImageData{
		{URL: "https://example.test/a.png"},
		{DataURI: "data:image/png;base64,YQ=="},
		{DataURI: "data:image/png;base64,YQ==", MIMEType: "image/jpeg"},
		{DataURI: "data:image/png;base64,YQ==", MIMEType: "image/png", Detail: "high"},
	} {
		m := []llm.Message{{Role: llm.RoleUser, Parts: []llm.ContentPart{{Type: llm.ContentTypeImage, Image: &image}}}}
		if err := rewind.ValidateLegacyContext(m, "gemini"); !errors.Is(err, rewind.ErrInvalid) {
			t.Fatalf("unsupported image accepted: %#v", image)
		}
	}
	m := []llm.Message{{Role: llm.RoleUser, Content: "prompt", Parts: []llm.ContentPart{{Type: llm.ContentTypeText, Text: "different"}}}}
	if err := rewind.ValidateLegacyContext(m, "gemini"); !errors.Is(err, rewind.ErrInvalid) {
		t.Fatal("contradictory prompt accepted")
	}
}

func TestNewOrdinaryReplayRoutesRemainUnqualifiedForExactResume(t *testing.T) {
	for _, route := range []string{"llamacpp", "ollama", "deepseek", "gemini", "google-vertex", "claude-code"} {
		m := []llm.Message{{Role: llm.RoleAssistant, Content: "answer", ReasoningContent: "retained display"}}
		if err := rewind.ValidateLegacyContext(m, route); err != nil {
			t.Fatalf("%s: %v", route, err)
		}
		if err := rewind.ValidateContext(m, route); !errors.Is(err, rewind.ErrInvalid) {
			t.Fatalf("%s silently qualified exact replay", route)
		}
	}
}
