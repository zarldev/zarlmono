package templates_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm/templates"
)

func TestSplitThinking(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		in           string
		wantContent  string
		wantThinking string
	}{
		{
			name:         "no tags",
			in:           "Hello world",
			wantContent:  "Hello world",
			wantThinking: "",
		},
		{
			name:         "single think block",
			in:           "<think>let me reason</think>Answer.",
			wantContent:  "Answer.",
			wantThinking: "let me reason",
		},
		{
			name:         "block in the middle",
			in:           "Before <think>mid</think> after.",
			wantContent:  "Before  after.",
			wantThinking: "mid",
		},
		{
			name:         "multiple blocks joined with blank line",
			in:           "<think>a</think>X<think>b</think>Y",
			wantContent:  "XY",
			wantThinking: "a\n\nb",
		},
		{
			name:         "case insensitive + alt tag names",
			in:           "<Thinking>step 1</Thinking> ok <REASONING>step 2</REASONING>",
			wantContent:  "ok",
			wantThinking: "step 1\n\nstep 2",
		},
		{
			name:         "multiline inner",
			in:           "<think>line1\nline2\nline3</think>Done.",
			wantContent:  "Done.",
			wantThinking: "line1\nline2\nline3",
		},
		{
			name:         "empty inner drops",
			in:           "<think>   </think>Answer.",
			wantContent:  "Answer.",
			wantThinking: "",
		},
		{
			name:         "unclosed tag left alone",
			in:           "<think>never closed Answer.",
			wantContent:  "<think>never closed Answer.",
			wantThinking: "",
		},
		{
			name:         "gemma4 channel with reasoning",
			in:           "<|channel>thought\nThe user is asking for the capital of France.\nThe capital of France is Paris.<channel|>The capital of France is Paris.",
			wantContent:  "The capital of France is Paris.",
			wantThinking: "The user is asking for the capital of France.\nThe capital of France is Paris.",
		},
		{
			name:         "gemma4 empty thought block drops",
			in:           "<|channel>thought\n<channel|>The capital of France is Paris.",
			wantContent:  "The capital of France is Paris.",
			wantThinking: "",
		},
		{
			name:         "mixed formats in one response",
			in:           "<think>xml-style</think>Answer before <|channel>thought\ngemma-style<channel|>and after.",
			wantContent:  "Answer before and after.",
			wantThinking: "xml-style\n\ngemma-style",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			content, thinking := templates.SplitThinking(tc.in)
			if content != tc.wantContent {
				t.Errorf("content = %q, want %q", content, tc.wantContent)
			}
			if thinking != tc.wantThinking {
				t.Errorf("thinking = %q, want %q", thinking, tc.wantThinking)
			}
		})
	}
}
