package rewind_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestProviderReplayShape(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, provider string
		message        llm.Message
	}{
		{"openai mixed content", "openai", llm.Message{Role: llm.RoleUser, Content: "lost", Parts: []llm.ContentPart{{Type: llm.ContentTypeText, Text: "kept"}}}},
		{"codex mixed content", "openai-codex", llm.Message{Role: llm.RoleUser, Content: "lost", Parts: []llm.ContentPart{{Type: llm.ContentTypeText, Text: "kept"}}}},
		{"openai reasoning", "openai", llm.Message{Role: llm.RoleAssistant, Content: "answer", ReasoningContent: "lost"}},
		{"anthropic empty user", "anthropic", llm.Message{Role: llm.RoleUser}},
		{"anthropic MIME contradiction", "anthropic", llm.Message{Role: llm.RoleUser, Parts: []llm.ContentPart{{Type: llm.ContentTypeImage, Image: &llm.ImageData{DataURI: "data:image/png;base64,YQ==", MIMEType: "image/jpeg"}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := rewind.ValidateContext([]llm.Message{tc.message}, tc.provider); !errors.Is(err, rewind.ErrInvalid) {
				t.Fatalf("ValidateContext: %v", err)
			}
		})
	}
}

func TestNativeOutputOrdering(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mutate func(*llm.Message)
		err    error
	}{
		{"consistent", func(*llm.Message) {}, nil},
		{"contradictory projection", func(m *llm.Message) { m.Content = "lost" }, rewind.ErrInvalid},
		{"wrong projection index", func(m *llm.Message) { m.ContentOutputIndex = llm.OutputPosition(2) }, rewind.ErrInvalid},
		{"duplicate native index", func(m *llm.Message) { m.ContinuationItems = append(m.ContinuationItems, m.ContinuationItems[0]) }, rewind.ErrInvalid},
		{"missing native index", func(m *llm.Message) { m.ContinuationItems[0].OutputIndex = nil }, rewind.ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := llm.Message{Role: llm.RoleAssistant, Content: "answer", ContentOutputIndex: llm.OutputPosition(0), ContinuationItems: []llm.ContinuationItem{{Provider: "anthropic", Format: "content_block.v1", Kind: "text", Data: []byte(`{"type":"text","text":"answer"}`), OutputIndex: llm.OutputPosition(0)}}}
			tc.mutate(&m)
			if err := rewind.ValidateContext([]llm.Message{m}, "anthropic"); !errors.Is(err, tc.err) {
				t.Fatalf("ValidateContext: %v, want %v", err, tc.err)
			}
		})
	}
}

func TestToolReplayMetadata(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, provider, output  string
		contentIndex, callIndex *int
	}{
		{"duplicate projected index", "openai-codex", "result", llm.OutputPosition(0), llm.OutputPosition(0)},
		{"ambiguous projected index", "anthropic", "result", llm.OutputPosition(0), nil},
		{"empty codex result", "openai-codex", "", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			call := toolCall("call")
			call.OutputIndex = tc.callIndex
			messages := []llm.Message{{Role: llm.RoleAssistant, Content: "answer", ContentOutputIndex: tc.contentIndex, ToolCalls: []llm.ToolCall{call}}, {Role: llm.RoleTool, ToolCallID: "call", Content: tc.output}}
			if err := rewind.ValidateContext(messages, tc.provider); !errors.Is(err, rewind.ErrInvalid) {
				t.Fatalf("ValidateContext: %v", err)
			}
		})
	}
}

func googleCompleteHistory() []llm.Message {
	return []llm.Message{{
		Role: llm.RoleAssistant, Content: "beforeafter", ReasoningContent: "private thought", ContentOutputIndex: llm.OutputPosition(2),
		ToolCalls: []llm.ToolCall{
			{ID: "first", Type: "function", OutputIndex: llm.OutputPosition(4), Function: llm.ToolCallFunction{Name: "read", Arguments: `{"n":9007199254740993,"path":"one"}`}},
			{ID: "second", Type: "function", OutputIndex: llm.OutputPosition(8), Function: llm.ToolCallFunction{Name: "write", Arguments: `{"path":"two"}`}},
		},
		ContinuationItems: []llm.ContinuationItem{
			googleCompleteItem(0, "", `{"text":"private thought","thought":true,"thoughtSignature":"c2ln"}`),
			googleCompleteItem(2, "", `{"text":"before","thoughtSignature":"c2ln"}`),
			googleCompleteItem(3, "", `{"thoughtSignature":"c2ln"}`),
			googleCompleteItem(4, "first", `{"functionCall":{"id":"first","name":"read","args":{"path":"one","n":9007199254740993}},"thoughtSignature":"c2ln"}`),
			googleCompleteItem(6, "", `{"text":"after"}`),
			googleCompleteItem(8, "second", `{"functionCall":{"name":"write","args":{"path":"two"}}}`),
		},
	}, {Role: llm.RoleTool, ToolCallID: "first", Content: "ok"}, {Role: llm.RoleTool, ToolCallID: "second", Content: "ok"}}
}

