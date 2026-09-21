package runner

import (
	"context"
	"fmt"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// prepareRequest refreshes the iteration control plane before shaping and
// recording the exact request. Run accounts for this phase even on failure.
func (t *taskRun) refreshPrompt(ctx context.Context) error {
	r, spec := t.r, t.spec
	if t.iter > 0 && r.iterationPrompt {
		system, err := r.prompt.System(ctx, spec.PromptVars)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrPromptRender, err)
		}
		t.messages[0] = llm.Message{Role: llm.RoleSystem, Content: system}
	}
	return nil
}

func (t *taskRun) prepareRequest(ctx context.Context, finalizeNudge string) (llm.CompletionRequest, error) {
	r, spec := t.r, t.spec
	shaped := r.template.ShapeMessages(t.messages, t.thinking)
	if finalizeNudge != "" {
		// Request-only: the nudge never enters canonical history. Cap the slice so
		// append cannot overwrite the backing array shared with that history.
		shaped = append(shaped[:len(shaped):len(shaped)], llm.Message{Role: llm.RoleUser, Content: finalizeNudge})
	}
	requestTools, err := r.buildRequestTools(ctx, t.st.toolSurfaceFingerprint)
	if err != nil {
		return llm.CompletionRequest{}, err
	}
	t.st.toolSurfaceFingerprint = requestTools.surface.Fingerprint
	t.toolSurface = requestTools.surface
	options := r.modelOptions.Clone()
	if options == nil {
		options = make(llm.ModelOptions, 1)
	}
	// Pin every iteration to the same prompt-cache route. Providers that do not
	// support routing ignore this option; it does not guarantee a cache hit.
	options["prompt_cache_key"] = string(spec.ID)
	req := llm.CompletionRequest{
		Messages: shaped, Tools: requestTools.tools, Stream: true, MaxTokens: r.maxTokens,
		Temperature: r.temperature, Thinking: llm.ThinkingConfig{Enabled: t.thinking},
		ChatTemplateKwargs: r.template.ThinkingKwargs(t.thinking), Options: options,
	}
	if r.historySink != nil && spec.Depth == 0 {
		if err := r.historySink.Request(ctx, req); err != nil {
			return llm.CompletionRequest{}, fmt.Errorf("%w: %w", ErrReplayHistory, err)
		}
	}
	return req, nil
}
