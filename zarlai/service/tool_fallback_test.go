package service_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zarlai/service"
)

func TestParseToolCallsFromText(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		wantName    string
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls, remaining := service.ParseToolCallsFromText(tt.content)
			if tt.wantNoCalls {
				if len(calls) != 0 {
					t.Fatalf("expected no calls, got %d", len(calls))
				}
				if remaining != tt.wantRemain {
					t.Errorf("remaining = %q, want %q", remaining, tt.wantRemain)
				}
				return
			}
			if len(calls) != 1 {
				t.Fatalf("expected 1 call, got %d", len(calls))
			}
			if calls[0].Function.Name != tt.wantName {
				t.Errorf("name = %q, want %q", calls[0].Function.Name, tt.wantName)
			}
			got := service.Get[string](calls[0].Function.Arguments, tt.wantArg)
			if got != tt.wantArgVal {
				t.Errorf("args[%s] = %q, want %q", tt.wantArg, got, tt.wantArgVal)
			}
			if remaining != tt.wantRemain {
				t.Errorf("remaining = %q, want %q", remaining, tt.wantRemain)
			}
		})
	}
}

func TestParseGemma4ToolCalls(t *testing.T) {
	cases := []struct {
		name       string
		in         string
		wantCalls  int
		wantName   string
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
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			calls, remaining := service.ParseToolCallsFromText(tt.in)
			if len(calls) != tt.wantCalls {
				t.Fatalf("calls len = %d, want %d (got %+v)", len(calls), tt.wantCalls, calls)
			}
			if tt.wantCalls == 0 {
				return
			}
			if calls[0].Function.Name != tt.wantName {
				t.Errorf("name = %q, want %q", calls[0].Function.Name, tt.wantName)
			}
			if tt.wantArg != "" {
				got, _ := calls[0].Function.Arguments[tt.wantArg].(string)
				if got != tt.wantArgVal {
					t.Errorf("args[%s] = %q, want %q", tt.wantArg, got, tt.wantArgVal)
				}
			}
			if remaining != tt.wantRemain {
				t.Errorf("remaining = %q, want %q", remaining, tt.wantRemain)
			}
		})
	}
}
