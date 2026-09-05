package openaicodex

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// reasoningEffort enumerates the values the Responses API accepts for
// reasoning.effort. The plugin's "xhigh" and GPT-5.6 "max" variants are
// Codex-only for ChatGPT-authenticated runs — sent as-is here.
type reasoningEffort string

const (
	reasoningEffortLow    reasoningEffort = "low"
	reasoningEffortMedium reasoningEffort = "medium"
	reasoningEffortHigh   reasoningEffort = "high"
	reasoningEffortXHigh  reasoningEffort = "xhigh"
	reasoningEffortMax    reasoningEffort = "max"
)

// textVerbosity enumerates text.verbosity values. medium is the
// default; low/high tweak how chatty the model is for the same task.
type textVerbosity string

// responsesRequest is the JSON body posted to /codex/responses. The
// shape is the Responses API (not Chat Completions): a flat `input`
// list of typed items rather than role-tagged `messages`, and config
// blocks (`reasoning`, `text`) for behaviour knobs.
//
// Unset pointer fields are omitted from the wire body — the Codex
// backend has opinionated defaults for anything we don't send, so
// we only set what's meaningful for the current request.
type responsesRequest struct {
	Model             string           `json:"model"`
	Instructions      string           `json:"instructions,omitempty"`
	Input             []inputItem      `json:"input"`
	Tools             []responsesTool  `json:"tools,omitempty"`
	ToolChoice        string           `json:"tool_choice,omitempty"`
	Stream            bool             `json:"stream"`
	Reasoning         *reasoningConfig `json:"reasoning,omitempty"`
	Text              *textConfig      `json:"text,omitempty"`
	Include           []string         `json:"include,omitempty"`
	Store             bool             `json:"store"`
	ParallelToolCalls bool             `json:"parallel_tool_calls,omitempty"`
	PromptCacheKey    string           `json:"prompt_cache_key,omitempty"`
}

// inputItem is one entry in the responsesRequest.Input array. The
// Responses API multiplexes several kinds of items through this one
// shape; Type selects which other fields are meaningful:
//
//	sseTypeMessage              — role + content parts
//	sseTypeFunctionCall        — assistant's tool invocation (history)
//	"function_call_output" — tool result feeding back to the model
//	"reasoning"            — prior reasoning bundle (rare; usually only
//	                         sent back when "reasoning.encrypted_content"
//	                         is included).
type inputItem struct {
	Type    string        `json:"type"`
	Role    string        `json:"role,omitempty"`
	Content []contentPart `json:"content,omitempty"`
	// Function-call fields. CallID is the cross-reference both ways
	// between a sseTypeFunctionCall item and its "function_call_output"
	// — the Codex backend rejects orphans.
	CallID           string          `json:"call_id,omitempty"`
	Name             string          `json:"name,omitempty"`
	Arguments        string          `json:"arguments,omitempty"`
	Output           string          `json:"output,omitempty"`
	ID               string          `json:"id,omitempty"`
	EncryptedContent string          `json:"encrypted_content,omitempty"`
	Raw              json.RawMessage `json:"-"`
}

func (i inputItem) MarshalJSON() ([]byte, error) {
	if len(i.Raw) > 0 {
		return i.Raw, nil
	}
	type wire inputItem
	return json.Marshal(wire(i))
}

