package rewind

import (
	"bytes"
	"encoding/json"
	"reflect"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func googleRoute(provider string) bool {
	return provider == providerGemini || provider == providerGoogleVertex
}

// The saved route and the native format owner are deliberately distinct.
// Only the signed function-call shape consumed by the adapter is admitted.
type googleNativePart struct {
	FunctionCall struct {
		ID   string          `json:"id,omitempty"`
		Name string          `json:"name"`
		Args json.RawMessage `json:"args,omitempty"`
	} `json:"functionCall"`
	ThoughtSignature []byte `json:"thoughtSignature"`
}

func googleContinuationValid(item llm.ContinuationItem) bool {
	if item.Provider != "google" || item.Format != "generate_content.part.v1" || item.Kind != "function_call" || item.ID == "" || item.OutputIndex == nil || *item.OutputIndex < 0 {
		return false
	}
	var part googleNativePart
	return strictJSON(item.Data, &part) && len(part.ThoughtSignature) != 0 && part.FunctionCall.Name != "" &&
		(part.FunctionCall.ID == "" || part.FunctionCall.ID == item.ID) &&
		(len(part.FunctionCall.Args) == 0 || bytes.Equal(part.FunctionCall.Args, []byte("null")) || jsonObject(part.FunctionCall.Args))
}

func jsonObject(raw []byte) bool {
	var fields map[string]json.RawMessage
	return json.Unmarshal(raw, &fields) == nil && fields != nil
}

func googleArgumentsValid(raw []byte) bool {
	return uniqueJSONText(raw) && (bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || jsonObject(raw))
}

// Decode numbers without float64 rounding: signed arguments must not silently
// disagree at the integer precision boundary. Object key order is immaterial.
func googleArgumentsMatch(native, projected []byte) bool {
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
	return reflect.DeepEqual(decode(native), decode(projected))
}

func googleOutputsPaired(message llm.Message) bool {
	for _, call := range message.ToolCalls {
		if !googleArgumentsValid([]byte(call.Function.Arguments)) {
			return false
		}
	}
	seen := make(map[string]bool, len(message.ContinuationItems))
	for _, item := range message.ContinuationItems {
		if !googleContinuationValid(item) || seen[item.ID] {
			return false
		}
		seen[item.ID] = true
		var part googleNativePart
		_ = json.Unmarshal(item.Data, &part)
		matched := false
		for _, call := range message.ToolCalls {
			if call.ID != item.ID {
				continue
			}
			if matched || call.OutputIndex == nil || *call.OutputIndex != *item.OutputIndex || call.Function.Name != part.FunctionCall.Name ||
				!googleArgumentsMatch(part.FunctionCall.Args, []byte(call.Function.Arguments)) {
				return false
			}
			matched = true
		}
		if !matched {
			return false
		}
	}
	return true
}
