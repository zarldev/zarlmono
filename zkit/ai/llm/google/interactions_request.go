package google

import (
	"context"
	"encoding/json"
	"strings"

	api "google.golang.org/genai/interactions/models/interactions"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func (p *Provider) interactionsRequest(ctx context.Context, req llm.CompletionRequest) (api.CreateModelInteraction, error) {
	body := api.CreateModelInteraction{Model: api.Model(p.model), Store: new(false), Background: new(false), Stream: new(req.Stream)}
	if req.Thinking.BudgetTokens != 0 || !req.ChatTemplateKwargs.IsZero() {
		return body, interactionsUnsupported()
	}
	// prompt_cache_key is a host routing hint inserted by the real runner,
	// not a Gemini generation control. This backend makes no routing guarantee.
	for key, value := range req.Options {
		if key != "prompt_cache_key" {
			return body, interactionsUnsupported()
		}
		if _, ok := value.(string); !ok {
			return body, interactionsUnsupported()
		}
	}
	// Affinity is host intent, not a wire-enforced policy on this backend.
	// Enabled is advisory: Gemini exposes thought evidence without an opt-in.
	if req.Temperature != 0 || req.MaxTokens != 0 {
		if req.Temperature < 0 || req.MaxTokens < 0 {
			return body, interactionsUnsupported()
		}
		body.GenerationConfig = &api.GenerationConfig{}
		if req.Temperature != 0 {
			body.GenerationConfig.Temperature = new(req.Temperature) //nolint:staticcheck // Pinned Interactions wire still supports temperature; no replacement exists.
		}
		if req.MaxTokens != 0 {
			body.GenerationConfig.MaxOutputTokens = new(req.MaxTokens)
		}
	}
	switch req.ResponseFormat.Type {
	case llm.ResponseFormatText:
	case llm.ResponseFormatJSONObject, llm.ResponseFormatJSONSchema:
		format := api.TextResponseFormat{MimeType: new(api.TextResponseFormatMimeTypeApplicationJSON)}
		if req.ResponseFormat.Type == llm.ResponseFormatJSONSchema {
			format.Schema = req.ResponseFormat.Schema.Map()
		}
		responseFormat := api.NewResponseFormat(format)
		value := api.NewCreateModelInteractionResponseFormat(responseFormat)
		body.ResponseFormat = &value
	default:
		return body, interactionsUnsupported()
	}
	for _, tool := range req.Tools {
		if tool.Type != string(api.ToolTypeFunction) {
			return body, interactionsUnsupported()
		}
		body.Tools = append(body.Tools, api.NewTool(api.Function{Name: new(tool.Function.Name), Description: new(tool.Function.Description), Parameters: tool.Function.Parameters.Map()}))
	}
	var steps []api.Step
	var systems []string
	calls := make(map[string]string)
	results := make(map[string]bool)
	for _, message := range req.Messages {
		if cause := context.Cause(ctx); cause != nil {
			return body, cause
		}
		for _, part := range message.Parts {
			if part.Type != llm.ContentTypeText {
				return body, interactionsUnsupported()
			}
		}
		if message.Role != llm.RoleAssistant && (len(message.ContinuationItems) != 0 || len(message.ToolCalls) != 0 || message.ReasoningContent != "") {
			return body, interactionsUnsupported()
		}
		switch message.Role {
		case llm.RoleSystem:
			systems = append(systems, interactionText(message))
		case llm.RoleUser:
			steps = append(steps, api.NewStep(api.UserInputStep{Content: interactionContent(message)}))
		case llm.RoleAssistant:
			native, found, err := p.interactionReplay(ctx, message)
			if err != nil {
				return body, err
			}
			if !found {
				if message.ReasoningContent != "" {
					native = append(native, api.NewStep(api.ThoughtStep{Summary: []api.ThoughtSummaryContent{api.NewThoughtSummaryContent(api.TextContent{Text: message.ReasoningContent})}}))
				}
				if len(message.Parts) != 0 || message.Content != "" || len(message.ToolCalls) == 0 {
					native = append(native, api.NewStep(api.ModelOutputStep{Content: interactionContent(message)}))
				}
				for _, call := range message.ToolCalls {
					args, err := interactionArguments(call.Function.Arguments)
					if err != nil || call.Type != string(api.ToolTypeFunction) {
						return body, ErrInteractionsProtocol
					}
					native = append(native, api.NewStep(api.FunctionCallStep{ID: call.ID, Name: call.Function.Name, Arguments: args}))
				}
			}
			for _, step := range native {
				if _, _, err := interactionProjection(step); err != nil {
					return body, err
				}
				if call := step.FunctionCallStep; call != nil {
					if _, exists := calls[call.ID]; exists {
						return body, ErrInteractionsProtocol
					}
					calls[call.ID] = call.Name
				}
			}
			steps = append(steps, native...)
		case llm.RoleTool:
			name, exists := calls[message.ToolCallID]
			if !exists || results[message.ToolCallID] {
				return body, ErrInteractionsProtocol
			}
			results[message.ToolCallID] = true
			// Tool Content is output; supplemental text is retained rather than replacing it.
			var output strings.Builder
			output.WriteString(message.Content)
			for _, part := range message.Parts {
				output.WriteString(part.Text)
			}
			text := output.String()
			steps = append(steps, api.NewStep(api.FunctionResultStep{CallID: message.ToolCallID, Name: new(name), Result: api.NewFunctionResultStepResultUnion(text)}))
		default:
			return body, interactionsUnsupported()
		}
	}
	if len(calls) != len(results) {
		return body, ErrInteractionsProtocol
	}
	if len(steps) == 0 {
		return body, interactionsUnsupported()
	}
	if len(systems) != 0 {
		body.SystemInstruction = new(strings.Join(systems, "\n\n"))
	}
	input := api.NewInteractionsInput(steps)
	body.Input = &input
	return body, nil
}

func interactionContent(message llm.Message) []api.Content {
	if message.Parts == nil {
		return []api.Content{api.NewContent(api.TextContent{Text: message.Content})}
	}
	content := make([]api.Content, 0, len(message.Parts))
	for _, part := range message.Parts {
		content = append(content, api.NewContent(api.TextContent{Text: part.Text}))
	}
	return content
}

func interactionText(message llm.Message) string {
	if message.Parts == nil {
		return message.Content
	}
	var text strings.Builder
	for _, part := range message.Parts {
		text.WriteString(part.Text)
	}
	return text.String()
}

func interactionArguments(text string) (map[string]any, error) {
	var args map[string]any
	if err := decodeReplayJSON([]byte(text), &args); err != nil || args == nil {
		return nil, ErrInteractionsProtocol
	}
	return args, nil
}

// interactionProjection validates the supported output surface at its wire boundary.
func interactionProjection(step api.Step) (string, string, error) {
	var content, thought strings.Builder
	switch step.Type {
	case api.StepTypeModelOutput:
		if step.ModelOutputStep.Error != nil {
			return "", "", ErrInteractionsProtocol
		}
		for _, part := range step.ModelOutputStep.Content {
			if part.Type != api.ContentTypeText {
				return "", "", interactionsUnsupported()
			}
			content.WriteString(part.TextContent.Text)
		}
	case api.StepTypeThought:
		for _, part := range step.ThoughtStep.Summary {
			if part.Type != api.ThoughtSummaryContentTypeText {
				return "", "", interactionsUnsupported()
			}
			thought.WriteString(part.TextContent.Text)
		}
	case api.StepTypeFunctionCall:
		call := step.FunctionCallStep
		if call.ID == "" || call.Name == "" || call.Arguments == nil {
			return "", "", ErrInteractionsProtocol
		}
		if _, err := json.Marshal(call.Arguments); err != nil {
			return "", "", ErrInteractionsProtocol
		}
	default:
		return "", "", interactionsUnsupported()
	}
	return content.String(), thought.String(), nil
}
