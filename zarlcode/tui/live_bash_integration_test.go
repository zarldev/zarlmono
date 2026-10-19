package tui_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/db"
)

const (
	liveBashPrefix = "LIVE_BEFORE_EXIT"
	liveBashFinal  = "FINAL_AFTER_RELEASE"
)

type liveBashFixture struct {
	ui    *tui.UI
	live  *engine.LiveRunner
	store *db.Store
	ws    code.Workspace
	sink  *teasink.Sink

	mu     sync.Mutex
	events []tea.Msg
	output chan struct{}
}

func newLiveBashFixture(t *testing.T, provider tieredLiveProvider) *liveBashFixture {
	t.Helper()
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	f := &liveBashFixture{store: store, ws: ws, output: make(chan struct{}, 1)}
	f.sink = teasink.New(func(msg tea.Msg) {
		f.mu.Lock()
		f.events = append(f.events, msg)
		f.mu.Unlock()
		if progress, ok := msg.(teasink.ToolOutputMsg); ok && strings.Contains(progress.Text, liveBashPrefix) {
			select {
			case f.output <- struct{}{}:
			default:
			}
		}
	})
	settings := engine.NewSettings(store, nil, nil, ws.Root())
	settings.Registry = nil
	f.live = engine.NewLiveRunner(provider, ws, "scripted-model",
		engine.WithSettings(settings),
		engine.WithLiveSink(f.sink),
		engine.WithToolOutputSink(&engine.ToolOutputSink{Store: store}),
	)
	f.live.SetProviderSpec(provider, engine.ProviderSpec{Name: "openai", Model: "scripted-model", APIKey: "fake-live-bash-api-canary"})
	f.ui = tui.New()
	f.ui.SetLiveRunner(f.live)
	f.ui.SetLiveEventSink(f.sink)
	f.ui.SetSettings(settings)
	f.ui.SetStartupReady(true)
	f.ui.SetSessionIdentity("live-bash-e2e", "Live bash E2E", false, time.Now())
	f.ui.Update(tea.WindowSizeMsg{Width: 160, Height: 80})

	t.Cleanup(func() {
		f.live.CancelTurn()
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), time.Second)
		defer cancel()
		if err := f.live.Close(ctx); err != nil {
			t.Errorf("close live runner: %v", err)
		}
		f.sink.Close()
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return f
}

func (f *liveBashFixture) applyEvents() []tea.Cmd {
	f.sink.Drain()
	f.mu.Lock()
	events := append([]tea.Msg(nil), f.events...)
	f.events = nil
	f.mu.Unlock()
	commands := make([]tea.Cmd, 0, len(events))
	for _, event := range events {
		_, cmd := f.ui.Update(event)
		commands = append(commands, cmd)
	}
	return commands
}

func driveLiveBashCommand(ui *tui.UI, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, child := range batch {
			driveLiveBashCommand(ui, child)
		}
		return
	}
	if msg != nil {
		_, next := ui.Update(msg)
		driveLiveBashCommand(ui, next)
	}
}

func liveBashGate(t *testing.T) (string, *os.File) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "release.fifo")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	gate, err := os.OpenFile(path, os.O_RDWR|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = gate.Close() })
	return path, gate
}

