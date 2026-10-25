package tui_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

type tieredLiveProvider struct {
	client runner.Client
	check  func(llm.CompletionRequest)
}

func (tieredLiveProvider) Name() string { return "openai" }

func (p tieredLiveProvider) Complete(ctx context.Context, request llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		p.check(request)
		p.client.Complete(ctx, request)(yield)
	}
}

func TestTieredLiveTimelinePreservesOriginalOutputThroughAutosaveAndResume(t *testing.T) {
	f := newBeforeFixture(t)
	f.ui.SetStartupReady(true)
	f.ui.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	first := strings.Repeat("original historical narrative ", 5000) + "ORIGINALTAIL\n\n"
	const answer = "Original complete final answer."
	const quoted = "Quoted example: [reasoning trimmed — 69 chars elided; operational tail retained]"
	client := runnertest.NewClient([][]llm.CompletionChunk{
		{{Content: first, UsageReported: true, Usage: llm.Usage{PromptTokens: 64000, TotalTokens: 64000}},
			runnertest.ChunkToolCall("inspect", "program", `{"script":"emit(call(\"ls\", {}))"}`)},
		{{UsageReported: true, Usage: llm.Usage{PromptTokens: 64000, TotalTokens: 64000}},
			runnertest.ChunkToolCall("inspect-again", "program", `{"script":"emit(call(\"ls\", {}))"}`)},
		{runnertest.ChunkText(answer)},
		{runnertest.ChunkText(quoted)},
	})
	calls, sawExcerpt := 0, false
	provider := tieredLiveProvider{client: client, check: func(request llm.CompletionRequest) {
		calls++
		if calls == 2 {
			var original bool
			for _, message := range request.Messages {
				original = original || message.Content == first
			}
			if !original {
				t.Error("newest unconsumed tool exchange was compacted")
			}
		}
		if calls != 3 {
			return
		}
		for _, message := range request.Messages {
			if strings.HasPrefix(message.Content, "Host-generated historical excerpts") {
				sawExcerpt = true
				if message.Role != llm.RoleUser || message.Observation.Version != 1 {
					t.Error("live request attributes host excerpts to assistant output")
				}
			}
		}
		// The real sink must already have delivered the complete first response
		// before the compacted third model request, not just at terminal save.
		f.sink.Drain()
		f.mu.Lock()
		var streamed strings.Builder
		for _, event := range f.events {
			if content, ok := event.(teasink.ContentMsg); ok {
				streamed.WriteString(content.Delta)
			}
		}
		f.mu.Unlock()
		if streamed.String() != first {
			t.Error("live output was replaced before reaching the timeline event stream")
		}
	}}
	f.live.ApplyTarget(engine.TargetUpdate{Provider: provider, Spec: engine.ProviderSpec{Name: "openai", Model: "saved-model"}, Window: 64000})
	settleRewindTurn(t, f, "first question")
	if !sawExcerpt || calls != 3 {
		t.Fatalf("production compaction did not run: calls=%d excerpt=%v", calls, sawExcerpt)
	}
	assertTieredLiveText(t, f.ui, first+answer)
	settleRewindTurn(t, f, "next question")
	assertTieredLiveText(t, f.ui, first+answer+quoted)

	stored, err := f.store.GetSessionResumeState(t.Context(), f.ui.SessionIdentity())
	if err != nil {
		t.Fatal(err)
	}
	resume, err := rewind.DecodeResume(stored.Session.ContextJSON)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(resume.Context, f.live.ContextSnapshot()) {
		t.Fatal("autosave did not persist the exact compacted context")
	}
	var savedAssistant strings.Builder
	for _, entry := range stored.Transcript.Entries {
		if entry.Kind != "assistant_message" {
			continue
		}
		var payload transcript.Payload
		if err := json.Unmarshal(entry.PayloadJSON, &payload); err != nil {
			t.Fatal(err)
		}
		savedAssistant.WriteString(payload.Text)
	}
	if savedAssistant.String() != first+answer+quoted {
		t.Fatal("autosave shortened original assistant observations or scrubbed a quoted marker")
	}
	exportPath := filepath.Join(t.TempDir(), "conversation.md")
	export := f.ui.Submit("/export " + exportPath)
	if export == nil {
		t.Fatal("missing export command")
	}
	f.ui.Update(export())
	body, err := os.ReadFile(exportPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), first) || !strings.Contains(string(body), quoted) || strings.Contains(string(body), "Host-generated historical excerpts") {
		t.Fatal("export confused compacted host input with genuine assistant output")
	}

	resumed := tui.New()
	resumed.SetLiveRunner(f.live)
	settings := engine.NewSettings(f.store, nil, nil, f.ws.Root())
	settings.Registry = nil // resume the directly supplied provider route
	resumed.SetSettings(settings)
	if err := resumed.ResumeSavedSession(t.Context(), f.ui.SessionIdentity()); err != nil {
		t.Fatal(err)
	}
	resumed.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	assertTieredLiveText(t, resumed, first+answer+quoted)
	if !strings.Contains(ansi.Strip(resumed.View().Content), quoted) {
		t.Fatal("resumed timeline silently filtered genuine quoted provider output")
	}
}

func assertTieredLiveText(t *testing.T, ui *tui.UI, want string) {
	t.Helper()
	var assistant strings.Builder
	for _, entry := range ui.CanonicalThread().Entries() {
		if entry.Kind == transcript.EntryKinds.ENTRYASSISTANTMESSAGE {
			assistant.WriteString(entry.Payload.Text)
		}
	}
	if assistant.String() != want {
		t.Fatal("live timeline changed original assistant text")
	}
	if strings.Contains(ansi.Strip(ui.View().Content), "Host-generated historical excerpts") {
		t.Fatal("host model-context projection became visible chat output")
	}
}
