package tui_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/agent/computer"
	"github.com/zarldev/zarlmono/zkit/agent/computer/browser"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

const settlementScreenshotURI = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a3XkAAAAASUVORK5CYII="

type screenshotSettlementSession struct{}

func (screenshotSettlementSession) Observe(context.Context, computer.ObserveRequest) (computer.Observation, error) {
	return computer.Observation{
		Surface:    computer.SurfaceInfo{Kind: computer.SurfaceKinds.BROWSER},
		Screenshot: &computer.ObservationImage{MIMEType: "image/png", DataURI: settlementScreenshotURI},
	}, nil
}

func (s screenshotSettlementSession) Act(ctx context.Context, _ computer.ActionRequest) (computer.Observation, error) {
	return s.Observe(ctx, computer.ObserveRequest{})
}

func (screenshotSettlementSession) Close() error { return nil }

type screenshotSettlementProvider struct{ calls int }

func (*screenshotSettlementProvider) Name() string { return "openai-codex" }

func (p *screenshotSettlementProvider) Complete(context.Context, llm.CompletionRequest) llm.CompletionStream {
	p.calls++
	chunk := llm.CompletionChunk{Content: "answer"}
	if p.calls == 1 {
		chunk = llm.CompletionChunk{ToolCalls: []llm.ToolCall{{ID: "screen", Type: "function", Function: llm.ToolCallFunction{Name: "computer_observe", Arguments: `{"include_screenshot":true}`}}}}
	}
	return func(yield func(llm.CompletionChunk, error) bool) { yield(chunk, nil) }
}

func TestLiveTurnScreenshotSettlementAllowsNextPrompt(t *testing.T) {
	f := newBeforeFixture(t, engine.WithComputerSessionFactory(func(context.Context, ...browser.Option) (engine.ComputerSession, error) {
		return screenshotSettlementSession{}, nil
	}))
	provider := &screenshotSettlementProvider{}
	f.live.SetProviderSpec(provider, engine.ProviderSpec{Name: "openai-codex", Model: "gpt-5.6"})
	for _, prompt := range []string{"take a screenshot", "continue"} {
		settleRewindTurn(t, f, prompt)
		stored, err := f.store.GetSessionResumeState(t.Context(), f.ui.SessionIdentity())
		if err != nil {
			t.Fatal(err)
		}
		resume, err := rewind.DecodeResume(stored.Session.ContextJSON)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(resume.Context, f.live.ContextSnapshot()) {
			t.Fatalf("screenshot turn was not saved exactly: %s", f.ui.ToastText())
		}
		images := 0
		for _, message := range resume.Context {
			for _, part := range message.Parts {
				if part.Image != nil && part.Image.DataURI == settlementScreenshotURI && part.Image.MIMEType == "image/png" {
					images++
				}
			}
		}
		if images != 1 {
			t.Fatalf("saved screenshot count = %d, want 1", images)
		}
	}
	if provider.calls != 3 {
		t.Fatalf("provider calls = %d, want screenshot, answer, follow-up", provider.calls)
	}
}