func TestLiveBashProgressFlowsThroughRunnerUIAutosaveAndResume(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	gatePath, gate := liveBashGate(t)
	command := fmt.Sprintf("printf '%s\\n'; read -r signal < %q; printf '%s\\n'", liveBashPrefix, gatePath, liveBashFinal)
	arguments, err := json.Marshal(code.BashArgs{Command: command, TimeoutSeconds: 3})
	if err != nil {
		t.Fatal(err)
	}
	client := runnertest.NewClient([][]llm.CompletionChunk{
		{runnertest.ChunkToolCall("live-bash", "bash", string(arguments))},
		{runnertest.ChunkText("MODEL_FINAL_ONCE")},
	})
	var requestMu sync.Mutex
	var requests []llm.CompletionRequest
	provider := tieredLiveProvider{client: client, check: func(request llm.CompletionRequest) {
		request.Messages = llm.CloneMessages(request.Messages)
		requestMu.Lock()
		requests = append(requests, request)
		requestMu.Unlock()
	}}
	f := newLiveBashFixture(t, provider)

	cmd := f.ui.Submit("run the gated command")
	if cmd == nil {
		t.Fatal("missing live turn command")
	}
	result := make(chan tea.Msg, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		result <- cmd()
	}()
	var release sync.Once
	releaseGate := func() error {
		var releaseErr error
		release.Do(func() { _, releaseErr = gate.WriteString("continue\n") })
		return releaseErr
	}
	t.Cleanup(func() {
		_ = releaseGate()
		f.live.CancelTurn()
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Error("live turn goroutine did not stop")
		}
	})

	select {
	case <-f.output:
	case <-ctx.Done():
		t.Fatalf("live output did not arrive before deadline: %v", context.Cause(ctx))
	}
	select {
	case <-finished:
		t.Fatal("bash call completed before FIFO release")
	default:
	}
	f.applyEvents()
	toolEntry := onlyLiveBashEntry(t, f.ui.CanonicalThread())
	if !strings.Contains(toolEntry.Payload.ToolInput, liveBashPrefix) || !strings.Contains(toolEntry.Payload.ToolInput, liveBashFinal) {
		t.Fatalf("submitted arguments unavailable while call is running: %q", toolEntry.Payload.ToolInput)
	}
	if !strings.Contains(toolEntry.Payload.ToolOutputPreview, liveBashPrefix) || strings.Contains(toolEntry.Payload.ToolOutputPreview, liveBashFinal) {
		t.Fatalf("canonical in-flight preview = %q", toolEntry.Payload.ToolOutputPreview)
	}
	if len(toolEntry.Payload.ToolOutputPreview) > tools.MaxToolOutputPreviewBytes {
		t.Fatalf("canonical preview bytes = %d, max %d", len(toolEntry.Payload.ToolOutputPreview), tools.MaxToolOutputPreviewBytes)
	}
	if strings.Contains(ansi.Strip(f.ui.View().Content), "live output:") {
		t.Fatal("live output automatically opened a collapsed tool's evidence")
	}
	expandFirstTool(f.ui)
	if view := ansi.Strip(f.ui.View().Content); !strings.Contains(view, "live output:") || !strings.Contains(view, liveBashPrefix) {
		t.Fatalf("expanded running bash omitted live output:\n%s", view)
	}

	if err := releaseGate(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
		if msg := <-result; msg != nil {
			t.Fatalf("live command bypassed sink with %T", msg)
		}
	case <-ctx.Done():
		t.Fatalf("live turn did not finish: %v", context.Cause(ctx))
	}
	commands := f.applyEvents()
	if len(commands) == 0 {
		t.Fatal("terminal event stream was empty")
	}
	driveLiveBashCommand(f.ui, commands[len(commands)-1])

	if got := client.CallCount(); got != 2 {
		t.Fatalf("model calls = %d, want tool request and one final result", got)
	}
	requestMu.Lock()
	captured := append([]llm.CompletionRequest(nil), requests...)
	requestMu.Unlock()
	if len(captured) != 2 || len(captured[0].Messages) == 0 {
		t.Fatalf("captured provider requests = %d", len(captured))
	}
	var submitted string
	for _, message := range captured[1].Messages {
		for _, call := range message.ToolCalls {
			if call.ID == "live-bash" {
				submitted = call.Function.Arguments
			}
		}
	}
	if submitted != string(arguments) {
		t.Fatalf("submitted arguments = %q, want %q", submitted, arguments)
	}
	assertOneFinalToolResult(t, f.live.ContextSnapshot())
	toolEntry = onlyLiveBashEntry(t, f.ui.CanonicalThread())
	if toolEntry.Payload.ToolState != transcript.ToolSucceeded || !strings.Contains(toolEntry.Payload.ToolOutputPreview, liveBashPrefix) {
		t.Fatalf("settled canonical tool entry = %#v", toolEntry.Payload)
	}
	f.ui.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	// Completion preserves the group and call the user already opened.
	completedView := ansi.Strip(f.ui.View().Content)
	resultAt := strings.Index(completedView, "result:")
	if resultAt < 0 {
		t.Fatalf("completed bash did not expose its authoritative result:\n%s", completedView)
	}
	resultView := completedView[resultAt:]
	if strings.Count(resultView, liveBashPrefix) != 1 || strings.Count(resultView, liveBashFinal) != 1 ||
		strings.Contains(resultView, "live output:") || strings.Contains(resultView, "output preview (bounded):") {
		t.Fatalf("final result did not replace the live preview exactly once:\n%s", completedView)
	}

	history, err := f.store.ListToolOutputHistory(t.Context(), f.ui.SessionIdentity())
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].ToolName != "bash" || history[0].ArgsJSON != string(arguments) ||
		strings.Count(history[0].Output, liveBashPrefix) != 1 || strings.Count(history[0].Output, liveBashFinal) != 1 {
		t.Fatalf("full tool history = %#v", history)
	}
	stored, err := f.store.GetSessionResumeState(t.Context(), f.ui.SessionIdentity())
	if err != nil {
		t.Fatal(err)
	}
	resume, err := rewind.DecodeResume(stored.Session.ContextJSON)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(resume.Context, f.live.ContextSnapshot()) {
		t.Fatal("autosave did not retain the exact final model context")
	}

	resumed := tui.New()
	resumed.SetLiveRunner(f.live)
	settings := engine.NewSettings(f.store, nil, nil, f.ws.Root())
	settings.Registry = nil
	resumed.SetSettings(settings)
	if err := resumed.ResumeSavedSession(t.Context(), f.ui.SessionIdentity()); err != nil {
		t.Fatal(err)
	}
	resumed.Update(tea.WindowSizeMsg{Width: 160, Height: 80})
	resumedEntry := onlyLiveBashEntry(t, resumed.CanonicalThread())
	if resumedEntry.Payload.ToolOutputPreview != toolEntry.Payload.ToolOutputPreview {
		t.Fatalf("resumed preview = %q, want %q", resumedEntry.Payload.ToolOutputPreview, toolEntry.Payload.ToolOutputPreview)
	}
	resumed.OpenToolHistory(f.store, resumed.SessionIdentity())
	resumed.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	privateHistoryView := ansi.Strip(resumed.View().Content)
	if !strings.Contains(privateHistoryView, "1 occurrences") || !strings.Contains(privateHistoryView, liveBashPrefix) ||
		!strings.Contains(privateHistoryView, liveBashFinal) || !strings.Contains(privateHistoryView, "[exit 0]") {
		t.Fatalf("resumed private history omitted the full final result:\n%s", privateHistoryView)
	}
	if !reflect.DeepEqual(f.live.ContextSnapshot(), resume.Context) {
		t.Fatal("resume changed the full final model context")
	}
}

func onlyLiveBashEntry(t *testing.T, thread transcript.Thread) transcript.Entry {
	t.Helper()
	var matches []transcript.Entry
	for _, entry := range thread.Entries() {
		if entry.Kind == transcript.EntryKinds.ENTRYTOOLCALL && entry.Payload.ToolName == "bash" {
			matches = append(matches, entry)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("bash transcript entries = %d, want 1", len(matches))
	}
	return matches[0]
}

func assertOneFinalToolResult(t *testing.T, messages []llm.Message) {
	t.Helper()
	toolResults, modelFinals := 0, 0
	for _, message := range messages {
		if message.Role == llm.RoleTool {
			toolResults++
			if message.ToolCallID != "live-bash" || strings.Count(message.Content, liveBashPrefix) != 1 ||
				strings.Count(message.Content, liveBashFinal) != 1 || !strings.Contains(message.Content, "[exit 0]") {
				t.Fatalf("final tool context = %#v", message)
			}
		}
		if strings.Contains(message.Content, "MODEL_FINAL_ONCE") {
			modelFinals++
		}
	}
	if toolResults != 1 || modelFinals != 1 {
		t.Fatalf("tool results = %d, final model results = %d", toolResults, modelFinals)
	}
}
