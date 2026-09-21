package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestSessionSaveFeedbackDistinguishesFailureWithoutPrivateDetails(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		checkpoint bool
	}{
		{name: "storage", want: "save error (see log)"},
		{name: "checkpoint", want: "checkpoint rejected", checkpoint: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var f *beforeFixture
			if tc.checkpoint {
				f = newBeforeFixture(t)
				p := nativeSettlementProvider{name: "openai", chunk: llm.CompletionChunk{
					Content: "answer", CompletedItems: []llm.ContinuationItem{{
						Provider: "openai", Format: "private-canary", Data: []byte(`{"private":"private-canary"}`),
					}},
				}}
				f.live.SetProviderSpec(p, engine.ProviderSpec{Name: "openai", Model: "saved-model"})
				settleRewindTurn(t, f, "checkpoint failure")
			} else {
				f, _ = failedRecoveryFixture(t)
			}
			f.ui.Update(tea.WindowSizeMsg{Width: 80, Height: 32})
			f.ui.SetSuccessToast("transient feedback")
			view := ansi.Strip(f.ui.View().Content)
			if !strings.Contains(view, "Not saved — "+tc.want) || !strings.Contains(view, "Ctrl+q: recovery") {
				t.Fatalf("narrow footer lost actionable unsaved status:\n%s", view)
			}
			if strings.Contains(view, "New turns are memory-only") {
				t.Fatal("long warning still occupies the footer")
			}
			openRecovery(f.ui)
			view = ansi.Strip(f.ui.View().Content)
			if !strings.Contains(view, "Not saved: "+tc.want) || !strings.Contains(view, "restart loses unsaved turns") {
				t.Fatalf("recovery omitted failure category or loss warning:\n%s", view)
			}
			if strings.Contains(view, "private-canary") || strings.Contains(view, "injected settlement failure") {
				t.Fatal("raw error or context leaked into recovery")
			}
			if tc.checkpoint {
				if !strings.Contains(view, "Retrying unchanged context cannot help") || recoveryKey(f.ui, 'r') != nil {
					t.Fatal("invalid checkpoint offered an ineffective retry")
				}
			} else {
				removeRecoveryFailure(t, f)
				cmd := recoveryKey(f.ui, 'r')
				if cmd == nil {
					t.Fatal("retryable storage error lost retry")
				}
				f.ui.Update(cmd())
				recoveryKey(f.ui, tea.KeyEscape)
				if strings.Contains(ansi.Strip(f.ui.View().Content), "Not saved") {
					t.Fatal("successful retry did not clear unsaved status")
				}
			}
		})
	}
}
