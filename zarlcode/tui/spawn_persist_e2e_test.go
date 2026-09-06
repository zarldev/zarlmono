package tui_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestSpawnedAgentTranscriptPersistsResumesAndExports(t *testing.T) {
	workspaceRoot := t.TempDir()
	workspace, err := code.NewWorkspace(workspaceRoot)
	if err != nil {
		t.Fatalf("new workspace: %v", err)
	}

	var messagesMu sync.Mutex
	var messages []tea.Msg
	sink := teasink.New(func(msg tea.Msg) {
		messagesMu.Lock()
		messages = append(messages, msg)
		messagesMu.Unlock()
	})
	t.Cleanup(sink.Close)

	childClient := runnertest.NewClient([][]llm.CompletionChunk{
		{runnertest.ChunkText("child found the durable edge")},
	})
	child := runner.New(
		childClient,
		runner.WithSink(sink),
		runner.WithModelIdentity("scripted", "child-model"),
		runner.WithMaxIterations(2),
	)

	parentClient := runnertest.NewClient([][]llm.CompletionChunk{
		{runnertest.ChunkToolCall("spawn-review", "agent_spawn", `{"agent":"reviewer","prompt":"inspect child contract"}`)},
		{runnertest.ChunkText("parent accepted the durable edge")},
	})
	registry := tools.NewRegistry()
	parent := runner.New(
		parentClient,
		runner.WithTools(registry),
		runner.WithSink(sink),
		runner.WithModelIdentity("scripted", "parent-model"),
		runner.WithMaxIterations(3),
	)
	spawnTool := spawn.New(parent, spawn.WithAgentResolver(func(name string) (*runner.Runner, error) {
		if name == "reviewer" {
			return child, nil
		}
		return nil, errors.New("unexpected agent name")
	}))
	if err := registry.Register(spawnTool); err != nil {
		t.Fatalf("register agent_spawn: %v", err)
	}

	result := parent.Run(t.Context(), runner.TaskSpec{
		ID:     taskscope.ID("root-turn"),
		Prompt: "delegate the durable check",
	})
	if result.Err != nil || result.Reason != runner.TerminalCompleted || result.FinalContent != "parent accepted the durable edge" {
		t.Fatalf("parent result = %#v", result)
	}
	if got := parentClient.CallCount(); got != 2 {
		t.Fatalf("parent model calls = %d, want 2", got)
	}
	if got := childClient.CallCount(); got != 1 {
		t.Fatalf("child model calls = %d, want 1", got)
	}

	sink.Drain()
	messagesMu.Lock()
	projected := append([]tea.Msg(nil), messages...)
	messagesMu.Unlock()

	ui := tui.New()
	stepE2E(t, ui, tea.WindowSizeMsg{Width: 140, Height: 40})
	for _, msg := range projected {
		// The sink callback only records. Applying all messages here keeps the
		// Bubble Tea model single-threaded, as it is in the real program.
		stepE2E(t, ui, msg)
	}

	visible := ansi.Strip(ui.View().Content)
	if count := strings.Count(visible, "reviewer · scripted/child-model"); count != 1 {
		t.Fatalf("visible sub-agent subsection count = %d, want 1:\n%s", count, visible)
	}
	if !strings.Contains(visible, "agents (1)") || !strings.Contains(visible, "parent accepted the durable edge") {
		t.Fatalf("live transcript missing agent group or parent result:\n%s", visible)
	}
	assertSpawnTranscript(t, ui.CanonicalThread())

	databasePath := filepath.Join(t.TempDir(), "sessions.db")
	store, err := db.Open(t.Context(), databasePath)
	if err != nil {
		t.Fatalf("open session store: %v", err)
	}
	t.Cleanup(func() {
		if store != nil {
			_ = store.Close()
		}
	})

	live := engine.NewLiveRunner(nil, workspace, "parent-model")
	live.RestoreContext(result.Messages)
	ui.SetLiveRunner(live)
	ui.SetSettings(engine.NewSettings(store, nil, nil, workspaceRoot))
	ui.SetSessionIdentity("spawn-e2e", "Spawn E2E", false, time.Now())
	registerLiveRunnerClose(t, live)
	runUICommand(t, ui, ui.ForceTranscriptPersist())
	if err := ui.SaveSession(t.Context()); err != nil {
		t.Fatalf("save session context: %v", err)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("close first session store: %v", err)
	}
	store = nil

	reopened, err := db.Open(t.Context(), databasePath)
	if err != nil {
		t.Fatalf("reopen session store: %v", err)
	}
	store = reopened

	resumedClient := runnertest.NewClient([][]llm.CompletionChunk{
		{runnertest.ChunkText("continued from the durable edge")},
	})
	resumedLive := engine.NewLiveRunner(scriptedProvider{Client: resumedClient}, workspace, "parent-model")
	resumed := tui.New()
	resumed.SetLiveRunner(resumedLive)
	resumed.SetSettings(engine.NewSettings(reopened, nil, nil, workspaceRoot))
	registerLiveRunnerClose(t, resumedLive)
	if err := resumed.ResumeSavedSession(t.Context(), "spawn-e2e"); err != nil {
		t.Fatalf("resume saved session: %v", err)
	}
	stepE2E(t, resumed, tea.WindowSizeMsg{Width: 140, Height: 40})
	assertSpawnTranscript(t, resumed.CanonicalThread())
	if got := resumedLive.ContextSnapshot(); !reflect.DeepEqual(got, result.Messages) {
		t.Fatalf("restored model context = %#v, want %#v", got, result.Messages)
	}
	if err := resumedLive.RunTurn(t.Context(), "continue from the durable edge"); err != nil {
		t.Fatalf("run resumed turn: %v", err)
	}
	if got := resumedClient.CallCount(); got != 1 {
		t.Fatalf("resumed model calls = %d, want 1", got)
	}

	resumedView := ansi.Strip(resumed.View().Content)
	for _, want := range []string{"agents (1)", "parent accepted the durable edge"} {
		if !strings.Contains(resumedView, want) {
			t.Fatalf("resumed UI missing %q:\n%s", want, resumedView)
		}
	}

	exportPath := filepath.Join(t.TempDir(), "spawn-e2e.md")
	stepE2E(t, resumed, tea.PasteMsg{Content: "/export " + exportPath})
	_, exportCmd := resumed.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if exportCmd == nil {
		t.Fatal("export returned no command")
	}
	runUIBatch(t, resumed, exportCmd)

	exported, err := os.ReadFile(exportPath)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	exportText := string(exported)
	for _, want := range []string{
		"## Conversation",
		"delegate the durable check",
		"reviewer",
		"inspect child contract",
		"child found the durable edge",
		"parent accepted the durable edge",
	} {
		if !strings.Contains(exportText, want) {
			t.Fatalf("export missing %q:\n%s", want, exportText)
		}
	}
}

