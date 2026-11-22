package google

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"google.golang.org/genai"
	"google.golang.org/genai/interactions/models/apierrors"
	api "google.golang.org/genai/interactions/models/interactions"
	"google.golang.org/genai/interactions/models/operations"
	"google.golang.org/genai/interactions/retry"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

const interactionsContinuationFormat = "interactions.step.v1"

// ErrInteractionsProtocol identifies an invalid or incomplete Interactions
// response or native replay payload. Diagnostics never contain response bodies.
var ErrInteractionsProtocol = errors.New("google: invalid interactions protocol")

func interactionsUnsupported() error {
	return fmt.Errorf("google interactions: unsupported request: %w", llm.ErrModelNotSupported)
}

func (p *Provider) streamInteractions(ctx context.Context, req llm.CompletionRequest, yield func(llm.CompletionChunk, error) bool) {
	stopped := false
	forward := func(chunk llm.CompletionChunk, err error) bool {
		if !yield(chunk, err) {
			stopped = true
			return false
		}
		return true
	}
	err := p.runInteractions(ctx, req, forward)
	if stopped {
		return
	}
	if cause := context.Cause(ctx); cause != nil {
		err = cause
	}
	if err != nil {
		yield(llm.CompletionChunk{}, err)
	}
}

func (p *Provider) runInteractions(ctx context.Context, req llm.CompletionRequest, yield func(llm.CompletionChunk, error) bool) error {
	config := p.client.ClientConfig()
	if config.Backend != genai.BackendGeminiAPI || config.HTTPOptions.APIVersion != "v1beta" {
		return interactionsUnsupported()
	}
	body, err := p.interactionsRequest(ctx, req)
	if err != nil {
		return err
	}
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	response, err := p.client.Interactions.Create(ctx, operations.CreateInteractionRequest{
		Body: operations.NewCreateInteractionRequestBody(body),
	}, operations.WithRetries(retry.Config{Strategy: "none"}))
	if err != nil {
		// The SDK can reject a wrong JSON field type before returning its raw
		// response snapshot. Translate that identity without exposing union/body
		// diagnostics. Request serialization uses controlled concrete types.
		var fieldType *json.UnmarshalTypeError
		var errorResponse *apierrors.ResponseValidationError
		if errors.As(err, &fieldType) && !errors.As(err, &errorResponse) {
			return ErrInteractionsProtocol
		}
		return fmt.Errorf("google interactions request: %w", llm.ErrProviderUnavailable)
	}
	state := interactionAssembly{steps: make(map[int]interactionStep)}
	if response.InteractionSSEStreamEvent != nil {
		stream := response.InteractionSSEStreamEvent
		defer func() { _ = stream.Close() }()
		if !req.Stream {
			return ErrInteractionsProtocol
		}
		for stream.Next() {
			if cause := context.Cause(ctx); cause != nil {
				return cause
			}
			downstream, err := state.accept(ctx, stream.Value().Data, stream.RawValue(), p.model, yield)
			if !downstream {
				return nil
			}
			if err != nil {
				return err
			}
		}
		if cause := context.Cause(ctx); cause != nil {
			return cause
		}
		if stream.Err() != nil {
			return ErrInteractionsProtocol
		}
	} else {
		defer func() { _ = response.HTTPMeta.Response.Body.Close() }()
		if req.Stream || response.Interaction == nil {
			return ErrInteractionsProtocol
		}
		resource := response.Interaction
		if len(resource.Errors) != 0 || resource.OutputAudio != nil || resource.OutputImage != nil || resource.OutputVideo != nil || (len(resource.Steps) == 0 && resource.OutputText != nil && *resource.OutputText != "") {
			return ErrInteractionsProtocol
		}
		// The pinned SDK retains the consumed JSON bytes in HTTPMeta.Response.Body.
		// Read that local snapshot to avoid float64 loss in open function arguments.
		data, readErr := io.ReadAll(response.HTTPMeta.Response.Body)
		if readErr != nil {
			return ErrInteractionsProtocol
		}
		raw, err := interactionObject(data)
		if err != nil {
			return err
		}
		steps, err := interactionRawSteps(raw)
		if err != nil {
			return err
		}
		state.status = resource.Status
		state.usage(resource.Usage)
		for index, value := range steps {
			step, err := decodeInteractionStep(value)
			if err != nil {
				return err
			}
			if _, _, err := interactionProjection(step); err != nil {
				return err
			}
			state.steps[index] = interactionStep{step: step, stopped: true}
		}
	}
	return state.finish(ctx, p.model, yield)
}

func interactionUsage(value *api.Usage) llm.Usage {
	number := func(value *int) int {
		if value != nil {
			return *value
		}
		return 0
	}
	return llm.Usage{
		PromptTokens:     number(value.TotalInputTokens),
		CompletionTokens: number(value.TotalOutputTokens) + number(value.TotalThoughtTokens),
		TotalTokens:      number(value.TotalTokens), CachedTokens: number(value.TotalCachedTokens),
		ReasoningTokens: number(value.TotalThoughtTokens), ReasoningTokensReported: value.TotalThoughtTokens != nil,
	}
}
