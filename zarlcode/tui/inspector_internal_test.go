package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInspector_OpensWithCtrlI(t *testing.T) {
	m := New()
	stepUI(t, m, tea.WindowSizeMsg{Width: 120, Height: 32})
	stepUI(t, m, tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'o'})

	out := ansi.Strip(m.View().Content)
	for _, want := range []string{"inspector", "tools", "prompt", "guardrails", "processes", "mcp", "events", "skills", "agents"} {
		if !strings.Contains(out, want) {
			t.Fatalf("inspector missing %q:\n%s", want, out)
		}
	}
}

func TestContextView_TabBarAndPromptSummary(t *testing.T) {
	m := New()
	stepUI(t, m, tea.WindowSizeMsg{Width: 120, Height: 32})
	m.session.SetCockpitExpanded(true)
	m.session.Run.foldTurnComplete(&llm.Usage{PromptTokens: 600, CompletionTokens: 80, TotalTokens: 680, CachedTokens: 200}, time.Second, 1)
	m.contextView.setTab(contextViewTabPrompt)

	out := ansi.Strip(m.View().Content)
	for _, want := range []string{"context view", "overview", "context", "prompt", "tools", "events", "preview"} {
		if !strings.Contains(out, want) {
			t.Fatalf("context view missing %q:\n%s", want, out)
		}
	}
}

func TestInspector_ShowsBackgroundProcesses(t *testing.T) {
	root := t.TempDir()
	ws, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	pm := code.NewProcessManager(ws)
	defer pm.Close(t.Context())
	id, err := pm.StartProcess("sleep 5")
	if err != nil {
		t.Fatalf("StartProcess: %v", err)
	}
	live := engine.NewLiveRunner(nil, ws, nil, "inspect-model")
	live.SetProcessManager(pm)
	snap := BuildInspectorSnapshot(NewSession("~", root, ""), live, nil)
	if len(snap.Processes) != 1 {
		t.Fatalf("process snapshot len = %d, want 1", len(snap.Processes))
	}
	if snap.Processes[0].ID != id || !snap.Processes[0].Running {
		t.Fatalf("process snapshot = %+v, want running id %q", snap.Processes[0], id)
	}

	ins := newInspector(snap)
	ins.cursor = int(inspectorTabProcesses)
	out := ansi.Strip(strings.Join(ins.contentLines(120), "\n"))
	for _, want := range []string{"processes", "1 running", id.String(), "sleep 5", "stdout=0 lines"} {
		if !strings.Contains(out, want) {
			t.Fatalf("process inspector missing %q:\n%s", want, out)
		}
	}
}

func TestInspector_KillSelectedProcessAction(t *testing.T) {
	root := t.TempDir()
	ws, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	pm := code.NewProcessManager(ws)
	defer pm.Close(t.Context())
	id, err := pm.StartProcess("sleep 5")
	if err != nil {
		t.Fatalf("StartProcess: %v", err)
	}
	live := engine.NewLiveRunner(nil, ws, nil, "inspect-model")
	live.SetProcessManager(pm)
	ins := newInspector(BuildInspectorSnapshot(NewSession("~", root, ""), live, nil))
	ins.cursor = int(inspectorTabProcesses)

	act := ins.handleKey(tea.KeyPressMsg{Text: "x", Code: 'x'})
	kill, ok := act.(actionKillProcess)
	if !ok {
		t.Fatalf("x returned %T, want actionKillProcess", act)
	}
	if kill.processID != id.String() || kill.signal != "TERM" {
		t.Fatalf("kill action = %+v, want %q TERM", kill, id)
	}
}

func TestUI_KillProcessFeedsAgentQueue(t *testing.T) {
	root := t.TempDir()
	ws, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	pm := code.NewProcessManager(ws)
	defer pm.Close(t.Context())
	id, err := pm.StartProcess("sleep 30")
	if err != nil {
		t.Fatalf("StartProcess: %v", err)
	}
	live := engine.NewLiveRunner(nil, ws, nil, "inspect-model")
	live.SetProcessManager(pm)
	m := New()
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	m = mm.(*UI)
	m.SetLiveRunner(live)
	m.session.Run.Running = true
	m.overlay.push(newInspector(BuildInspectorSnapshot(m.session, live, nil)))

	cmd := m.handleAction(actionKillProcess{processID: id.String(), signal: "KILL"})
	if cmd == nil {
		t.Fatal("kill action returned nil cmd")
	}
	msg, ok := cmd().(processKillResultMsg)
	if !ok {
		t.Fatalf("kill cmd returned %T, want processKillResultMsg", msg)
	}
	if msg.Error != "" {
		t.Fatalf("kill msg error: %s", msg.Error)
	}
	mm, _ = m.Update(msg)
	m = mm.(*UI)

	queued := m.live.QueueSnapshot()
	if len(queued) != 1 {
		t.Fatalf("queued messages = %d, want 1", len(queued))
	}
	if got := queued[0].Message.Content; !strings.Contains(got, id.String()) || !strings.Contains(got, "background process") || !strings.Contains(got, "sleep 30") {
		t.Fatalf("queued message missing process kill detail: %q", got)
	}
	info, err := pm.Info(id)
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.Running {
		t.Fatalf("process still running after kill: %+v", info)
	}
	last, ok := m.timeline.items[len(m.timeline.items)-1].(*noticeItem)
	if !ok || !strings.Contains(ansi.Strip(last.text), "process killed") || !strings.Contains(ansi.Strip(last.text), "queued to agent") {
		t.Fatalf("timeline last item = %#v, want kill notice queued to agent", m.timeline.items[len(m.timeline.items)-1])
	}
}

