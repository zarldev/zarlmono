package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	model "github.com/zarldev/zarlmono/zkit/agent/computer"
	"github.com/zarldev/zarlmono/zkit/agent/computer/browser"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	toolcomputer "github.com/zarldev/zarlmono/zkit/ai/tools/computer"
)

const questionCount = 10

type quizHarness struct {
	provider llm.Provider
	registry *tools.Registry
	session  *browser.Session
}

func newQuizHarness(ctx context.Context, cfg config, provider llm.Provider) (*quizHarness, error) {
	var opts []browser.Option
	if cfg.chromePath != "" {
		opts = append(opts, browser.WithChromePath(cfg.chromePath))
	}
	opts = append(opts, browser.WithHeadless(cfg.headless))

	session, err := browser.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("start browser: %w", err)
	}

	reg := tools.NewRegistry()
	toolcomputer.Register(reg, session, session)
	return &quizHarness{provider: provider, registry: reg, session: session}, nil
}

func (h *quizHarness) close() {
	if h != nil && h.session != nil {
		_ = h.session.Close()
	}
}

func (h *quizHarness) observe(ctx context.Context) (model.Observation, error) {
	return execute[model.Observation](ctx, h.registry, toolcomputer.ToolNameComputerObserve, toolcomputer.ObserveArgs{
		IncludeText:    true,
		IncludeTargets: true,
	})
}

func (h *quizHarness) navigate(ctx context.Context, url string) (model.Observation, error) {
	return execute[model.Observation](ctx, h.registry, toolcomputer.ToolNameComputerAct, toolcomputer.ActArgs{
		Action: toolcomputer.Action{Kind: model.ActionKinds.NAVIGATE, URL: url},
		Until:  &toolcomputer.Trigger{Kind: model.TriggerKinds.NAVIGATIONCOMPLETE},
	})
}

func (h *quizHarness) click(ctx context.Context, answer string, untilText string) (model.Observation, error) {
	return execute[model.Observation](ctx, h.registry, toolcomputer.ToolNameComputerAct, toolcomputer.ActArgs{
		Action: toolcomputer.Action{
			Kind:   model.ActionKinds.CLICK,
			Target: &toolcomputer.TargetRef{Role: "button", Name: answer},
		},
		Until: &toolcomputer.Trigger{Kind: model.TriggerKinds.TEXTPRESENT, Text: untilText},
	})
}
func (h *quizHarness) run(ctx context.Context, questions []quizQuestion, url string) (model.Observation, error) {
	slog.InfoContext(ctx, "navigating to quiz", "url", url)
	if _, err := h.navigate(ctx, url); err != nil {
		return model.Observation{}, fmt.Errorf("navigate: %w", err)
	}

	var obs model.Observation
	for i := 1; i <= len(questions); i++ {
		var err error
		obs, err = h.observe(ctx)
		if err != nil {
			return model.Observation{}, fmt.Errorf("observe: %w", err)
		}

		choices := buttonNames(obs.Targets)
		answer, err := askLLM(ctx, h.provider, obs.VisibleText, choices)
		if err != nil {
			return model.Observation{}, fmt.Errorf("llm answer: %w\nvisible text:\n%s", err, obs.VisibleText)
		}
		slog.InfoContext(ctx, "question answered", "question", i, "answer", answer)

		untilText := fmt.Sprintf("Question %d of %d", i+1, len(questions))
		if i == len(questions) {
			untilText = "Quiz complete"
		}
		obs, err = h.click(ctx, answer, untilText)
		if err != nil {
			return model.Observation{}, fmt.Errorf("answer: %w", err)
		}
	}
	return obs, nil
}

func pauseBeforeExit(ctx context.Context, pause time.Duration, headless bool) {
	if pause == 0 && !headless {
		pause = 10 * time.Second
	}
	if pause <= 0 {
		return
	}
	slog.InfoContext(ctx, "keeping browser open", "duration", pause)
	select {
	case <-ctx.Done():
	case <-time.After(pause):
	}
}
