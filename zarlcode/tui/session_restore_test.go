package tui_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestRestoreMessagesConnectsToolCallsToAssistant(t *testing.T) {
	m := tui.New()
	m.RestoreMessages([]llm.Message{
		{Role: "user", Content: "inspect foo"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "call_1", Function: llm.ToolCallFunction{Name: "read", Arguments: `{"path":"foo.go"}`}}}},
		{Role: "tool", ToolCallID: "call_1", Content: "package main\nfunc main() {}"},
		{Role: "assistant", Content: "done"},
	})
	out := strings.Join(m.RenderTranscript(100, 30), "\n")
	for _, want := range []string{"inspect foo", "tools (1)", "done"} {
		if !strings.Contains(out, want) {
			t.Fatalf("restored transcript missing %q:\n%s", want, out)
		}
	}
}

func TestDecodeSessionHistoryRejectsCorruptionAndToleratesAuxiliaryCorruption(t *testing.T) {
	if _, err := tui.DecodeSessionHistory(db.SessionRecord{HistoryJSON: []byte(`{`)}); err == nil {
		t.Fatal("corrupt history accepted")
	}
	history, err := tui.DecodeSessionHistory(db.SessionRecord{HistoryJSON: []byte(`[{"role":"user","content":"hi"}]`), PlanJSON: []byte(`{ broken`), LastUsageJSON: []byte(`broken`)})
	if err != nil || len(history) != 1 || history[0].Content != "hi" {
		t.Fatalf("history=%+v err=%v", history, err)
	}
}