// contentPart is one element of an inputItem.Content array. text uses
// sseTypeInputText for user turns and "output_text" for assistant turns;
// images go through "input_image".
type contentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
	VideoURL string `json:"video_url,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

// responsesTool is the function-tool shape the Responses API accepts.
// Unlike Chat Completions there's no outer { "type":sseTypeFunction,sseTypeFunction:{...} }
// wrapper — Name/Description/Parameters live at the top level.
type responsesTool struct {
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
	Strict      bool           `json:"strict,omitempty"`
}

type reasoningConfig struct {
	Effort  reasoningEffort `json:"effort,omitempty"`
	Summary string          `json:"summary,omitempty"`
}

type textConfig struct {
	Verbosity textVerbosity `json:"verbosity,omitempty"`
	Format    *textFormat   `json:"format,omitempty"`
}

// textFormat is the Responses API's structured-output directive, carried
// on text.format. Unlike Chat Completions the schema fields are flat
// (name/schema/strict live directly under format, not nested under a
// json_schema key). Type is "json_object" or "json_schema".
type textFormat struct {
	Type   string         `json:"type"`
	Name   string         `json:"name,omitempty"`
	Schema map[string]any `json:"schema,omitempty"`
	Strict bool           `json:"strict,omitempty"`
}

// responseFormatToText maps the provider-neutral ResponseFormat onto the
// Responses API's text.format. Returns nil for the unconstrained text
// default. json_schema requires a name, so an empty Name defaults to
// "response".
func responseFormatToText(rf llm.ResponseFormat) *textFormat {
	switch rf.Type {
	case llm.ResponseFormatJSONObject:
		return &textFormat{Type: "json_object"}
	case llm.ResponseFormatJSONSchema:
		name := rf.Name
		if name == "" {
			name = "response"
		}
		return &textFormat{Type: "json_schema", Name: name, Schema: rf.Schema.Map(), Strict: rf.Strict}
	default:
		return nil
	}
}

// buildRequest maps an llm.CompletionRequest plus the resolved model +
// instructions into the wire body for /codex/responses. This provider
// always requests streaming SSE from Codex, regardless of req.Stream,
// because run parses one streaming wire shape for text, reasoning, and
// tool-call events. Reasoning effort, text verbosity, and tool_choice
// can be overridden via req.Options (keys: "reasoning_effort",
// "text_verbosity", "tool_choice", "parallel_tool_calls",
// "prompt_cache_key").
func buildRequest(req llm.CompletionRequest, model, instructions string, suppressDefaultEffort ...bool) responsesRequest {
	rr := responsesRequest{
		Model:        model,
		Instructions: instructions,
		Input:        messagesToInput(req.Messages),
		Tools:        toolsToResponsesTools(req.Tools),
		Stream:       true,
		// `store: false` keeps the Codex backend from persisting
		// conversation state server-side. We're a stateless client.
		Store: false,
		// Always include encrypted reasoning so multi-turn flows
		// preserve the model's chain-of-thought across requests.
		Include:           []string{"reasoning.encrypted_content"},
		ParallelToolCalls: true,
	}
	if len(req.Tools) > 0 {
		rr.ToolChoice = "auto"
	}
	// Default reasoning_summary to "detailed" rather than "auto":
	// "auto" is a server-side heuristic that frequently returns NO
	// summary for short prompts, which leaves the zarlcode live
	// reasoning pane permanently empty and looks broken to users.
	// "detailed" guarantees the model's reasoning summary is streamed
	// as response.reasoning_summary_text.delta events; the codex
	// provider routes those onto the runner's out-of-band Thinking
	// channel so the cockpit's thinking item can render them.
	// Override via options when a caller actually wants the heuristic
	// behaviour.
	if effort := optionString(req.Options, "reasoning_effort"); effort != "" {
		rr.Reasoning = &reasoningConfig{Effort: reasoningEffort(effort)}
		if supportsReasoningSummary(model) {
			rr.Reasoning.Summary = optionStringOr(req.Options, "reasoning_summary", "detailed")
		}
	} else if (len(suppressDefaultEffort) == 0 || !suppressDefaultEffort[0]) && defaultReasoningEffort(model) != "" {
		rr.Reasoning = &reasoningConfig{Effort: defaultReasoningEffort(model)}
		if supportsReasoningSummary(model) {
			rr.Reasoning.Summary = "detailed"
		}
	}
	if verb := optionString(req.Options, "text_verbosity"); validTextVerbosity(verb) {
		rr.Text = &textConfig{Verbosity: textVerbosity(verb)}
	}
	// Structured output rides on text.format, sharing the text block with
	// verbosity. Honors ResponseFormat so enum/verdict schemas constrain
	// Codex output rather than being silently ignored.
	if tf := responseFormatToText(req.ResponseFormat); tf != nil {
		if rr.Text == nil {
			rr.Text = &textConfig{}
		}
		rr.Text.Format = tf
	}
	if tc := optionString(req.Options, "tool_choice"); tc != "" {
		rr.ToolChoice = tc
	}
	if cacheKey := optionString(req.Options, "prompt_cache_key"); cacheKey != "" {
		rr.PromptCacheKey = cacheKey
	}
	return rr
}

// optionString reads a string option from llm.ModelOptions, treating
// missing/non-string entries as empty.
func optionString(opts llm.ModelOptions, key string) string {
	if opts == nil {
		return ""
	}
	if v, ok := opts[key].(string); ok {
		return v
	}
	return ""
}

func validTextVerbosity(verbosity string) bool {
	switch verbosity {
	case "low", "medium", "high":
		return true
	default:
		return false
	}
}

func optionStringOr(opts llm.ModelOptions, key, fallback string) string {
	if v := optionString(opts, key); v != "" {
		return v
	}
	return fallback
}

// messagesToInput translates llm.Message history into Responses API
// input items. Several shape changes happen here:
//
//   - assistant messages with ToolCalls turn into one sseTypeMessage item
//     plus one sseTypeFunctionCall item per call;
//   - tool messages turn into "function_call_output" items;
//   - user messages carry Parts (multimodal) if set, else flat Content.
//
// System messages get prepended into the request's `instructions`
// field upstream — see Provider.complete. We still accept them here
// (mapped to a user-role sseTypeMessage with input_text) for the rare case
// where a caller routes a system message through the regular history.
func messagesToInput(msgs []llm.Message) []inputItem {
	out := make([]inputItem, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case llm.RoleSystem:
			// Codex prefers instructions in the top-level field; if a
			// caller passed one mid-history we still preserve it as a
			// user-role text part to avoid losing information.
			out = append(out, inputItem{
				Type:    sseTypeMessage,
				Role:    llm.RoleUser,
				Content: []contentPart{{Type: sseTypeInputText, Text: m.Content}},
			})
		case llm.RoleUser:
			out = append(out, inputItem{
				Type:    sseTypeMessage,
				Role:    llm.RoleUser,
				Content: userContentParts(m),
			})
		case llm.RoleAssistant:
			out = append(out, assistantInputItems(m)...)
		case llm.RoleTool:
			// `output` is a required field on function_call_output even
			// when the tool returned empty content — omitempty would
			// strip it and the Codex API replies with `Missing required
			// parameter: 'input[N].output'`. Force a placeholder so the
			// tool turn round-trips cleanly even for void-returning
			// tools (or tools that only set Error and leave Content "").
			body := m.Content
			if body == "" {
				body = "(no output)"
			}
			out = append(out, inputItem{
				Type:   "function_call_output",
				CallID: m.ToolCallID,
				Output: body,
			})
		default:
			out = append(out, inputItem{
				Type:    sseTypeMessage,
				Role:    llm.RoleUser,
				Content: []contentPart{{Type: sseTypeInputText, Text: m.Content}},
			})
		}
	}
	return out
}

