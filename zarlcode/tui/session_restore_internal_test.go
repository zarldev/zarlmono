package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestTimelineRestoreMessagesConnectsToolCallsToAssistant(t *testing.T) {
	tl := newTimeline()
	tl.restoreMessages([]llm.Message{
		{Role: "user", Content: "inspect foo"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{
			ID: "call_1",
			Function: llm.ToolCallFunction{
				Name:      "read",
				Arguments: `{"path":"foo.go"}`,
			},
		}}},
		{Role: "tool", ToolCallID: "call_1", Content: "package main\nfunc main() {}"},
		{Role: "assistant", Content: "done"},
	})

	if len(tl.items) != 4 {
		t.Fatalf("restored item count = %d, want 4 (%T)", len(tl.items), tl.items)
	}
	if _, ok := tl.items[1].(*assistantItem); !ok {
		t.Fatalf("tool-call assistant should restore as assistant item, got %T", tl.items[1])
	}
	g, ok := tl.items[2].(*groupItem)
	if !ok {
		t.Fatalf("tool call/result should restore as a nested tool group, got %T", tl.items[2])
	}
	if !g.nested || !g.closed || g.kind != groupTools {
		t.Fatalf("group = nested:%v closed:%v kind:%v, want nested closed tools", g.nested, g.closed, g.kind)
	}
	if len(g.children) != 1 {
		t.Fatalf("group children = %d, want 1", len(g.children))
	}
	tool, ok := g.children[0].(*toolItem)
	if !ok {
		t.Fatalf("tool child type = %T, want *toolItem", g.children[0])
	}
	if tool.name != "read" || tool.arg != "foo.go" {
		t.Errorf("tool header = name %q arg %q, want read foo.go", tool.name, tool.arg)
	}
	if tool.state != toolOK {
		t.Errorf("tool state = %v, want ok", tool.state)
	}
	if !strings.Contains(tool.result, "package main") {
		t.Errorf("tool result not attached: %q", tool.result)
	}
	if _, ok := tl.toolIdx["call_1"]; !ok {
		t.Error("restored tool index missing call_1")
	}
}

func TestResumeIntroSession_TargetMismatchPrompts(t *testing.T) {
	m := New()
	m.settings = newTestSettings(t)
	m.SetProviderContext(engine.ProviderSpec{Name: "openai", Model: "gpt-4o-mini"}, engine.ProviderSpec{Name: "openai", Model: "gpt-4o-mini"})
	m.SetProvider("openai")
	m.SetWorkspace("/tmp/ws", "gpt-4o-mini")
	m.intro = newIntroPane("/tmp/ws", nil, "openai", "gpt-4o-mini")

	rec := db.SessionRecord{
		ID:          "s1",
		Workspace:   m.settings.WorkspaceRoot(),
		Provider:    "anthropic",
		Model:       "claude-sonnet-4-5",
		HistoryJSON: []byte(`[{"role":"user","content":"hi"}]`),
		CreatedAt:   time.Now(),
	}
	if err := m.settings.Store.SaveSession(t.Context(), rec); err != nil {
		t.Fatalf("save session: %v", err)
	}

	if cmd := m.resumeIntroSession("s1"); cmd != nil {
		t.Fatalf("resume returned cmd before target decision")
	}
	if m.intro == nil {
		t.Fatal("intro dismissed before target decision")
	}
	if _, ok := m.overlay.top().(*resumeTargetDialog); !ok {
		t.Fatalf("overlay = %T, want *resumeTargetDialog", m.overlay.top())
	}
}

func TestResumeTargetDialog_CurrentTargetKeepsCurrentProvider(t *testing.T) {
	m := New()
	m.settings = newTestSettings(t)
	m.SetProviderContext(engine.ProviderSpec{Name: "openai", Model: "gpt-4o-mini"}, engine.ProviderSpec{Name: "openai", Model: "gpt-4o-mini"})
	m.SetProvider("openai")
	m.SetWorkspace("/tmp/ws", "gpt-4o-mini")
	m.intro = newIntroPane("/tmp/ws", nil, "openai", "gpt-4o-mini")
	saved := &savedSession{sessionSummary: sessionSummary{ID: "s1", Label: "saved", Provider: "anthropic", Model: "claude-sonnet-4-5", CreatedAt: time.Now()}, History: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}}
	m.overlay.push(newResumeTargetDialog(saved, "openai", "gpt-4o-mini"))

	cmd := m.handleAction(actionResumeSession{session: saved, useSaved: false})
	if cmd == nil {
		t.Fatal("resume should return toast command")
	}
	if m.intro != nil {
		t.Fatal("intro not dismissed")
	}
	if got := m.session.ActiveProviderSpec(); got.Name != "openai" || got.Model != "gpt-4o-mini" {
		t.Fatalf("active provider changed: %+v", got)
	}
}