func TestInspector_ShowsToolsTabByDefault(t *testing.T) {
	m := New()
	stepUI(t, m, tea.WindowSizeMsg{Width: 120, Height: 32})
	stepUI(t, m, tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'o'})

	out := ansi.Strip(m.View().Content)
	for _, want := range []string{"tools", "no tools registered"} {
		if !strings.Contains(out, want) {
			t.Fatalf("tools tab missing %q:\n%s", want, out)
		}
	}
	// BUILD mode label only appears when tools roster is populated.
}

func TestInspector_TogglesBetweenTabs(t *testing.T) {
	m := New()
	stepUI(t, m, tea.WindowSizeMsg{Width: 120, Height: 32})
	stepUI(t, m, tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'o'})
	stepUI(t, m, tea.KeyPressMsg{Code: tea.KeyTab})

	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "prompt") || !strings.Contains(out, "system prompt") {
		t.Fatalf("prompt tab not rendered after tab:\n%s", out)
	}
}

func TestInspectorPromptRendersLivePromptSurface(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "AGENTS.md"), "Inspector must show workspace instructions.")
	mustWrite(t, filepath.Join(root, ".zarlcode", "skills", "go.md"), `---
name: go
description: Go workflow
---

Run go test.
`)
	mustWrite(t, filepath.Join(root, ".zarlcode", "agents", "reviewer.md"), `---
name: reviewer
description: Review code
model: review-model
---

You review code.
`)
	ws, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	live := engine.NewLiveRunner(nil, ws, nil, "inspect-model")
	live.SetLimits(0, 0, 0, 1) // enable spawn_agent, so agent roster is inlined
	snap := BuildInspectorSnapshot(NewSession("~", root, ""), live, nil)

	// The prompt itself no longer enumerates a tool roster or skill/agent lists
	// (kept thin + byte-stable); it carries the static surface plus workspace
	// instructions.
	for _, want := range []string{
		"rendered next-turn BUILD mode prompt",
		"Inspector must show workspace instructions.",
	} {
		if !strings.Contains(snap.PromptSystem, want) {
			t.Fatalf("inspector prompt missing %q:\n%s", want, snap.PromptSystem)
		}
	}
	// Tools (incl. the skill/agent discovery tools) are surfaced via the
	// inspector's tool list and the model's tool interface, not the prompt text.
	toolset := map[string]bool{}
	for _, spec := range snap.Tools {
		toolset[spec.Name.String()] = true
	}
	for _, want := range []string{"list_skills", "list_agents", "spawn_agent", "web_fetch", "update_plan"} {
		if !toolset[want] {
			t.Fatalf("inspector tool list missing %q: %v", want, snap.Tools)
		}
	}
	if len(snap.Errors) != 0 {
		t.Fatalf("unexpected inspector errors: %#v", snap.Errors)
	}
}

func TestInspectorToolsUsePlanFilteredLiveSurface(t *testing.T) {
	root := t.TempDir()
	ws, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	live := engine.NewLiveRunner(nil, ws, nil, "inspect-model")
	live.SetLimits(0, 0, 0, 1)
	live.SetPlanMode(true)
	snap := BuildInspectorSnapshot(NewSession("~", root, ""), live, nil)

	seen := map[string]bool{}
	for _, spec := range snap.Tools {
		seen[string(spec.Name)] = true
	}
	for _, want := range []string{"read", "grep", "glob", "ls", "web_fetch", "update_plan"} {
		if !seen[want] {
			t.Fatalf("plan inspector tools missing %q; saw %#v", want, seen)
		}
	}
	for _, blocked := range []string{"write", "edit", "bash"} {
		if seen[blocked] {
			t.Fatalf("plan inspector tools should hide %q; saw %#v", blocked, seen)
		}
	}
}

func TestInspector_ShowsEventsAfterRunnerMessages(t *testing.T) {
	m := New()
	stepUI(t, m, tea.WindowSizeMsg{Width: 120, Height: 32})
	stepUI(t, m, teasink.ConversationStartedMsg{TaskID: "insp-run"})
	stepUI(t, m, teasink.ToolStartedMsg{ToolName: "read"})
	stepUI(t, m, teasink.ToolCompletedMsg{ToolName: "read", Duration: time.Millisecond})
	stepUI(t, m, teasink.ConversationEndedMsg{TaskID: "insp-run", Iterations: 1})
	stepUI(t, m, tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'o'})
	// Tab to events tab (tab 5).
	for range 5 {
		stepUI(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	}

	out := ansi.Strip(m.View().Content)
	for _, want := range []string{"events", "run started", "tool started", "tool completed", "run ended"} {
		if !strings.Contains(out, want) {
			t.Fatalf("events tab missing %q:\n%s", want, out)
		}
	}
}

func TestEventRing_AddAndSnapshot(t *testing.T) {
	r := NewEventRing(3)
	r.Add(EventRingEntry{Kind: "a", Detail: "first", At: time.Now()})
	r.Add(EventRingEntry{Kind: "b", Detail: "second", At: time.Now()})
	r.Add(EventRingEntry{Kind: "c", Detail: "third", At: time.Now()})
	r.Add(EventRingEntry{Kind: "d", Detail: "fourth", At: time.Now()})

	s := r.Snapshot()
	if len(s) != 3 {
		t.Fatalf("ring snapshot len = %d, want 3", len(s))
	}
	if s[0].Kind != "b" || s[1].Kind != "c" || s[2].Kind != "d" {
		t.Fatalf("ring order = %v, want [b, c, d]", s)
	}
}