func googleCompleteItem(index int, id, data string) llm.ContinuationItem {
	return llm.ContinuationItem{Provider: "google", Format: "generate_content.part.v2", Kind: "part", ID: id, Data: []byte(data), OutputIndex: llm.OutputPosition(index)}
}

func TestGoogleCompleteOrdinaryHistoryValidation(t *testing.T) {
	t.Parallel()
	for _, route := range []string{"gemini", "google-vertex"} {
		t.Run(route, func(t *testing.T) {
			t.Parallel()
			for _, tc := range []struct {
				name   string
				change func([]llm.Message)
				err    error
			}{
				{"complete signed text thought signature and calls", func([]llm.Message) {}, nil},
				{"absent display text", func(m []llm.Message) { m[0].Content = ""; m[0].ContentOutputIndex = nil; m[0].ReasoningContent = "" }, nil},
				{"foreign owner", func(m []llm.Message) { m[0].ContinuationItems[0].Provider = "gemini" }, rewind.ErrInvalid},
				{"foreign kind", func(m []llm.Message) { m[0].ContinuationItems[0].Kind = "function_call" }, rewind.ErrInvalid},
				{"mixed versions", func(m []llm.Message) {
					m[0].ContinuationItems[3].Format = "generate_content.part.v1"
					m[0].ContinuationItems[3].Kind = "function_call"
				}, rewind.ErrInvalid},
				{"future format", func(m []llm.Message) { m[0].ContinuationItems[1].Format = "generate_content.part.v3" }, rewind.ErrInvalid},
				{"text ID", func(m []llm.Message) { m[0].ContinuationItems[0].ID = "first" }, rewind.ErrInvalid},
				{"wrong visible text", func(m []llm.Message) { m[0].Content = "PRIVATE-canary" }, rewind.ErrInvalid},
				{"wrong thought", func(m []llm.Message) { m[0].ReasoningContent = "PRIVATE-canary" }, rewind.ErrInvalid},
				{"wrong text position", func(m []llm.Message) { m[0].ContentOutputIndex = llm.OutputPosition(6) }, rewind.ErrInvalid},
				{"missing position", func(m []llm.Message) { m[0].ContinuationItems[0].OutputIndex = nil }, rewind.ErrInvalid},
				{"negative position", func(m []llm.Message) { m[0].ContinuationItems[0].OutputIndex = llm.OutputPosition(-1) }, rewind.ErrInvalid},
				{"position collision", func(m []llm.Message) { m[0].ContinuationItems[1].OutputIndex = llm.OutputPosition(0) }, rewind.ErrInvalid},
				{"out of order parts", func(m []llm.Message) {
					m[0].ContinuationItems[0], m[0].ContinuationItems[1] = m[0].ContinuationItems[1], m[0].ContinuationItems[0]
				}, rewind.ErrInvalid},
				{"out of order calls", func(m []llm.Message) { m[0].ToolCalls[0], m[0].ToolCalls[1] = m[0].ToolCalls[1], m[0].ToolCalls[0] }, rewind.ErrInvalid},
				{"duplicate call binding", func(m []llm.Message) { m[0].ToolCalls[1].ID = "first"; m[0].ContinuationItems[5].ID = "first" }, rewind.ErrInvalid},
				{"wrong call position", func(m []llm.Message) { m[0].ToolCalls[0].OutputIndex = llm.OutputPosition(3) }, rewind.ErrInvalid},
				{"missing call position", func(m []llm.Message) { m[0].ToolCalls[0].OutputIndex = nil }, rewind.ErrInvalid},
				{"wrong call name", func(m []llm.Message) { m[0].ToolCalls[0].Function.Name = "write" }, rewind.ErrInvalid},
				{"wrong call ID", func(m []llm.Message) { m[0].ContinuationItems[3].ID = "other" }, rewind.ErrInvalid},
				{"undeclared native call", func(m []llm.Message) { m[0].ToolCalls = m[0].ToolCalls[:1] }, rewind.ErrInvalid},
				{"unrepresented declared call", func(m []llm.Message) { m[0].ContinuationItems = m[0].ContinuationItems[:5] }, rewind.ErrInvalid},
				{"different arguments", func(m []llm.Message) { m[0].ToolCalls[0].Function.Arguments = `{"path":"PRIVATE-canary"}` }, rewind.ErrInvalid},
				{"wrong result order", func(m []llm.Message) { m[1], m[2] = m[2], m[1] }, rewind.ErrInvalid},
			} {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()
					messages := googleCompleteHistory()
					tc.change(messages)
					before := llm.CloneMessages(messages)
					err := rewind.ValidateLegacyContext(messages, route)
					if !errors.Is(err, tc.err) {
						t.Fatalf("ValidateLegacyContext: %v, want %v", err, tc.err)
					}
					if !reflect.DeepEqual(before, messages) {
						t.Fatal("validation mutated borrowed history")
					}
					if err != nil && strings.Contains(err.Error(), "PRIVATE-canary") {
						t.Fatal("private data leaked")
					}
				})
			}
			if err := rewind.ValidateContext(googleCompleteHistory(), route); !errors.Is(err, rewind.ErrInvalid) {
				t.Fatal("ordinary v2 history silently qualified as exact replay")
			}
		})
	}
}

