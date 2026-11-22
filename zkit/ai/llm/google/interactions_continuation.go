package google

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"strings"

	api "google.golang.org/genai/interactions/models/interactions"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

type interactionEnvelope struct {
	Model string          `json:"model"`
	Step  json.RawMessage `json:"step"`
}

func (p *Provider) interactionReplay(ctx context.Context, message llm.Message) ([]api.Step, bool, error) {
	if len(message.ContinuationItems) == 0 {
		return nil, false, nil
	}
	items := slices.Clone(message.ContinuationItems)
	for _, item := range items {
		if item.Provider != continuationProvider || item.Format != interactionsContinuationFormat {
			return nil, false, interactionsUnsupported()
		}
		if item.OutputIndex == nil || *item.OutputIndex < 0 {
			return nil, false, ErrInteractionsProtocol
		}
	}
	slices.SortFunc(items, func(a, b llm.ContinuationItem) int { return cmp.Compare(*a.OutputIndex, *b.OutputIndex) })
	steps := make([]api.Step, 0, len(items))
	calls := make(map[string]llm.ToolCall)
	var content, thought strings.Builder
	var contentIndex *int
	for index, item := range items {
		if cause := context.Cause(ctx); cause != nil {
			return nil, false, cause
		}
		if *item.OutputIndex != index {
			return nil, false, ErrInteractionsProtocol
		}
		envelope, err := interactionObject(item.Data)
		if err != nil {
			return nil, false, err
		}
		var model string
		if err := json.Unmarshal(envelope["model"], &model); err != nil || model != p.model {
			return nil, false, ErrInteractionsProtocol
		}
		step, err := decodeInteractionStep(envelope["step"])
		if err != nil {
			return nil, false, err
		}
		text, reasoning, err := interactionProjection(step)
		if err != nil {
			return nil, false, err
		}
		if item.Kind != "" && item.Kind != string(step.Type) {
			return nil, false, ErrInteractionsProtocol
		}
		if text != "" && contentIndex == nil {
			contentIndex = llm.OutputPosition(index)
		}
		content.WriteString(text)
		thought.WriteString(reasoning)
		if call := step.FunctionCallStep; call != nil {
			if item.ID != "" && item.ID != call.ID {
				return nil, false, ErrInteractionsProtocol
			}
			if _, exists := calls[call.ID]; exists {
				return nil, false, ErrInteractionsProtocol
			}
			args, err := json.Marshal(call.Arguments)
			if err != nil {
				return nil, false, ErrInteractionsProtocol
			}
			calls[call.ID] = llm.ToolCall{ID: call.ID, Type: string(api.ToolTypeFunction), OutputIndex: llm.OutputPosition(index), Function: llm.ToolCallFunction{Name: call.Name, Arguments: string(args)}}
		} else if item.ID != "" {
			return nil, false, ErrInteractionsProtocol
		}
		steps = append(steps, step)
	}
	if message.ContentOutputIndex != nil && (contentIndex == nil || *message.ContentOutputIndex != *contentIndex) {
		return nil, false, ErrInteractionsProtocol
	}
	if (message.Content != "" && message.Content != content.String()) ||
		(message.Parts != nil && interactionText(message) != content.String()) ||
		(message.ReasoningContent != "" && message.ReasoningContent != thought.String()) {
		return nil, false, ErrInteractionsProtocol
	}
	seen := make(map[string]bool)
	for _, call := range message.ToolCalls {
		native, exists := calls[call.ID]
		if !exists || seen[call.ID] || call.Type != string(api.ToolTypeFunction) || call.Function.Name != native.Function.Name || (call.OutputIndex != nil && *call.OutputIndex != *native.OutputIndex) {
			return nil, false, ErrInteractionsProtocol
		}
		seen[call.ID] = true
		left, err := interactionArguments(call.Function.Arguments)
		if err != nil {
			return nil, false, err
		}
		right, err := interactionArguments(native.Function.Arguments)
		if err != nil || !equalReplayJSON(ctx, left, right) {
			return nil, false, ErrInteractionsProtocol
		}
	}
	return steps, true, nil
}

