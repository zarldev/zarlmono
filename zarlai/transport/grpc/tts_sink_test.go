package grpc

import (
	"testing"
)

func TestStripToolCallMarkup(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		final    bool
		wantSafe string
		wantHeld string
	}{
		{
			name:     "no markup",
			input:    "Hello, how are you?",
			wantSafe: "Hello, how are you?",
			wantHeld: "",
		},
		{
			name:     "single closed block in middle",
			input:    "Let me check. <tool_call>{\"name\":\"x\"}</tool_call> Here you go.",
			wantSafe: "Let me check.  Here you go.",
			wantHeld: "",
		},
		{
			name:     "unclosed block, non-final — held",
			input:    "Let me check. <tool_call>{\"name\":\"x\"",
			wantSafe: "Let me check. ",
			wantHeld: "<tool_call>{\"name\":\"x\"",
		},
		{
			name:     "unclosed block, final — discarded",
			input:    "Let me check. <tool_call>{\"name\":\"x\"",
			final:    true,
			wantSafe: "Let me check. ",
			wantHeld: "",
		},
		{
			name:     "two closed blocks",
			input:    "A. <tool_call>{}</tool_call> B. <tool_call>{}</tool_call> C.",
			wantSafe: "A.  B.  C.",
			wantHeld: "",
		},
		{
			name:     "closed then unclosed",
			input:    "A. <tool_call>{}</tool_call> <tool_call>{partial",
			wantSafe: "A.  ",
			wantHeld: "<tool_call>{partial",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			safe, held := stripToolCallMarkup(tt.input, tt.final)
			if safe != tt.wantSafe {
				t.Errorf("safe = %q, want %q", safe, tt.wantSafe)
			}
			if held != tt.wantHeld {
				t.Errorf("held = %q, want %q", held, tt.wantHeld)
			}
		})
	}
}
