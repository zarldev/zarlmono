package tui_test

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestTieredCompactionPreservesTimelineThroughResume(t *testing.T) {
	first := strings.Repeat("first narrative ", 40) + "FIRSTORIGINALMIDDLE" + strings.Repeat(" first tail", 40) + "\n\n"
	second := strings.Repeat("second narrative ", 40) + "SECONDORIGINALMIDDLE" + strings.Repeat(" second tail", 40) + "\n\n"
	const answer = "Original final answer."
	const followup = "Original next turn."
	for _, tc := range []struct {
		name   string
		target int
		marker string
	}{
		{name: "tool truncation", target: 10000, marker: "chars elided post-compact"},
		{name: "assistant host excerpt", target: 3000, marker: "Host-generated historical excerpts"},
		{name: "tool elision", target: 100, marker: "[tool result elided post-compact"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newBeforeFixture(t)
			f.ui.SetSessionIdentity("tiered-timeline", "Tiered timeline", false, time.Now())
			f.ui.Update(tea.WindowSizeMsg{Width: 160, Height: 120})
			registry := tools.NewRegistry()
			registry.Register(runnertest.Tool{Name: "echo", Result: strings.Repeat("original tool result ", 75)})
			client := runnertest.NewClient([][]llm.CompletionChunk{
				{runnertest.ChunkText(first), runnertest.ChunkToolCall("one", "echo", "{}")},
				{runnertest.ChunkText(second), runnertest.ChunkToolCall("two", "echo", "{}")},
				{runnertest.ChunkText(answer)},
				{runnertest.ChunkText(followup)},
			})
			r := runner.New(client, runner.WithSink(f.sink), runner.WithTools(registry),
				runner.WithCompactor(&compact.Tiered{TargetBytes: tc.target, Phase1Threshold: .01}),
				runner.WithCompactKeepRecent(1), runner.WithMaxIterations(3))
			result := r.Run(t.Context(), runner.TaskSpec{ID: "first-turn", Prompt: "Original question"})
			if result.Err != nil || result.Reason != runner.TerminalCompleted {
				t.Fatalf("run: %s, %v", result.Reason, result.Err)
			}
			var contextText strings.Builder
			for _, message := range result.Messages {
				contextText.WriteString(message.Content)
			}
			if !strings.Contains(contextText.String(), tc.marker) {
				t.Fatalf("fixture did not produce %q in model context", tc.marker)
			}
			f.live.RestoreContext(result.Messages)
			f.applyEvents()
			assertTieredTimeline(t, f.ui, first+second+answer)

			result = r.Run(t.Context(), runner.TaskSpec{ID: "next-turn", Prompt: "Next question", Context: result.Messages})
			if result.Err != nil || result.Reason != runner.TerminalCompleted {
				t.Fatalf("next turn: %s, %v", result.Reason, result.Err)
			}
			f.live.RestoreContext(result.Messages)
			f.applyEvents()
			assertTieredTimeline(t, f.ui, first+second+answer+followup)
			if err := f.ui.SaveSession(t.Context()); err != nil {
				t.Fatal(err)
			}

			resumed := tui.New()
			resumed.SetLiveRunner(f.live)
			resumed.SetSettings(engine.NewSettings(f.store, nil, nil, f.ws.Root()))
			if err := resumed.ResumeSavedSession(t.Context(), "tiered-timeline"); err != nil {
				t.Fatal(err)
			}
			resumed.Update(tea.WindowSizeMsg{Width: 160, Height: 120})
			assertTieredTimeline(t, resumed, first+second+answer+followup)
		})
	}
}

func assertTieredTimeline(t *testing.T, ui *tui.UI, want string) {
	t.Helper()
	var assistant strings.Builder
	for _, entry := range ui.CanonicalThread().Entries() {
		if entry.Kind == transcript.EntryKinds.ENTRYASSISTANTMESSAGE {
			assistant.WriteString(entry.Payload.Text)
		}
	}
	if assistant.String() != want {
		t.Fatal("canonical assistant text changed after tiered compaction")
	}
	view := ansi.Strip(ui.View().Content)
	for _, original := range []string{"FIRSTORIGINALMIDDLE", "SECONDORIGINALMIDDLE", "Original final answer."} {
		if !strings.Contains(view, original) {
			t.Errorf("timeline is missing %q", original)
		}
	}
	for _, marker := range []string{"[reasoning trimmed", "chars elided post-compact", "[tool result elided post-compact", "Host-generated historical excerpts"} {
		if strings.Contains(view, marker) {
			t.Errorf("timeline contains model-context marker %q", marker)
		}
	}
}
