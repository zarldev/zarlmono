package tools_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestParseFromText(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		content     string
		wantName    tools.ToolName
		wantArg     string
		wantArgVal  string
		wantNoCalls bool
		wantRemain  string
	}{
		{
			name:       "tool_call tag",
			content:    `Let me check. <tool_call>{"name": "get_state", "arguments": {"entity_id": "light.kitchen"}}</tool_call>`,
			wantName:   "get_state",
			wantArg:    "entity_id",
			wantArgVal: "light.kitchen",
			wantRemain: "Let me check.",
		},
		{
			name:       "unclosed tool_call tag",
			content:    `<tool_call>{"name": "web_search", "arguments": {"query": "cats"}}`,
			wantName:   "web_search",
			wantArg:    "query",
			wantArgVal: "cats",
			wantRemain: "",
		},
		{
			name:       "fenced json block",
			content:    "sure\n```json\n{\"name\": \"timer\", \"arguments\": {\"duration\": \"5m\"}}\n```",
			wantName:   "timer",
			wantArg:    "duration",
			wantArgVal: "5m",
			wantRemain: "sure",
		},
		{
			name:       "bare json object",
			content:    `{"name": "remember", "arguments": {"fact": "user likes coffee"}}`,
			wantName:   "remember",
			wantArg:    "fact",
			wantArgVal: "user likes coffee",
			wantRemain: "",
		},
		{
			name:       "parameters alias",
			content:    `<tool_call>{"name": "recall", "parameters": {"query": "allergies"}}</tool_call>`,
			wantName:   "recall",
			wantArg:    "query",
			wantArgVal: "allergies",
			wantRemain: "",
		},
		{
			name:       "stringified arguments",
			content:    `<tool_call>{"name": "web_search", "arguments": "{\"query\": \"test\"}"}</tool_call>`,
			wantName:   "web_search",
			wantArg:    "query",
			wantArgVal: "test",
			wantRemain: "",
		},
		{
			name:        "plain prose no calls",
			content:     "Hello, how can I help you today?",
			wantNoCalls: true,
			wantRemain:  "Hello, how can I help you today?",
		},
		{
			name:        "json without name key ignored",
			content:     `{"status": "ok", "items": []}`,
			wantNoCalls: true,
			wantRemain:  `{"status": "ok", "items": []}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			calls, remaining := tools.ParseFromText(tc.content)
			if tc.wantNoCalls {
				if len(calls) != 0 {
					t.Fatalf("expected no calls, got %d", len(calls))
				}
				if remaining != tc.wantRemain {
					t.Errorf("remaining = %q, want %q", remaining, tc.wantRemain)
				}
				return
			}
			if len(calls) != 1 {
				t.Fatalf("expected 1 call, got %d", len(calls))
			}
			if calls[0].Name != tc.wantName {
				t.Errorf("name = %q, want %q", calls[0].Name, tc.wantName)
			}
			got := calls[0].Arguments.String(tc.wantArg, "")
			if got != tc.wantArgVal {
				t.Errorf("args[%s] = %q, want %q", tc.wantArg, got, tc.wantArgVal)
			}
			if remaining != tc.wantRemain {
				t.Errorf("remaining = %q, want %q", remaining, tc.wantRemain)
			}
		})
	}
}

func TestParseFromText_Gemma4(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		in         string
		wantCalls  int
		wantName   tools.ToolName
		wantArg    string
		wantArgVal string
		wantRemain string
	}{
		{
			name:       "gemma4 native tool call",
			in:         `<|tool_call>call:gesture{gesture:"index",mood:"happy"}<tool_call|>`,
			wantCalls:  1,
			wantName:   "gesture",
			wantArg:    "gesture",
			wantArgVal: "index",
			wantRemain: "",
		},
		{
			name:       "gemma4 with surrounding text",
			in:         `Sure, I'll do that. <|tool_call>call:ha_call_service{entity_id:"light.kitchen",domain:"light",service:"turn_on"}<tool_call|> There.`,
			wantCalls:  1,
			wantName:   "ha_call_service",
			wantArg:    "entity_id",
			wantArgVal: "light.kitchen",
			wantRemain: "Sure, I'll do that.  There.",
		},
		{
			name:       "gemma4 empty args",
			in:         `<|tool_call>call:list_tasks{}<tool_call|>`,
			wantCalls:  1,
			wantName:   "list_tasks",
			wantArg:    "",
			wantArgVal: "",
			wantRemain: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			calls, remaining := tools.ParseFromText(tc.in)
			if len(calls) != tc.wantCalls {
				t.Fatalf("calls len = %d, want %d (got %+v)", len(calls), tc.wantCalls, calls)
			}
			if tc.wantCalls == 0 {
				return
			}
			if calls[0].Name != tc.wantName {
				t.Errorf("name = %q, want %q", calls[0].Name, tc.wantName)
			}
			if tc.wantArg != "" {
				got := calls[0].Arguments.String(tc.wantArg, "")
				if got != tc.wantArgVal {
					t.Errorf("args[%s] = %q, want %q", tc.wantArg, got, tc.wantArgVal)
				}
			}
			if remaining != tc.wantRemain {
				t.Errorf("remaining = %q, want %q", remaining, tc.wantRemain)
			}
		})
	}
}