func assistantInputItems(m llm.Message) []inputItem {
	projected := make([]inputItem, 0, 1+len(m.ToolCalls))
	if m.Content != "" {
		projected = append(projected, inputItem{
			Type:    sseTypeMessage,
			Role:    llm.RoleAssistant,
			Content: []contentPart{{Type: "output_text", Text: m.Content}},
		})
	}
	for _, tc := range m.ToolCalls {
		projected = append(projected, inputItem{
			Type:      sseTypeFunctionCall,
			CallID:    tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}

	type indexedItem struct {
		index int
		order int
		item  inputItem
	}
	indexed := make([]indexedItem, 0, len(m.ContinuationItems)+len(projected))
	occupied := make(map[int]bool, len(m.ContinuationItems))
	for order, continuation := range m.ContinuationItems {
		item, ok := codexReasoningInput(continuation)
		if !ok {
			continue
		}
		index := order
		if continuation.OutputIndex != nil {
			index = *continuation.OutputIndex
		}
		indexed = append(indexed, indexedItem{index: index, order: order, item: item})
		occupied[index] = true
	}
	index := 0
	for order, item := range projected {
		var position *int
		if item.Type == sseTypeMessage {
			position = m.ContentOutputIndex
		} else {
			toolIndex := order
			if m.Content != "" {
				toolIndex--
			}
			if toolIndex >= 0 && toolIndex < len(m.ToolCalls) {
				position = m.ToolCalls[toolIndex].OutputIndex
			}
		}
		if position != nil {
			index = *position
		} else {
			for occupied[index] {
				index++
			}
		}
		indexed = append(indexed, indexedItem{index: index, order: len(m.ContinuationItems) + order, item: item})
		occupied[index] = true
		index++
	}
	sort.SliceStable(indexed, func(i, j int) bool {
		if indexed[i].index == indexed[j].index {
			return indexed[i].order < indexed[j].order
		}
		return indexed[i].index < indexed[j].index
	})
	out := make([]inputItem, len(indexed))
	for i := range indexed {
		out[i] = indexed[i].item
	}
	return out
}

func codexReasoningInput(continuation llm.ContinuationItem) (inputItem, bool) {
	if continuation.Provider != codexContinuationProvider || continuation.Format != codexReasoningFormat ||
		(continuation.Kind != "" && continuation.Kind != sseTypeReasoning) {
		return inputItem{}, false
	}
	var item inputItem
	if err := json.Unmarshal(continuation.Data, &item); err != nil || item.Type != sseTypeReasoning || item.ID == "" || item.EncryptedContent == "" {
		return inputItem{}, false
	}
	item.Raw = append(item.Raw[:0], continuation.Data...)
	return item, true
}

// userContentParts builds the contentPart slice for a user message,
// preferring Parts (multimodal) when set, falling back to a single
// input_text part built from Content.
func userContentParts(m llm.Message) []contentPart {
	if len(m.Parts) == 0 {
		return []contentPart{{Type: sseTypeInputText, Text: m.Content}}
	}
	parts := make([]contentPart, 0, len(m.Parts))
	for _, p := range m.Parts {
		switch p.Type {
		case llm.ContentTypeText:
			parts = append(parts, contentPart{Type: sseTypeInputText, Text: p.Text})
		case llm.ContentTypeImage:
			if p.Image == nil {
				continue
			}
			u := p.Image.DataURI
			if u == "" {
				u = p.Image.URL
			}
			if u == "" {
				continue
			}
			parts = append(parts, contentPart{Type: "input_image", ImageURL: u, Detail: p.Image.Detail})
		case llm.ContentTypeAudio:
			// Codex doesn't currently accept audio on /responses;
			// skip silently so we don't break the request body.
		case llm.ContentTypeVideo:
			if p.Video == nil {
				continue
			}
			u := p.Video.DataURI
			if u == "" {
				u = p.Video.URL
			}
			if u == "" {
				continue
			}
			parts = append(parts, contentPart{Type: "input_video", VideoURL: u})
		}
	}
	return parts
}

// toolsToResponsesTools maps Chat-Completions-shaped llm.Tool slices
// into the flat Responses API tool shape (no sseTypeFunction wrapper).
func toolsToResponsesTools(tools []llm.Tool) []responsesTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]responsesTool, 0, len(tools))
	for _, t := range tools {
		out = append(out, responsesTool{
			Type:        sseTypeFunction,
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  t.Function.ParametersMap(),
		})
	}
	return out
}