func TestGoogleCompleteOrdinaryHistoryRejectsUnverifiedNativeShapes(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		`null`, `{}`, `{"text":"one","text":"two"}`, `{"text":null}`, `{"thought":true}`, `{"text":"answer","thought":"yes"}`,
		`{"text":"answer","future":true}`, `{"inlineData":{"mimeType":"image/png","data":"YQ=="}}`,
		`{"text":"answer","partMetadata":{"open":true}}`, `{"thoughtSignature":"!"}`,
		`{"text":"answer","thoughtSignature":null}`, `{"text":"answer","thoughtSignature":[115,105,103]}`,
		`{"functionCall":null}`, `{"functionCall":{}}`, `{"functionCall":{"name":""}}`, `{"functionCall":{"name":"read","extra":true}}`,
		`{"functionCall":{"id":"other","name":"read"}}`, `{"functionCall":{"name":"read","args":[]}}`,
		`{"functionCall":{"name":"read","args":{"a":1,"a":1}}}`, `{"functionCall":{"name":"read","args":{"a":{"x":1,"x":1}}}}`,
		`{"functionCall":{"name":"read"},"text":"also text"}`, `{"functionCall":{"name":"read"},"thought":true}`,
		`{"functionCall":{"name":"read","args":{"n":1e9999999999999999999999999}}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			messages := []llm.Message{{Role: llm.RoleAssistant, ContinuationItems: []llm.ContinuationItem{googleCompleteItem(0, "", raw)}}}
			if strings.Contains(raw, `"functionCall"`) {
				messages[0].ContinuationItems[0].ID = "call"
				args := "{}"
				if strings.Contains(raw, `"a":1`) {
					args = `{"a":1}`
				}
				messages[0].ToolCalls = []llm.ToolCall{{ID: "call", Type: "function", OutputIndex: llm.OutputPosition(0), Function: llm.ToolCallFunction{Name: "read", Arguments: args}}}
				messages = append(messages, llm.Message{Role: llm.RoleTool, ToolCallID: "call", Content: "ok"})
			}
			before := llm.CloneMessages(messages)
			if err := rewind.ValidateLegacyContext(messages, "gemini"); !errors.Is(err, rewind.ErrInvalid) {
				t.Fatalf("ValidateLegacyContext: %v", err)
			}
			if !reflect.DeepEqual(before, messages) {
				t.Fatal("rejection changed history")
			}
		})
	}
}

func TestGoogleCompleteOrdinaryHistoryExactArguments(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, native, projected string
		err                     error
	}{
		{"integer precision", `{"n":9007199254740993}`, `{"n":9007199254740993.0}`, nil},
		{"integer mismatch", `{"n":9007199254740993}`, `{"n":9007199254740992}`, rewind.ErrInvalid},
		{"fraction precision", `{"n":0.10000000000000001}`, `{"n":0.10000000000000001}`, nil},
		{"fraction mismatch", `{"n":0.10000000000000001}`, `{"n":0.1}`, rewind.ErrInvalid},
		{"equivalent exponent", `{"n":123.00e-2}`, `{"n":1.23}`, nil},
		{"negative zero", `{"n":-0.00e-9999999999999999999999999}`, `{"n":0}`, nil},
		{"bounded tiny exponent", `{"n":10e-9999999999999999999999999}`, `{"n":1e-9999999999999999999999998}`, nil},
		{"underflow not zero", `{"n":1e-9999999999999999999999999}`, `{"n":0}`, rewind.ErrInvalid},
		{"underflow mismatch", `{"n":1e-9999999999999999999999999}`, `{"n":1e-9999999999999999999999998}`, rewind.ErrInvalid},
		{"subnormal mismatch", `{"n":1e-324}`, `{"n":2e-324}`, rewind.ErrInvalid},
		{"smallest subnormal", `{"n":5e-324}`, `{"n":50e-325}`, nil},
		{"SDK overflow", `{"n":1e309}`, `{"n":1e309}`, rewind.ErrInvalid},
		{"largest finite", `{"n":1.7976931348623157e308}`, `{"n":17976931348623157e292}`, nil},
		{"nested equality", `{"a":[true,{"n":1.00},null]}`, `{"a":[true,{"n":1e0},null]}`, nil},
		{"nested null not object", `{"a":null}`, `{"a":{}}`, rewind.ErrInvalid},
		{"nested array order", `{"a":[1,2]}`, `{"a":[2,1]}`, rewind.ErrInvalid},
		{"scalar not argument object", `1`, `1`, rewind.ErrInvalid},
		{"duplicate projection keys", `{"a":1}`, `{"a":1,"a":1}`, rewind.ErrInvalid},
		{"omitted parameterless", ``, `{}`, nil},
		{"null parameterless", `null`, `{}`, nil},
		{"empty parameterless", `{}`, `null`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			args := ""
			if tc.native != "" {
				args = `,"args":` + tc.native
			}
			messages := []llm.Message{{Role: llm.RoleAssistant,
				ToolCalls:         []llm.ToolCall{{ID: "call", Type: "function", OutputIndex: llm.OutputPosition(0), Function: llm.ToolCallFunction{Name: "read", Arguments: tc.projected}}},
				ContinuationItems: []llm.ContinuationItem{googleCompleteItem(0, "call", `{"functionCall":{"name":"read"`+args+`}}`)},
			}, {Role: llm.RoleTool, ToolCallID: "call", Content: "ok"}}
			before := llm.CloneMessages(messages)
			if err := rewind.ValidateLegacyContext(messages, "gemini"); !errors.Is(err, tc.err) {
				t.Fatalf("ValidateLegacyContext: %v, want %v", err, tc.err)
			}
			if !reflect.DeepEqual(before, messages) {
				t.Fatal("comparison normalized retained arguments")
			}
		})
	}
}

func TestGoogleCompleteOrdinaryHistoryPersistence(t *testing.T) {
	for _, route := range []string{"gemini", "google-vertex"} {
		t.Run(route, func(t *testing.T) {
			restored := reopenProviderHistory(t, route, googleCompleteHistory())
			if err := rewind.ValidateContext(restored, route); !errors.Is(err, rewind.ErrInvalid) {
				t.Fatal("persisted ordinary snapshot silently qualified as exact replay")
			}
		})
	}
}

func TestGoogleCompleteOrdinaryNativeOnlyParts(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		`{"text":"answer"}`, `{"text":"private thought","thought":true}`,
		`{"thoughtSignature":"c2ln"}`, `{"text":"answer","thoughtSignature":"c2ln"}`,
	} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			messages := []llm.Message{{Role: llm.RoleAssistant, ContinuationItems: []llm.ContinuationItem{googleCompleteItem(0, "", raw)}}}
			if err := rewind.ValidateLegacyContext(messages, "gemini"); err != nil {
				t.Fatal(err)
			}
		})
	}
}