func registerLiveRunnerClose(t *testing.T, live *engine.LiveRunner) {
	t.Helper()
	testCtx := t.Context()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(testCtx), time.Second)
		defer cancel()
		if err := live.Close(ctx); err != nil {
			t.Errorf("close live runner: %v", err)
		}
	})
}

type scriptedProvider struct {
	*runnertest.Client
}

func (scriptedProvider) Name() string { return "scripted" }

func assertSpawnTranscript(t testing.TB, thread transcript.Thread) {
	t.Helper()

	var subagentID string
	var subagents int
	for _, entry := range thread.Entries() {
		if entry.Payload.Subagent == "" {
			continue
		}
		subagents++
		subagentID = entry.ID
		if entry.Payload.Subagent != transcript.SubagentCompleted || entry.Payload.AgentName != "reviewer" || !strings.Contains(entry.Payload.Prompt, "inspect child contract") {
			t.Fatalf("sub-agent entry = %#v", entry)
		}
	}
	if subagents != 1 {
		t.Fatalf("sub-agent entries = %d, want 1", subagents)
	}

	var childResult, parentResult bool
	for _, entry := range thread.Entries() {
		switch {
		case entry.ParentID == subagentID && entry.Payload.Text == "child found the durable edge":
			childResult = true
		case entry.ParentID == "" && entry.Payload.Text == "parent accepted the durable edge":
			parentResult = true
		}
	}
	if !childResult || !parentResult {
		t.Fatalf("transcript result ownership: child=%v parent=%v; entries=%#v", childResult, parentResult, thread.Entries())
	}
}

func stepE2E(t testing.TB, ui *tui.UI, msg tea.Msg) {
	t.Helper()
	updated, _ := ui.Update(msg)
	if updated != ui {
		t.Fatalf("Update returned %T, want original *tui.UI", updated)
	}
}

func runUIBatch(t testing.TB, ui *tui.UI, cmd tea.Cmd) {
	t.Helper()
	var run func(tea.Cmd)
	run = func(cmd tea.Cmd) {
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, batched := range batch {
				run(batched)
			}
			return
		}
		stepE2E(t, ui, msg)
	}
	run(cmd)
}
func runUICommand(t testing.TB, ui *tui.UI, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected UI command")
	}
	for cmd != nil {
		msg := cmd()
		updated, next := ui.Update(msg)
		if updated != ui {
			t.Fatalf("Update returned %T, want original *tui.UI", updated)
		}
		cmd = next
	}
}