// defaultReasoningEffort returns the effort the Codex backend uses
// when one isn't specified explicitly. Pattern: -max → max,
// -spark → low (real-time iteration is the whole point), -mini →
// low (fast & cheap is what you ask mini for), other codex → medium,
// gpt-5 family (base / 5.5 / 5-pro etc) → medium so the Responses
// API actually emits reasoning_summary deltas (otherwise the server
// defaults to no summary and zarlcode's transcript stays empty of
// the thinking child the user expects to see), general-purpose →
// server default. The picker's preset ids let callers pin a
// specific effort; this just covers the bare-base path where the
// user typed "gpt-5.4-mini" with no suffix.
func defaultReasoningEffort(model string) reasoningEffort {
	switch {
	case strings.HasSuffix(model, "-max"):
		return reasoningEffortMax
	case strings.HasSuffix(model, "-spark"):
		return reasoningEffortLow
	case strings.HasSuffix(model, "-mini"):
		return reasoningEffortLow
	case strings.HasPrefix(model, "gpt-5"):
		return reasoningEffortMedium
	case strings.Contains(model, "codex"):
		return reasoningEffortMedium
	default:
		return ""
	}
}

// supportsReasoningSummary reports whether Codex accepts the reasoning.summary
// request knob. Spark models reject it with unsupported_parameter even though
// they still accept reasoning.effort and stream reasoning events.
func supportsReasoningSummary(model string) bool {
	return !strings.HasSuffix(model, "-spark")
}