// interactionObject uses the SDK's exact field-name interpretation at every
// raw extraction boundary. Unknown differently-cased fields remain inert.
func interactionObject(data []byte) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return nil, ErrInteractionsProtocol
	}
	return fields, nil
}

func decodeInteractionStep(data []byte) (api.Step, error) {
	var step api.Step
	raw, err := interactionObject(data)
	if err != nil {
		return step, err
	}
	var kind api.StepType
	if err := json.Unmarshal(raw["type"], &kind); err != nil {
		return step, ErrInteractionsProtocol
	}
	var textData json.RawMessage
	switch kind {
	case api.StepTypeModelOutput:
		textData = raw["content"]
	case api.StepTypeThought:
		textData = raw["summary"]
	case api.StepTypeFunctionCall:
	default:
		return step, interactionsUnsupported()
	}
	if len(textData) != 0 {
		var texts []json.RawMessage
		if err := json.Unmarshal(textData, &texts); err != nil {
			return step, ErrInteractionsProtocol
		}
		for _, text := range texts {
			if err := validateInteractionText(text); err != nil {
				return step, err
			}
		}
	}
	if err := json.Unmarshal(data, &step); err != nil || step.Type != kind {
		return step, ErrInteractionsProtocol
	}
	// SDK open-object decoding uses float64; reparse canonical arguments before
	// replay so persisted signed histories keep exact numbers at the boundary.
	if step.FunctionCallStep != nil {
		args, err := interactionArguments(string(raw["arguments"]))
		if err != nil {
			return step, err
		}
		step.FunctionCallStep.Arguments = args
	}
	return step, nil
}

// validateInteractionText preserves explicitly empty and whitespace-only text,
// while rejecting missing, null or non-string canonical text.
func validateInteractionText(data []byte) error {
	raw, err := interactionObject(data)
	if err != nil {
		return err
	}
	var kind api.ContentType
	if err := json.Unmarshal(raw["type"], &kind); err != nil {
		return ErrInteractionsProtocol
	}
	if kind != api.ContentTypeText {
		return interactionsUnsupported()
	}
	return validateInteractionTextField(raw["text"])
}

func validateInteractionTextField(data []byte) error {
	var text *string
	if err := json.Unmarshal(data, &text); err != nil || text == nil {
		return ErrInteractionsProtocol
	}
	return nil
}

// equalInteractionSteps compares validated SDK structures with exact JSON
// number semantics, rather than SDK float64 values or serialized spellings.
func equalInteractionSteps(ctx context.Context, left, right api.Step) bool {
	values := make([]any, 2)
	for index, step := range []api.Step{left, right} {
		data, err := json.Marshal(step)
		if err != nil {
			return false
		}
		if err := decodeReplayJSON(data, &values[index]); err != nil {
			return false
		}
	}
	return equalReplayJSON(ctx, values[0], values[1])
}

func interactionChunk(step api.Step, index int, model string) (llm.CompletionChunk, error) {
	content, thought, err := interactionProjection(step)
	if err != nil {
		return llm.CompletionChunk{}, err
	}
	data, err := json.Marshal(step)
	if err != nil {
		return llm.CompletionChunk{}, ErrInteractionsProtocol
	}
	envelope, err := json.Marshal(interactionEnvelope{Model: model, Step: data})
	if err != nil {
		return llm.CompletionChunk{}, ErrInteractionsProtocol
	}
	item := llm.ContinuationItem{Provider: continuationProvider, Format: interactionsContinuationFormat, Kind: string(step.Type), OutputIndex: llm.OutputPosition(index), Data: envelope}
	if step.FunctionCallStep != nil {
		item.ID = step.FunctionCallStep.ID
	}
	chunk := llm.CompletionChunk{Content: content, Thinking: thought, CompletedItems: []llm.ContinuationItem{item}}
	if content != "" {
		chunk.ContentOutputIndex = llm.OutputPosition(index)
	}
	return chunk, nil
}
