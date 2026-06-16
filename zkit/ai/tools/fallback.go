package tools

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/llm/repair"
)

// FallbackCall is a tool call extracted from text emitted by a model that
// didn't use the structured tool_calls field. Pair Name + Arguments with
// Registry.ParseCall (and a fresh call ID) to dispatch.
type FallbackCall struct {
	Name      ToolName
	Arguments ToolParameters
}

// ParseFromText extracts tool calls a model emitted as plain text instead
// of in the native structured tool_calls field. Covers the common
// "almost-tool-call" shapes small models fall back to when chat-template
// wiring is imperfect:
//
//   - <tool_call>{"name": ..., "arguments": ...}</tool_call>
//   - Gemma-4: <|tool_call>call:NAME{key:val,...}<tool_call|>
//   - ```json\n{"name": ..., "arguments": ...}\n```
//   - a bare JSON object containing both "name" and "arguments" keys
//
// The returned remaining string is the content with any matched tool-call
// fragments removed and whitespace trimmed — safe to surface to the user
// as the turn's textual reply when no native tool_calls were produced.
func ParseFromText(content string) ([]FallbackCall, string) {
	if content == "" {
		return nil, ""
	}
	remaining := content

	var calls []FallbackCall

	for _, m := range toolCallTagRe.FindAllStringSubmatchIndex(remaining, -1) {
		inner := remaining[m[2]:m[3]]
		if call, ok := decodeToolCallJSON(inner); ok {
			calls = append(calls, call)
		}
	}
	remaining = toolCallTagRe.ReplaceAllString(remaining, "")

	// Gemma-4's native tool-call dialect:
	// `<|tool_call>call:<name>{<args>}<tool_call|>`
	// llama.cpp's chat template emits this on the way out but its response
	// parser doesn't translate it back into structured tool_calls yet.
	for _, m := range gemma4ToolCallRe.FindAllStringSubmatchIndex(remaining, -1) {
		name := remaining[m[2]:m[3]]
		argBody := remaining[m[4]:m[5]]
		if call, ok := decodeGemma4ToolCall(name, argBody); ok {
			calls = append(calls, call)
		}
	}
	remaining = gemma4ToolCallRe.ReplaceAllString(remaining, "")

	for _, m := range fencedJSONRe.FindAllStringSubmatchIndex(remaining, -1) {
		inner := strings.TrimSpace(remaining[m[2]:m[3]])
		if call, ok := decodeToolCallJSON(inner); ok {
			calls = append(calls, call)
		}
	}
	remaining = fencedJSONRe.ReplaceAllString(remaining, "")

	if len(calls) == 0 {
		if call, ok := decodeBareJSONCall(remaining); ok {
			calls = append(calls, call)
			remaining = ""
		}
	}

	remaining = strings.TrimSpace(remaining)
	return calls, remaining
}

// toolCallTagRe matches <tool_call>...</tool_call> and the unclosed variant
// (<tool_call>{...} with no closing tag, running to end of string).
// Capture group 1 is the JSON body.
var toolCallTagRe = regexp.MustCompile(`(?s)<tool_call>\s*(\{.*?\})\s*(?:</tool_call>|$)`)

// fencedJSONRe matches ```json ... ``` (or bare ```) when the payload is
// an object. Capture group 1 is the JSON body.
var fencedJSONRe = regexp.MustCompile("(?s)```(?:json)?\\s*(\\{.*?\\})\\s*```")

// toolCallShape is the canonical {name, arguments} payload. Some models use
// "parameters" instead of "arguments" — both are accepted.
type toolCallShape struct {
	Name       string          `json:"name"`
	Arguments  json.RawMessage `json:"arguments"`
	Parameters json.RawMessage `json:"parameters"`
}

func decodeToolCallJSON(raw string) (FallbackCall, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw[0] != '{' {
		return FallbackCall{}, false
	}
	var s toolCallShape
	// repair.Unmarshal accepts malformed JSON from small models —
	// unescaped newlines, trailing commas, missing closers, etc. —
	// before falling back to a strict-only error. The text-extraction
	// path is the most common place to see these (the model is
	// already emitting tool-call text instead of native tool-calls,
	// so its JSON discipline is shaky to begin with).
	if err := repair.Unmarshal([]byte(raw), &s); err != nil || s.Name == "" {
		return FallbackCall{}, false
	}
	argsRaw := s.Arguments
	if len(argsRaw) == 0 {
		argsRaw = s.Parameters
	}
	args := ToolParameters{}
	if len(argsRaw) > 0 {
		// Arguments is sometimes a stringified JSON blob; try both
		// shapes through the repair cascade so a malformed stringified
		// arg blob still recovers.
		if err := repair.Unmarshal(argsRaw, &args); err != nil {
			var asStr string
			if err := json.Unmarshal(argsRaw, &asStr); err == nil && asStr != "" {
				_ = repair.Unmarshal([]byte(asStr), &args)
			}
		}
	}
	return FallbackCall{Name: ToolName(s.Name), Arguments: args}, true
}

// gemma4ToolCallRe matches Gemma-4's native tool-call emission, e.g.
// `<|tool_call>call:gesture{gesture:"index",mood:"happy"}<tool_call|>`.
// Capture group 1 is the function name, group 2 is the argument body.
var gemma4ToolCallRe = regexp.MustCompile(`(?s)<\|tool_call>call:([a-zA-Z_][a-zA-Z0-9_]*)\{(.*?)\}<tool_call\|>`)

// gemma4KeyRe matches unquoted JSON-object keys: a run of word chars
// followed by `:`, after `{` or `,`. Used to lift gemma-4's JS-style
// object literal into valid JSON.
var gemma4KeyRe = regexp.MustCompile(`([{,]\s*)([a-zA-Z_][a-zA-Z0-9_]*)(\s*:)`)

// decodeGemma4ToolCall parses Gemma-4's JS-object-literal argument syntax
// into typed Arguments. Strategy: add double-quotes around bare keys so
// the body becomes valid JSON, then let encoding/json do the heavy lifting
// (numbers, booleans, nested objects). Empty body decodes as an empty
// Arguments map — valid for zero-arg tool calls.
func decodeGemma4ToolCall(name, body string) (FallbackCall, bool) {
	if name == "" {
		return FallbackCall{}, false
	}
	body = strings.TrimSpace(body)
	args := ToolParameters{}
	if body != "" {
		// Wrap first so the leading key has `{` preceding it — the regex
		// needs `{` or `,` to identify an object-literal key position.
		// Braces around group refs are required because Go regex Expand
		// parses `$1"` as a group named `1"`, silently eating the quote.
		jsonBody := gemma4KeyRe.ReplaceAllString("{"+body+"}", `${1}"${2}"${3}`)
		if err := json.Unmarshal([]byte(jsonBody), &args); err != nil {
			return FallbackCall{}, false
		}
	}
	return FallbackCall{Name: ToolName(name), Arguments: args}, true
}

// decodeBareJSONCall tries to parse the entire trimmed content as a single
// tool-call object. Only succeeds if it has a "name" and an "arguments" or
// "parameters" key — guards against decoding stray JSON that isn't a
// tool call.
func decodeBareJSONCall(content string) (FallbackCall, bool) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" || trimmed[0] != '{' {
		return FallbackCall{}, false
	}
	if !strings.Contains(trimmed, `"name"`) {
		return FallbackCall{}, false
	}
	if !strings.Contains(trimmed, `"arguments"`) && !strings.Contains(trimmed, `"parameters"`) {
		return FallbackCall{}, false
	}
	return decodeToolCallJSON(trimmed)
}
