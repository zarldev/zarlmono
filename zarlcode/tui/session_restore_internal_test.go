package tui

import (
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

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

func TestResumeTargetDialogUsesSharedActionRegions(t *testing.T) {
	saved := &savedSession{sessionSummary: sessionSummary{Provider: "anthropic", Model: "claude-sonnet-4-5"}}
	d := newResumeTargetDialog(saved, "openai", "gpt-4o-mini")
	buf := uv.NewScreenBuffer(100, 20)
	d.draw(buf, buf.Bounds())
	out := ansi.Strip(buf.Render())

	for _, want := range []string{
		"[resume]", "choose model target", "anthropic / claude-sonnet-4-5", "openai / gpt-4o-mini",
		"s / enter use saved", "c use current", "any other cancel",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("resume target dialog missing %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "[resume]") != 1 {
		t.Fatalf("resume target should render one framed title:\n%s", out)
	}
}

func TestResumeSession_CorruptAuxiliaryBlobsWarnsOnce(t *testing.T) {
	m := New()
	m.settings = newTestSettings(t)
	m.intro = newIntroPane("/tmp/ws", nil, "", "")
	saved := &savedSession{
		sessionSummary: sessionSummary{ID: "s1", Label: "saved", CreatedAt: time.Now()},
		History:        []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
		restoreDiagnostics: []sessionRestoreDiagnostic{
			sessionRestorePlanCorrupt,
			sessionRestoreDiffBodiesCorrupt,
			sessionRestoreUsageCorrupt,
		},
		ToolTraceRaw: []byte(`{`),
	}

	if cmd := m.completeResumeSession(saved, false); cmd == nil {
		t.Fatal("resume should return toast command")
	}
	if got := m.session.ToastTone; got != toastWarn {
		t.Fatalf("toast tone = %v, want warning", got)
	}
	if !strings.Contains(m.session.Toast, "some saved details were unavailable") {
		t.Fatalf("toast = %q, want incomplete-details notice", m.session.Toast)
	}
	if len(saved.restoreDiagnostics) != 0 {
		t.Fatalf("diagnostics were not consumed: %v", saved.restoreDiagnostics)
	}

	m.completeResumeSession(saved, false)
	if got := m.session.ToastTone; got != toastSuccess {
		t.Fatalf("second resume toast tone = %v, want success", got)
	}
	if strings.Contains(m.session.Toast, "some saved details were unavailable") {
		t.Fatalf("second resume repeated incomplete-details notice: %q", m.session.Toast)
	}
	if len(saved.restoreDiagnostics) != 0 {
		t.Fatalf("second resume left diagnostics: %v", saved.restoreDiagnostics)
	}
}

func TestDecodeSavedSession_CorruptHistoryFails(t *testing.T) {
	_, err := decodeSavedSession(db.SessionRecord{HistoryJSON: []byte(`{`)})
	if err == nil {
		t.Fatal("decode corrupt history: got nil error")
	}
}
