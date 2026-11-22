package google

import (
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"slices"
	"strings"

	"google.golang.org/genai"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func decodeBase64(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}

// convertMessages splits llm.Message slice into Gemini's expected
// shape: a single optional system instruction plus a list of
// per-turn Contents (alternating user / model roles, with tool
// calls and tool results expressed as function_call / function_response
// parts on the surrounding turn).
//
// All system messages collapse into one SystemInstruction (joined
// with blank lines) — Gemini only honours one system block.
func convertMessages(ctx context.Context, messages []llm.Message) (*genai.Content, []*genai.Content, error) {
	var sys *genai.Content
	var contents []*genai.Content
	var sysParts []string
	for messageIndex, m := range messages {
		if cause := context.Cause(ctx); cause != nil {
			return nil, nil, cause
		}
		switch m.Role {
		case llm.RoleSystem:
			if m.Content != "" {
				sysParts = append(sysParts, m.Content)
			}
		case llm.RoleUser:
			parts := []*genai.Part{}
			// The runner projects the prompt into both Content and the first
			// text part. Parts own that redundant text; Content remains the
			// fallback for ordinary text-only and nonredundant direct requests.
			if m.Content != "" && (len(m.Parts) == 0 || m.Parts[0].Type != llm.ContentTypeText || m.Parts[0].Text != m.Content) {
				parts = append(parts, &genai.Part{Text: m.Content})
			}
			for _, p := range m.Parts {
				if gp := convertPart(p); gp != nil {
					parts = append(parts, gp)
				}
			}
			if len(parts) == 0 {
				continue
			}
			contents = append(contents, &genai.Content{
				Role:  llm.RoleUser,
				Parts: parts,
			})
		case llm.RoleAssistant:
			parts, native, err := continuationParts(ctx, m)
			if err != nil {
				return nil, nil, err
			}
			if !native {
				if m.Content != "" {
					parts = append(parts, &genai.Part{Text: m.Content})
				}
				for _, tc := range m.ToolCalls {
					native, found, err := signedFunctionCallPart(ctx, m.ContinuationItems, tc)
					if err != nil {
						return nil, nil, err
					}
					if found {
						parts = append(parts, native)
						continue
					}
					args := map[string]any{}
					if tc.Function.Arguments != "" {
						_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
					}
					parts = append(parts, &genai.Part{FunctionCall: &genai.FunctionCall{
						ID: tc.ID, Name: tc.Function.Name, Args: args,
					}})
				}
			}
			if len(parts) == 0 {
				continue
			}
			contents = append(contents, &genai.Content{
				Role:  "model",
				Parts: parts,
			})
		case llm.RoleTool:
			// Tool-result messages live on the user side in Gemini's
			// turn alternation. The response goes inside FunctionResponse.
			//
			// MCP/zarlcode tool results are plain text; wrap as
			// {"result": <text>} so the schema matches what models expect.
			name, err := toolNameFromID(ctx, messages[:messageIndex], m.ToolCallID)
			if err != nil {
				return nil, nil, err
			}
			resp := map[string]any{"result": m.Content}
			part := &genai.Part{FunctionResponse: &genai.FunctionResponse{
				ID:       m.ToolCallID,
				Name:     name,
				Response: resp,
			}}
			if len(contents) > 0 && contents[len(contents)-1].Role == llm.RoleUser {
				contents[len(contents)-1].Parts = append(contents[len(contents)-1].Parts, part)
				continue
			}
			contents = append(contents, &genai.Content{
				Role:  llm.RoleUser,
				Parts: []*genai.Part{part},
			})
		}
	}

	if len(sysParts) > 0 {
		sys = &genai.Content{
			Role:  llm.RoleUser,
			Parts: []*genai.Part{{Text: strings.Join(sysParts, "\n\n")}},
		}
	}
	return sys, contents, nil
}

// continuationParts decodes v2's complete replay projection. Supplied display
// text and reasoning must agree with the native projection; absent display
// fields permit native-only histories. Native parts are never repaired.
func continuationParts(ctx context.Context, message llm.Message) ([]*genai.Part, bool, error) {
	var items []llm.ContinuationItem
	legacy := false
	legacyIDs := make(map[string]struct{})
	for _, item := range message.ContinuationItems {
		if cause := context.Cause(ctx); cause != nil {
			return nil, false, cause
		}
		if item.Provider != continuationProvider {
			continue
		}
		switch item.Format {
		case continuationFormat:
			items = append(items, item)
		case legacyContinuationFormat:
			legacy = true
			if _, err := decodeLegacyPart(ctx, item); err != nil {
				return nil, false, err
			}
			if _, exists := legacyIDs[item.ID]; exists {
				return nil, false, errors.New("google: ambiguous legacy continuation function-call ID")
			}
			legacyIDs[item.ID] = struct{}{}
		}
	}
	if len(items) == 0 {
		if legacy {
			seen := make(map[string]struct{}, len(message.ToolCalls))
			for _, call := range message.ToolCalls {
				if _, exists := seen[call.ID]; exists {
					return nil, false, errors.New("google: ambiguous display tool-call ID")
				}
				seen[call.ID] = struct{}{}
				delete(legacyIDs, call.ID)
			}
			if len(legacyIDs) != 0 {
				return nil, false, errors.New("google: legacy continuation has no matching display tool call")
			}
		}
		return nil, false, nil
	}
	if legacy {
		return nil, false, errors.New("google: mixed legacy and complete continuation formats")
	}
	for _, item := range items {
		if item.OutputIndex == nil || *item.OutputIndex < 0 {
			return nil, false, errors.New("google: continuation part requires a nonnegative output index")
		}
	}
	// Sort our own slice, never the borrowed message's continuation items.
	slices.SortStableFunc(items, func(a, b llm.ContinuationItem) int {
		return cmp.Compare(*a.OutputIndex, *b.OutputIndex)
	})
	parts := make([]*genai.Part, 0, len(items))
	type nativeCall struct {
		function    genai.FunctionCall
		outputIndex int
	}
	calls := make(map[string]nativeCall)
	var content, reasoning strings.Builder
	for index, item := range items {
		if index > 0 && *items[index-1].OutputIndex == *item.OutputIndex {
			return nil, false, fmt.Errorf("google: duplicate continuation output index %d", *item.OutputIndex)
		}
		part, err := decodeContinuationPart(ctx, item)
		if err != nil {
			return nil, false, err
		}
		if part.FunctionCall != nil {
			id := item.ID
			if id == "" {
				id = part.FunctionCall.ID
			}
			if _, exists := calls[id]; exists {
				return nil, false, errors.New("google: ambiguous native continuation function-call ID")
			}
			calls[id] = nativeCall{function: *part.FunctionCall, outputIndex: *item.OutputIndex}
		}
		if part.Thought {
			reasoning.WriteString(part.Text)
		} else {
			content.WriteString(part.Text)
		}
		parts = append(parts, part)
	}
	if (message.Content != "" && message.Content != content.String()) ||
		(message.ReasoningContent != "" && message.ReasoningContent != reasoning.String()) {
		return nil, false, errors.New("google: display text does not match native continuation")
	}
	seenCalls := make(map[string]struct{}, len(message.ToolCalls))
	for _, call := range message.ToolCalls {
		if _, exists := seenCalls[call.ID]; exists {
			return nil, false, errors.New("google: ambiguous display tool-call ID")
		}
		seenCalls[call.ID] = struct{}{}
		native, exists := calls[call.ID]
		if !exists || native.function.Name != call.Function.Name ||
			(call.OutputIndex != nil && *call.OutputIndex != native.outputIndex) {
			return nil, false, errors.New("google: tool call does not match native continuation")
		}
		if err := validateCallArguments(ctx, call, native.function); err != nil {
			return nil, false, err
		}
	}
	return parts, true, nil
}

// validateCallArguments compares the display projection to the signed argument
// object without replacing native data. Only root parameterless representations
// (empty string, null, omitted args and {}) are interchangeable.
func validateCallArguments(ctx context.Context, call llm.ToolCall, function genai.FunctionCall) error {
	var args map[string]any
	if call.Function.Arguments != "" {
		if err := decodeReplayJSON([]byte(call.Function.Arguments), &args); err != nil {
			return fmt.Errorf("google: decode tool-call arguments: %w", err)
		}
	}
	if (len(args) != 0 || len(function.Args) != 0) && !equalReplayJSON(ctx, args, function.Args) {
		if cause := context.Cause(ctx); cause != nil {
			return cause
		}
		return errors.New("google: tool-call arguments do not match native continuation")
	}
	return nil
}

// decodeReplayJSON preserves both json.Unmarshal's strict syntax errors and
// exact numbers in open SDK fields, rather than rounding them through float64.
func decodeReplayJSON(data []byte, target any) error {
	var raw json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	return decoder.Decode(target)
}

// equalReplayJSON compares parsed JSON without changing the signed native data.
// Decimal and exponent spellings compare by exact value, including large integers.
func equalReplayJSON(ctx context.Context, left, right any) bool {
	if context.Cause(ctx) != nil {
		return false
	}
	switch value := left.(type) {
	case json.Number:
		other, ok := right.(json.Number)
		if !ok {
			return false
		}
		return equalReplayNumber(value, other)
	case map[string]any:
		other, ok := right.(map[string]any)
		if !ok || len(value) != len(other) {
			return false
		}
		for key, entry := range value {
			otherEntry, exists := other[key]
			if !exists || !equalReplayJSON(ctx, entry, otherEntry) {
				return false
			}
		}
		return true
	case []any:
		other, ok := right.([]any)
		if !ok || len(value) != len(other) {
			return false
		}
		for index, entry := range value {
			if !equalReplayJSON(ctx, entry, other[index]) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(left, right)
	}
}

// equalReplayNumber compares normalized decimal coefficients and exponents.
// The bigint stores only the exponent itself, never a power of ten: compact
// huge exponents therefore require space proportional to their spelling, not
// their value. Inputs are already validated JSON numbers.
func equalReplayNumber(left, right json.Number) bool {
	a, aExponent := replayDecimal(left)
	b, bExponent := replayDecimal(right)
	return a == b && aExponent.Cmp(&bExponent) == 0
}

func replayDecimal(number json.Number) (string, big.Int) {
	text := string(number)
	negative := strings.HasPrefix(text, "-")
	text = strings.TrimPrefix(text, "-")
	mantissa, exponentText := text, "0"
	if index := strings.IndexAny(text, "eE"); index >= 0 {
		mantissa, exponentText = text[:index], text[index+1:]
	}
	fractionDigits := 0
	if index := strings.IndexByte(mantissa, '.'); index >= 0 {
		fractionDigits = len(mantissa) - index - 1
	}
	coefficient := strings.TrimLeft(strings.ReplaceAll(mantissa, ".", ""), "0")
	var exponent big.Int
	if coefficient == "" {
		return "0", exponent
	}
	trimmed := strings.TrimRight(coefficient, "0")
	offset := big.NewInt(int64(len(coefficient) - len(trimmed) - fractionDigits))
	exponent.SetString(exponentText, 10)
	exponent.Add(&exponent, offset)
	if negative {
		trimmed = "-" + trimmed
	}
	return trimmed, exponent
}

func validateReplayNumbers(ctx context.Context, raw any) error {
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	switch value := raw.(type) {
	case json.Number:
		// The request-local body hook restores exact numbers after SDK conversion,
		// including rounding and underflow. Overflow still makes the SDK's initial
		// float64 remarshal fail before that hook can run; reject it before dispatch.
		if _, err := value.Float64(); err != nil {
			return errors.New("google: continuation number cannot encode through SDK JSON")
		}
	case map[string]any:
		for _, entry := range value {
			if err := validateReplayNumbers(ctx, entry); err != nil {
				return err
			}
		}
	case []any:
		for _, entry := range value {
			if err := validateReplayNumbers(ctx, entry); err != nil {
				return err
			}
		}
	}
	return nil
}

func decodeContinuationPart(ctx context.Context, item llm.ContinuationItem) (*genai.Part, error) {
	var raw any
	if err := decodeReplayJSON(item.Data, &raw); err != nil {
		return nil, fmt.Errorf("google: decode continuation part: %w", err)
	}
	if err := validateReplayNumbers(ctx, raw); err != nil {
		return nil, err
	}

	var part genai.Part
	if err := decodeReplayJSON(item.Data, &part); err != nil {
		return nil, fmt.Errorf("google: decode continuation part: %w", err)
	}
	// Null, empty objects and unknown-only payloads have no replayable SDK data.
	data, err := json.Marshal(part)
	if err != nil {
		return nil, fmt.Errorf("google: encode continuation part: %w", err)
	}
	if string(data) == "{}" || (part.FunctionCall != nil && part.FunctionCall.Name == "") {
		return nil, errors.New("google: continuation part has no replayable content")
	}
	if part.FunctionCall != nil && item.ID != "" && part.FunctionCall.ID != "" && item.ID != part.FunctionCall.ID {
		return nil, errors.New("google: continuation function-call ID does not match native data")
	}
	return &part, nil
}

func decodeLegacyPart(ctx context.Context, item llm.ContinuationItem) (*genai.Part, error) {
	part, err := decodeContinuationPart(ctx, item)
	if err != nil {
		return nil, err
	}
	if part.FunctionCall == nil || len(part.ThoughtSignature) == 0 {
		return nil, errors.New("google: legacy continuation requires a signed function call")
	}
	return part, nil
}

func signedFunctionCallPart(ctx context.Context, items []llm.ContinuationItem, call llm.ToolCall) (*genai.Part, bool, error) {
	for _, item := range items {
		if item.Provider != continuationProvider || item.Format != legacyContinuationFormat || item.ID != call.ID {
			continue
		}
		part, err := decodeLegacyPart(ctx, item)
		if err != nil {
			return nil, false, err
		}
		if part.FunctionCall.Name != call.Function.Name ||
			(item.OutputIndex != nil && call.OutputIndex != nil && *item.OutputIndex != *call.OutputIndex) {
			return nil, false, errors.New("google: tool call does not match legacy continuation")
		}
		if err := validateCallArguments(ctx, call, *part.FunctionCall); err != nil {
			return nil, false, err
		}
		return part, true, nil
	}
	return nil, false, nil
}

// toolNameFromID walks back through messages to find the assistant
// tool-call whose ID matches — Gemini's FunctionResponse requires the
// Name (not just ID) to bind the response to the call.
func toolNameFromID(ctx context.Context, messages []llm.Message, id string) (string, error) {
	// Only preceding messages are supplied, so later reuse cannot change an
	// earlier response. The nearest assistant binding wins across turns.
	for index := len(messages) - 1; index >= 0; index-- {
		if cause := context.Cause(ctx); cause != nil {
			return "", cause
		}
		message := messages[index]
		if message.Role != llm.RoleAssistant {
			continue
		}
		for _, call := range message.ToolCalls {
			if call.ID == id {
				return call.Function.Name, nil
			}
		}
		for _, item := range message.ContinuationItems {
			if item.Provider != continuationProvider ||
				(item.Format != continuationFormat && item.Format != legacyContinuationFormat) {
				continue
			}
			part, err := decodeContinuationPart(ctx, item)
			if err != nil {
				return "", err
			}
			if part.FunctionCall == nil {
				continue
			}
			routingID := item.ID
			if routingID == "" {
				routingID = part.FunctionCall.ID
			}
			if routingID == id {
				return part.FunctionCall.Name, nil
			}
		}
	}
	return "", nil
}

func validateMessages(messages []llm.Message) error {
	for _, message := range messages {
		for _, item := range message.ContinuationItems {
			if item.Provider == continuationProvider && item.Format == interactionsContinuationFormat {
				return interactionsUnsupported()
			}
		}
		for _, part := range message.Parts {
			switch part.Type {
			case llm.ContentTypeText:
				continue
			case llm.ContentTypeImage:
				if message.Role != llm.RoleUser {
					return fmt.Errorf("google: %w: media input is supported only in user messages", llm.ErrModelNotSupported)
				}
				if part.Image == nil || !validInlineMedia(part.Image.DataURI, part.Image.MIMEType, "image/") {
					return fmt.Errorf("google: %w: image input requires a valid inline data URI", llm.ErrModelNotSupported)
				}
			case llm.ContentTypeVideo:
				if message.Role != llm.RoleUser {
					return fmt.Errorf("google: %w: media input is supported only in user messages", llm.ErrModelNotSupported)
				}
				if part.Video == nil || !validInlineMedia(part.Video.DataURI, part.Video.MIMEType, "video/") {
					return fmt.Errorf("google: %w: video input requires a valid inline data URI", llm.ErrModelNotSupported)
				}
			default:
				return fmt.Errorf("google: %w: unsupported input part", llm.ErrModelNotSupported)
			}
		}
	}
	return nil
}

func validInlineMedia(dataURI, mimeType, mediaPrefix string) bool {
	header, _, ok := strings.Cut(dataURI, ";base64,")
	return ok && strings.TrimPrefix(header, "data:") == mimeType &&
		strings.HasPrefix(mimeType, mediaPrefix) && len(dataURIBytes(dataURI)) > 0
}

// convertPart maps an llm.ContentPart to a genai.Part. Unknown /
// empty parts return nil so the caller can skip them.
func convertPart(p llm.ContentPart) *genai.Part {
	switch p.Type {
	case llm.ContentTypeText:
		if p.Text == "" {
			return nil
		}
		return &genai.Part{Text: p.Text}
	case llm.ContentTypeImage:
		if p.Image == nil {
			return nil
		}
		// Inline base64 image. Gemini wants InlineData, not a URL.
		mime := p.Image.MIMEType
		data := dataURIBytes(p.Image.DataURI)
		if data == nil {
			return nil
		}
		return &genai.Part{
			InlineData: &genai.Blob{Data: data, MIMEType: mime},
		}
	case llm.ContentTypeVideo:
		if p.Video == nil {
			return nil
		}
		mime := p.Video.MIMEType
		data := dataURIBytes(p.Video.DataURI)
		if data == nil {
			return nil
		}
		return &genai.Part{
			InlineData: &genai.Blob{Data: data, MIMEType: mime},
		}
	}
	return nil
}

// dataURIBytes decodes the base64 payload of a data: URI. Returns nil
// when the URI doesn't match the expected shape.
func dataURIBytes(uri string) []byte {
	const sep = ";base64,"
	if !strings.HasPrefix(uri, "data:") {
		return nil
	}
	_, encoded, ok := strings.Cut(uri, sep)
	if !ok || encoded == "" {
		return nil
	}
	out, err := decodeBase64(encoded)
	if err != nil || len(out) == 0 {
		return nil
	}
	return out
}

// convertTools wraps every llm.Tool as a single genai.Tool with one
// FunctionDeclaration each. Gemini accepts multiple FunctionDeclarations
// inside one Tool — flattening them keeps the wire format minimal.
func convertTools(tools []llm.Tool) []*genai.Tool {
	if len(tools) == 0 {
		return nil
	}
	decls := make([]*genai.FunctionDeclaration, 0, len(tools))
	for _, t := range tools {
		decl := &genai.FunctionDeclaration{
			Name:        t.Function.Name,
			Description: t.Function.Description,
		}
		if !t.Function.Parameters.IsZero() {
			// The raw JSON Schema field preserves constraints and enum value types
			// that cannot be represented by the SDK's OpenAPI Schema type.
			decl.ParametersJsonSchema = t.Function.Parameters.Map()
		}
		decls = append(decls, decl)
	}
	return []*genai.Tool{{FunctionDeclarations: decls}}
}
