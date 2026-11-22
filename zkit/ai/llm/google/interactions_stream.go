package google

import (
	"context"
	"encoding/json"

	api "google.golang.org/genai/interactions/models/interactions"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

type interactionStep struct {
	step             api.Step
	arguments        string
	argumentsStarted bool
	stopped          bool
	emitted          bool
}

type interactionAssembly struct {
	steps     map[int]interactionStep
	status    api.InteractionStatus
	terminal  bool
	completed bool
	tokens    llm.Usage
	reported  bool
}

func (s *interactionAssembly) usage(value *api.Usage) {
	if value != nil {
		s.tokens = interactionUsage(value)
		s.reported = true
	}
}

func (s *interactionAssembly) accept(ctx context.Context, event api.InteractionSSEEvent, raw json.RawMessage, model string, yield func(llm.CompletionChunk, error) bool) (bool, error) {
	if s.terminal && (event.Type != api.InteractionSSEEventTypeInteractionCompleted || s.completed) {
		return true, ErrInteractionsProtocol
	}
	// RawValue retains presence and JSON types that the SDK's scalar fields lose.
	wire, err := interactionObject(raw)
	if err != nil {
		return true, err
	}
	data, err := interactionObject(wire["data"])
	if err != nil {
		return true, err
	}
	var eventType api.InteractionSSEEventType
	if err := json.Unmarshal(data["event_type"], &eventType); err != nil || eventType != event.Type {
		return true, ErrInteractionsProtocol
	}
	switch event.Type {
	case api.InteractionSSEEventTypeStepStart, api.InteractionSSEEventTypeStepDelta, api.InteractionSSEEventTypeStepStop:
		var index *int
		if err := json.Unmarshal(data["index"], &index); err != nil || index == nil || *index < 0 {
			return true, ErrInteractionsProtocol
		}
	}
	switch event.Type {
	case api.InteractionSSEEventTypeInteractionCreated:
		resource := event.InteractionCreatedEvent.Interaction
		if resource.Status != api.InteractionSseEventInteractionStatusInProgress && resource.Status != api.InteractionSseEventInteractionStatusQueued {
			return true, ErrInteractionsProtocol
		}
		s.usage(resource.Usage)
	case api.InteractionSSEEventTypeStepStart:
		start := event.StepStart
		if _, exists := s.steps[start.Index]; exists {
			return true, ErrInteractionsProtocol
		}
		step, err := decodeInteractionStep(data["step"])
		if err != nil {
			return true, err
		}
		if _, _, err := interactionProjection(step); err != nil {
			return true, err
		}
		s.steps[start.Index] = interactionStep{step: step}
	case api.InteractionSSEEventTypeStepDelta:
		delta := event.StepDelta
		entry, exists := s.steps[delta.Index]
		if !exists || entry.stopped {
			return true, ErrInteractionsProtocol
		}
		if err := validateInteractionDelta(data["delta"], delta.Delta); err != nil {
			return true, err
		}
		if err := entry.apply(delta.Delta); err != nil {
			return true, err
		}
		s.steps[delta.Index] = entry
		if delta.Metadata != nil {
			s.usage(delta.Metadata.TotalUsage)
		}
	case api.InteractionSSEEventTypeStepStop:
		stop := event.StepStop
		entry, exists := s.steps[stop.Index]
		if !exists || entry.stopped {
			return true, ErrInteractionsProtocol
		}
		if entry.argumentsStarted {
			args, err := interactionArguments(entry.arguments)
			if err != nil {
				return true, err
			}
			entry.step.FunctionCallStep.Arguments = args
		}
		if _, _, err := interactionProjection(entry.step); err != nil {
			return true, err
		}
		entry.stopped = true
		s.steps[stop.Index] = entry
		s.usage(stop.Usage) // step_usage is local, not a cumulative snapshot.
		return s.emitReady(model, yield)
	case api.InteractionSSEEventTypeInteractionStatusUpdate:
		update := event.InteractionStatusUpdate
		s.usage(update.Usage)
		switch update.Status {
		case api.InteractionStatusUpdateStatusInProgress, api.InteractionStatusUpdateStatusQueued:
		case api.InteractionStatusUpdateStatusCompleted, api.InteractionStatusUpdateStatusRequiresAction:
			s.status = api.InteractionStatus(update.Status)
			s.terminal = true
		default:
			return true, ErrInteractionsProtocol
		}
	case api.InteractionSSEEventTypeInteractionCompleted:
		resource := event.InteractionCompletedEvent.Interaction
		if s.terminal && s.status != api.InteractionStatus(resource.Status) {
			return true, ErrInteractionsProtocol
		}
		s.completed = true
		s.status = api.InteractionStatus(resource.Status)
		s.terminal = true
		s.usage(resource.Usage)
		rawInteraction, err := interactionObject(data["interaction"])
		if err != nil {
			return true, err
		}
		steps, err := interactionRawSteps(rawInteraction)
		if err != nil {
			return true, err
		}
		if len(steps) != 0 {
			// Lifecycle snapshots must agree with assembled steps, never replace them.
			if len(steps) != len(s.steps) {
				return true, ErrInteractionsProtocol
			}
			for index, data := range steps {
				step, err := decodeInteractionStep(data)
				if err != nil {
					return true, err
				}
				if _, _, err := interactionProjection(step); err != nil {
					return true, err
				}
				entry, exists := s.steps[index]
				if !exists || !entry.stopped || !equalInteractionSteps(ctx, step, entry.step) {
					return true, ErrInteractionsProtocol
				}
			}
		}
	default:
		return true, ErrInteractionsProtocol
	}
	if cause := context.Cause(ctx); cause != nil {
		return true, cause
	}
	return true, nil
}

// interactionRawSteps extracts optional snapshots with canonical keys, just
// like the SDK. A differently-cased unknown key cannot replace native steps.
func interactionRawSteps(fields map[string]json.RawMessage) ([]json.RawMessage, error) {
	var steps []json.RawMessage
	if data := fields["steps"]; len(data) != 0 {
		if err := json.Unmarshal(data, &steps); err != nil {
			return nil, ErrInteractionsProtocol
		}
	}
	return steps, nil
}

// validateInteractionDelta checks required text before SDK scalar decoding can
// turn null or omission into an apparently valid empty string.
func validateInteractionDelta(data []byte, delta api.StepDeltaData) error {
	raw, err := interactionObject(data)
	if err != nil {
		return err
	}
	var kind api.StepDeltaDataType
	if err := json.Unmarshal(raw["type"], &kind); err != nil || kind != delta.Type {
		return ErrInteractionsProtocol
	}
	switch kind {
	case api.StepDeltaDataTypeText:
		return validateInteractionTextField(raw["text"])
	case api.StepDeltaDataTypeThoughtSummary:
		return validateInteractionText(raw["content"])
	}
	return nil
}

// apply owns the SDK union pointers held by this stream-local value entry.
func (s *interactionStep) apply(delta api.StepDeltaData) error {
	switch delta.Type {
	case api.StepDeltaDataTypeArgumentsDelta:
		if s.step.FunctionCallStep == nil || delta.ArgumentsDelta.Arguments == nil {
			return ErrInteractionsProtocol
		}
		// A nonempty complete starting argument object cannot also be a fragment.
		if len(s.step.FunctionCallStep.Arguments) != 0 {
			return ErrInteractionsProtocol
		}
		s.argumentsStarted = true
		s.arguments += *delta.ArgumentsDelta.Arguments
	case api.StepDeltaDataTypeText:
		if s.step.ModelOutputStep == nil {
			return ErrInteractionsProtocol
		}
		content := s.step.ModelOutputStep.Content
		if len(content) == 0 {
			content = append(content, api.NewContent(api.TextContent{Text: delta.TextDelta.Text}))
		} else {
			last := content[len(content)-1]
			if last.TextContent == nil {
				return ErrInteractionsProtocol
			}
			last.TextContent.Text += delta.TextDelta.Text
			content[len(content)-1] = last
		}
		s.step.ModelOutputStep.Content = content
	case api.StepDeltaDataTypeTextAnnotationDelta:
		if s.step.ModelOutputStep == nil || len(s.step.ModelOutputStep.Content) == 0 {
			return ErrInteractionsProtocol
		}
		content := s.step.ModelOutputStep.Content
		last := content[len(content)-1]
		if last.TextContent == nil {
			return ErrInteractionsProtocol
		}
		last.TextContent.Annotations = append(last.TextContent.Annotations, delta.TextAnnotationDelta.Annotations...)
		content[len(content)-1] = last
		s.step.ModelOutputStep.Content = content
	case api.StepDeltaDataTypeThoughtSignature:
		if s.step.ThoughtStep == nil || delta.ThoughtSignatureDelta.Signature == nil {
			return ErrInteractionsProtocol
		}
		if s.step.ThoughtStep.Signature != nil {
			return ErrInteractionsProtocol
		}
		// A signature delta carries the complete encrypted signature, not an
		// argument/text fragment. Never concatenate or repair signed data.
		s.step.ThoughtStep.Signature = new(*delta.ThoughtSignatureDelta.Signature)
	case api.StepDeltaDataTypeThoughtSummary:
		if s.step.ThoughtStep == nil || delta.ThoughtSummaryDelta.Content == nil || delta.ThoughtSummaryDelta.Content.Type != api.ContentTypeText {
			return ErrInteractionsProtocol
		}
		s.step.ThoughtStep.Summary = append(s.step.ThoughtStep.Summary, api.NewThoughtSummaryContent(*delta.ThoughtSummaryDelta.Content.TextContent))
	default:
		return interactionsUnsupported()
	}
	return nil
}

func (s *interactionAssembly) emitReady(model string, yield func(llm.CompletionChunk, error) bool) (bool, error) {
	for index := range len(s.steps) {
		entry, exists := s.steps[index]
		if !exists || !entry.stopped {
			break
		}
		if entry.emitted {
			continue
		}
		chunk, err := interactionChunk(entry.step, index, model)
		if err != nil {
			return true, err
		}
		if !yield(chunk, nil) {
			return false, nil
		}
		entry.emitted = true
		s.steps[index] = entry
	}
	return true, nil
}

func (s *interactionAssembly) finish(ctx context.Context, model string, yield func(llm.CompletionChunk, error) bool) error {
	if s.status != api.InteractionStatusCompleted && s.status != api.InteractionStatusRequiresAction {
		return ErrInteractionsProtocol
	}
	calls := make([]llm.ToolCall, 0)
	ids := make(map[string]bool)
	for index := range len(s.steps) {
		entry, exists := s.steps[index]
		if !exists || !entry.stopped {
			return ErrInteractionsProtocol
		}
		if call := entry.step.FunctionCallStep; call != nil {
			if ids[call.ID] {
				return ErrInteractionsProtocol
			}
			ids[call.ID] = true
			args, err := json.Marshal(call.Arguments)
			if err != nil {
				return ErrInteractionsProtocol
			}
			calls = append(calls, llm.ToolCall{ID: call.ID, Type: string(api.ToolTypeFunction), OutputIndex: llm.OutputPosition(index), Function: llm.ToolCallFunction{Name: call.Name, Arguments: string(args)}})
		}
	}
	if (s.status == api.InteractionStatusRequiresAction) != (len(calls) > 0) {
		return ErrInteractionsProtocol
	}
	if downstream, err := s.emitReady(model, yield); err != nil || !downstream {
		return err
	}
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	reason := llm.FinishReasons.STOP
	if len(calls) > 0 {
		reason = llm.FinishReasons.TOOLCALLS
	}
	yield(llm.CompletionChunk{ToolCalls: calls, FinishReason: reason, Usage: s.tokens, UsageReported: s.reported}, nil)
	return nil
}
