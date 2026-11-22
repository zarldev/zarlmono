package rewind

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"reflect"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

const googleCompletePartFormat = "generate_content.part.v2"

// v2 owns the complete output, unlike v1's signed call supplement. Keep this
// ordinary-history contract separate from exact provider checkpoint admission.
func savedProviderMessageValid(message llm.Message, provider string) bool {
	if googleRoute(provider) {
		for _, item := range message.ContinuationItems {
			if item.Format == googleCompletePartFormat {
				return googleCompleteMessageValid(message)
			}
		}
	}
	return providerMessageValid(message, provider)
}

func savedGoogleContinuationValid(item llm.ContinuationItem) bool {
	if item.Format != googleCompletePartFormat {
		return googleContinuationValid(item)
	}
	_, valid := googleCompletePart(item)
	return valid
}

// Deliberately closed: media and open SDK metadata need their own verified
// replay contract. An adapter's ability to decode them is not admission proof.
type googleCompleteNativePart struct {
	Text             string          `json:"text,omitempty"`
	Thought          bool            `json:"thought,omitempty"`
	ThoughtSignature string          `json:"thoughtSignature,omitempty"`
	FunctionCall     json.RawMessage `json:"functionCall,omitempty"`
}

type googleCompleteFunction struct {
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

func googleCompletePart(item llm.ContinuationItem) (googleCompleteNativePart, bool) {
	var part googleCompleteNativePart
	if item.Provider != "google" || item.Format != googleCompletePartFormat || item.Kind != "part" ||
		item.OutputIndex == nil || *item.OutputIndex < 0 || !strictJSON(item.Data, &part) {
		return part, false
	}
	if part.ThoughtSignature != "" {
		signature, err := base64.StdEncoding.DecodeString(part.ThoughtSignature)
		if err != nil || len(signature) == 0 {
			return part, false
		}
	}
	if len(part.FunctionCall) == 0 {
		return part, item.ID == "" && (part.Text != "" || part.ThoughtSignature != "")
	}
	var call googleCompleteFunction
	return part, part.Text == "" && !part.Thought && item.ID != "" &&
		strictJSON(part.FunctionCall, &call) && call.Name != "" && (call.ID == "" || call.ID == item.ID) &&
		(len(call.Args) == 0 || googleArgumentsValid(call.Args))
}

func googleCompleteMessageValid(message llm.Message) bool {
	if message.Role != llm.RoleAssistant {
		return false
	}
	var content, reasoning strings.Builder
	firstText, previous, callIndex := -1, -1, 0
	seenCalls := make(map[string]bool, len(message.ToolCalls))
	for _, item := range message.ContinuationItems {
		part, valid := googleCompletePart(item)
		if !valid || *item.OutputIndex <= previous {
			return false
		}
		previous = *item.OutputIndex
		if part.Thought {
			reasoning.WriteString(part.Text)
		} else {
			content.WriteString(part.Text)
			if firstText < 0 && part.Text != "" {
				firstText = previous
			}
		}
		if len(part.FunctionCall) == 0 {
			continue
		}
		if callIndex == len(message.ToolCalls) || seenCalls[item.ID] {
			return false
		}
		seenCalls[item.ID] = true
		var native googleCompleteFunction
		_ = json.Unmarshal(part.FunctionCall, &native) // The closed shape was checked above.
		call := message.ToolCalls[callIndex]
		if call.ID != item.ID || call.Type != "function" || call.Function.Name != native.Name ||
			call.OutputIndex == nil || *call.OutputIndex != previous ||
			!googleCompleteArgumentsMatch(native.Args, []byte(call.Function.Arguments)) {
			return false
		}
		callIndex++
	}
	// Missing display text is allowed by the native adapter; conflicting text
	// is not. Every native call must have a distinct declared tool binding so
	// ordinary history's tool-result ordering remains enforceable.
	return callIndex == len(message.ToolCalls) &&
		(message.Content == "" || message.Content == content.String()) &&
		(message.ReasoningContent == "" || message.ReasoningContent == reasoning.String()) &&
		(message.ContentOutputIndex == nil || (message.Content != "" && *message.ContentOutputIndex == firstText))
}

func googleCompleteArgumentsMatch(native, projected []byte) bool {
	if !googleArgumentsValid(projected) {
		return false
	}
	decode := func(raw []byte) any {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		_ = decoder.Decode(&value)
		return value
	}
	// Only parameterless root objects/null/omitted args are equivalent. Never
	// normalize nested nulls, arrays, scalar values or the retained wire bytes.
	left, right := decode(native), decode(projected)
	parameterless := func(value any) bool {
		if value == nil {
			return true
		}
		object, ok := value.(map[string]any)
		return ok && len(object) == 0
	}
	return (parameterless(left) && parameterless(right)) || googleExactJSONEqual(left, right)
}

func googleExactJSONEqual(left, right any) bool {
	switch value := left.(type) {
	case json.Number:
		other, ok := right.(json.Number)
		if !ok {
			return false
		}
		a, aExponent := googleDecimal(value)
		b, bExponent := googleDecimal(other)
		return a == b && aExponent.Cmp(&bExponent) == 0
	case map[string]any:
		other, ok := right.(map[string]any)
		if !ok || len(value) != len(other) {
			return false
		}
		for key, entry := range value {
			otherEntry, exists := other[key]
			if !exists || !googleExactJSONEqual(entry, otherEntry) {
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
			if !googleExactJSONEqual(entry, other[index]) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(left, right)
	}
}

// Compare exact decimal coefficients and exponents without float64 rounding or
// constructing powers of ten. Space depends on spelling length, never exponent
// magnitude (including tiny numbers that underflow the SDK's initial decode).
func googleDecimal(number json.Number) (string, big.Int) {
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
