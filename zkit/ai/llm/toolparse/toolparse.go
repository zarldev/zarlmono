// Package toolparse recovers structured tool calls from assistant text
// when the provider's own structured-output path produced none — the
// model emitted a known protocol artifact (tagged block, JSON object,
// bare array) as plain text instead of the wire-format tool_calls field.
//
// This is the shared pipeline that replaces the one-off parsers previously
// embedded inside individual providers. Every provider that supports tool
// calls should call ParseArtifact as a fallback before presenting the
// assistant's text to the runner.
package toolparse

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// toolCallTypeFunction is the OpenAI-style tool-call discriminator.
const toolCallTypeFunction = "function"

const jsonNull = "null"

// Tag constants the model copies from the prompt's rendered history
// or from the protocol directive itself.
const (
	assistantToolCallsOpen  = "<assistant_tool_calls>"
	assistantToolCallsClose = "</assistant_tool_calls>"
	ToolCallsOpen           = "<tool_calls>"
	ToolCallsClose          = "</tool_calls>"
)

// Shape identifies which protocol-artifact shape was matched.
type Shape string

const (
	ShapeNone                 Shape = ""
	ShapeTaggedAssistantCalls Shape = "tagged_assistant_tool_calls"
	ShapeTaggedToolCalls      Shape = "tagged_tool_calls"
	ShapeProtocolObject       Shape = "protocol_object"
	ShapeBareNestedArray      Shape = "bare_nested_function_array"
	ShapeBareFlatArray        Shape = "bare_flat_array"
	ShapeRecoveredToolCalls   Shape = "recovered_tool_calls"
)

// Result carries the outcome of a ParseArtifact call.
type Result struct {
	// Calls is the extracted tool calls, if any.
	Calls []llm.ToolCall
	// RemainingContent is the input content with the artifact stripped,
	// leaving any prose that surrounded it.
	RemainingContent string
	// Shape identifies which artifact shape was matched.
	Shape Shape
	// HighConfidence means the parsed result is trustworthy enough to
	// promote into executable ToolCalls. Low-confidence results should
	// leave the content as-is.
	HighConfidence bool
}

// IsToolCallArtifactPrefix returns true when the content begins with a
// known tool-call artifact prefix, useful for streaming decisions.
func IsToolCallArtifactPrefix(content string) bool {
	c := strings.TrimLeft(content, " \t\r\n")
	return strings.HasPrefix(c, assistantToolCallsOpen) ||
		strings.HasPrefix(c, ToolCallsOpen) ||
		strings.HasPrefix(c, `{"tool_calls"`) ||
		(strings.HasPrefix(c, `[`) && (strings.Contains(c, `"id"`) || strings.Contains(c, `"name"`) || strings.Contains(c, `"function"`)))
}

// ParseArtifact attempts to recover tool calls from a piece of assistant
// content that should have arrived as wire-format tool_calls but was
// emitted as text instead.
func ParseArtifact(content string) Result {
	content = strings.TrimSpace(content)
	if content == "" {
		return Result{Shape: ShapeNone}
	}

	// 1. Tagged blocks: the model copies the <assistant_tool_calls> or
	//    <tool_calls> framing from the prompt's rendered history.
	if calls, remaining := parseTaggedToolCallBlocks(content); len(calls) > 0 {
		// Default to the assistant shape; flip only when the tool_calls tag
		// is the one actually present.
		shape := ShapeTaggedAssistantCalls
		if !strings.Contains(content, assistantToolCallsOpen) && strings.Contains(content, ToolCallsOpen) {
			shape = ShapeTaggedToolCalls
		}
		return Result{
			Calls:            assignCallIDs(calls),
			RemainingContent: remaining,
			Shape:            shape,
			HighConfidence:   true,
		}
	}

	// 2. A JSON object with a "tool_calls" key.
	if calls, found := parseProtocolObject(stripJSONFence(content)); found {
		return Result{
			Calls:            assignCallIDs(calls),
			RemainingContent: "",
			Shape:            ShapeProtocolObject,
			HighConfidence:   true,
		}
	}

	// 3. The model sometimes emits a preamble ("Let me read the files")
	//    before the tool_calls object. Scan for JSON objects containing
	//    "tool_calls" anywhere in the text.
	for _, candidate := range jsonCandidatesContainingToolCalls(content) {
		if calls, found := parseProtocolObject(candidate); found {
			remaining := strings.Replace(content, candidate, "", 1)
			remaining = strings.TrimSpace(remaining)
			return Result{
				Calls:            assignCallIDs(calls),
				RemainingContent: remaining,
				Shape:            ShapeProtocolObject,
				HighConfidence:   true,
			}
		}
	}

	// 4. Last resort: a bare array emitted without wrapping tags, in
	//    either the nested-function OpenAI shape or the documented flat shape.
	if calls := parseNestedFunctionArray(stripJSONFence(content)); len(calls) > 0 {
		return Result{
			Calls:            assignCallIDs(calls),
			RemainingContent: "",
			Shape:            ShapeBareNestedArray,
			HighConfidence:   true,
		}
	}
	if calls := parseFlatFunctionArray(stripJSONFence(content)); len(calls) > 0 {
		return Result{
			Calls:            assignCallIDs(calls),
			RemainingContent: "",
			Shape:            ShapeBareFlatArray,
			HighConfidence:   true,
		}
	}

	// 5. Loose recovery: a "tool_calls" protocol object whose outer delimiters
	//    the model malformed — a misplaced or missing bracket (e.g. the closing
	//    "}]}}" for a correct "}}]}") that makes strict json.Unmarshal reject the
	//    whole document even though each call's "name"/"arguments" pair is itself
	//    intact. Without this the slightly-broken call leaks back as visible text.
	if calls := recoverLooseToolCalls(stripJSONFence(content)); len(calls) > 0 {
		return Result{
			Calls:            assignCallIDs(calls),
			RemainingContent: "",
			Shape:            ShapeRecoveredToolCalls,
			HighConfidence:   true,
		}
	}

	return Result{Shape: ShapeNone}
}

// --- tagged block parsing ---

func parseTaggedToolCallBlocks(text string) ([]llm.ToolCall, string) {
	var calls []llm.ToolCall
	remaining := text

	// Try <assistant_tool_calls> first, then <tool_calls>
	remaining = extractTagged(remaining, assistantToolCallsOpen, assistantToolCallsClose, &calls)
	remaining = extractTagged(remaining, ToolCallsOpen, ToolCallsClose, &calls)

	return calls, strings.TrimSpace(remaining)
}

func extractTagged(text, open, closeTag string, calls *[]llm.ToolCall) string {
	for {
		start := strings.Index(text, open)
		if start < 0 {
			return text
		}
		rest := text[start+len(open):]
		end := strings.Index(rest, closeTag)
		if end < 0 {
			return text
		}
		block := stripJSONFence(rest[:end])
		if blockCalls := parseNestedFunctionArray(block); len(blockCalls) > 0 {
			*calls = append(*calls, blockCalls...)
		} else if blockCalls := parseFlatFunctionArray(block); len(blockCalls) > 0 {
			*calls = append(*calls, blockCalls...)
		}
		// Continue after the close tag to handle multiple blocks
		text = rest[end+len(closeTag):]
	}
}

// --- protocol object {"tool_calls": [...]} ---

type rawToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func parseProtocolObject(text string) ([]llm.ToolCall, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, false
	}
	var payload struct {
		ToolCalls []rawToolCall `json:"tool_calls"`
	}
	if err := json.Unmarshal([]byte(text), &payload); err != nil || len(payload.ToolCalls) == 0 {
		return nil, false
	}
	out := make([]llm.ToolCall, 0, len(payload.ToolCalls))
	for _, c := range payload.ToolCalls {
		if c.Name == "" {
			continue
		}
		out = append(out, llm.ToolCall{
			ID:   c.ID,
			Type: toolCallTypeFunction,
			Function: llm.ToolCallFunction{
				Name:      c.Name,
				Arguments: normalizeArguments(c.Arguments),
			},
		})
	}
	return out, len(out) > 0
}

func jsonCandidatesContainingToolCalls(text string) []string {
	var out []string
	for i := 0; i < len(text); i++ {
		if text[i] != '{' {
			continue
		}
		end := jsonObjectEnd(text, i)
		if end < 0 {
			continue
		}
		candidate := text[i:end]
		if strings.Contains(candidate, `"tool_calls"`) {
			out = append(out, candidate)
		}
		i = end - 1
	}
	return out
}

func jsonObjectEnd(text string, start int) int {
	if start < 0 || start >= len(text) || text[start] != '{' {
		return -1
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(text); i++ {
		c := text[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch c {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return -1
}

// --- loose recovery of a malformed protocol object ---

// recoverLooseToolCalls rebuilds tool calls from a "tool_calls" protocol object
// whose outer delimiters the model malformed badly enough that every strict
// json.Unmarshal above rejected it. It only engages when the text still carries
// the "tool_calls" marker, then walks the document keying on the "id" / "name" /
// "arguments" fields — extracting each "arguments" value with a balanced-brace
// scan rather than trusting the wrapper's closing brackets. A misplaced or
// missing top-level "}" or "]" therefore no longer discards an otherwise intact
// call. Because it skips over each arguments value whole, "name" keys nested
// inside a call's arguments don't spuriously start a new call.
func recoverLooseToolCalls(content string) []llm.ToolCall {
	if !strings.Contains(content, `"tool_calls"`) {
		return nil
	}
	var calls []llm.ToolCall
	var id, name string
	for i := 0; i < len(content); {
		if content[i] != '"' {
			i++
			continue
		}
		raw, strEnd := scanJSONString(content, i)
		if strEnd < 0 {
			i++
			continue
		}
		// A string is a key only when the next non-space byte is ':'. Otherwise
		// it is a value — skip past it whole so its bytes can't be re-scanned.
		colon := skipSpace(content, strEnd)
		if colon >= len(content) || content[colon] != ':' {
			i = strEnd
			continue
		}
		valPos := skipSpace(content, colon+1)
		switch jsonStringValue(raw) {
		case "id":
			v, end := scanJSONValueString(content, valPos)
			if end < 0 {
				i = strEnd
				continue
			}
			id = jsonStringValue(v)
			i = end
		case "name":
			v, end := scanJSONValueString(content, valPos)
			if end < 0 {
				i = strEnd
				continue
			}
			name = jsonStringValue(v)
			i = end
		case "arguments":
			args, end := scanJSONValue(content, valPos)
			if end < 0 {
				i = strEnd
				continue
			}
			if name != "" {
				calls = append(calls, llm.ToolCall{
					ID:   id,
					Type: toolCallTypeFunction,
					Function: llm.ToolCallFunction{
						Name:      name,
						Arguments: normalizeArguments(json.RawMessage(args)),
					},
				})
			}
			id, name = "", ""
			i = end
		default:
			i = strEnd
		}
	}
	return calls
}

// scanJSONValueString reads a JSON string value starting at pos (which must be
// the opening quote), returning the raw quoted slice and the index after the
// closing quote, or ("", -1) when pos is not a string.
func scanJSONValueString(text string, pos int) (string, int) {
	if pos >= len(text) || text[pos] != '"' {
		return "", -1
	}
	return scanJSONString(text, pos)
}

// scanJSONValue reads one JSON value (object, array, string, or primitive)
// starting at pos, returning the raw slice and the index just past it. Objects
// and arrays use a balanced scan so a value extends to its true close regardless
// of any malformed delimiter that follows it.
func scanJSONValue(text string, pos int) (string, int) {
	if pos >= len(text) {
		return "", -1
	}
	switch text[pos] {
	case '{', '[':
		end := jsonSpanEnd(text, pos)
		if end < 0 {
			return "", -1
		}
		return text[pos:end], end
	case '"':
		return scanJSONString(text, pos)
	default:
		j := pos
		for j < len(text) && !strings.ContainsRune(",}] \t\r\n", rune(text[j])) {
			j++
		}
		if j == pos {
			return "", -1
		}
		return text[pos:j], j
	}
}

// scanJSONString reads a quoted JSON string starting at the opening quote,
// honoring backslash escapes, and returns the raw slice (quotes included) plus
// the index just past the closing quote, or ("", -1) when unterminated.
func scanJSONString(text string, start int) (string, int) {
	if start >= len(text) || text[start] != '"' {
		return "", -1
	}
	escaped := false
	for i := start + 1; i < len(text); i++ {
		if escaped {
			escaped = false
			continue
		}
		switch text[i] {
		case '\\':
			escaped = true
		case '"':
			return text[start : i+1], i + 1
		}
	}
	return "", -1
}

// jsonSpanEnd returns the index just past the object or array that opens at
// start, tracking nesting of the same delimiter and skipping string contents.
// Like jsonObjectEnd it counts only the opening delimiter's pair, which is safe
// because the other bracket type stays balanced inside a well-formed value.
func jsonSpanEnd(text string, start int) int {
	if start < 0 || start >= len(text) {
		return -1
	}
	var open, closeCh byte
	switch text[start] {
	case '{':
		open, closeCh = '{', '}'
	case '[':
		open, closeCh = '[', ']'
	default:
		return -1
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(text); i++ {
		c := text[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch c {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case open:
			depth++
		case closeCh:
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return -1
}

func jsonStringValue(raw string) string {
	var s string
	if json.Unmarshal([]byte(raw), &s) == nil {
		return s
	}
	return strings.Trim(raw, `"`)
}

func skipSpace(text string, i int) int {
	for i < len(text) {
		switch text[i] {
		case ' ', '\t', '\r', '\n':
			i++
		default:
			return i
		}
	}
	return i
}

// --- bare array parsing ---

func parseNestedFunctionArray(text string) []llm.ToolCall {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "[") {
		return nil
	}
	var raw []struct {
		ID       string `json:"id"`
		Function struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"function"`
	}
	if err := json.Unmarshal([]byte(text), &raw); err != nil || len(raw) == 0 {
		return nil
	}
	out := make([]llm.ToolCall, 0, len(raw))
	for _, c := range raw {
		if c.Function.Name == "" {
			continue
		}
		out = append(out, llm.ToolCall{
			ID:   c.ID,
			Type: toolCallTypeFunction,
			Function: llm.ToolCallFunction{
				Name:      c.Function.Name,
				Arguments: normalizeArguments(c.Function.Arguments),
			},
		})
	}
	return out
}

func parseFlatFunctionArray(text string) []llm.ToolCall {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "[") {
		return nil
	}
	var raw []rawToolCall
	if err := json.Unmarshal([]byte(text), &raw); err != nil || len(raw) == 0 {
		return nil
	}
	out := make([]llm.ToolCall, 0, len(raw))
	for _, c := range raw {
		if c.Name == "" {
			continue
		}
		out = append(out, llm.ToolCall{
			ID:   c.ID,
			Type: toolCallTypeFunction,
			Function: llm.ToolCallFunction{
				Name:      c.Name,
				Arguments: normalizeArguments(c.Arguments),
			},
		})
	}
	return out
}

// --- id assignment ---

// assignCallIDs guarantees every call carries a unique ID before the calls
// reach the runner, which keys tool calls by ID (drain.go) and silently drops
// a later call that collides with an earlier one. A model-supplied ID is kept
// when it is unique; an empty or duplicate ID is replaced with the next free
// call_<n>. Centralising here — rather than minting per-array in each leaf
// parser — is what makes the tagged path safe, where several blocks are merged
// and each would otherwise restart its own call_0 counter.
func assignCallIDs(calls []llm.ToolCall) []llm.ToolCall {
	seen := make(map[string]bool, len(calls))
	n := 0
	nextFree := func() string {
		for {
			id := fmt.Sprintf("call_%d", n)
			n++
			if !seen[id] {
				return id
			}
		}
	}
	for i := range calls {
		id := calls[i].ID
		if id == "" || seen[id] {
			id = nextFree()
		}
		seen[id] = true
		calls[i].ID = id
	}
	return calls
}

// --- argument normalization ---

func normalizeArguments(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == jsonNull {
		return "{}"
	}
	// The OpenAI wire form encodes arguments as a JSON-encoded string
	// ("{\"k\":1}"); some models nest the object directly ({"k":1}).
	if strings.HasPrefix(s, `"`) {
		var unquoted string
		if json.Unmarshal(raw, &unquoted) == nil {
			if unquoted = strings.TrimSpace(unquoted); unquoted != "" && unquoted != jsonNull {
				return unquoted
			}
			return "{}"
		}
	}
	return s
}

// --- utilities ---

func stripJSONFence(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "```") {
		text = strings.TrimPrefix(text, "```json")
		text = strings.TrimPrefix(text, "```")
		// Trim again so a "```json\n" opener leaves no leading newline for
		// callers that don't re-trim before parsing.
		text = strings.TrimSpace(text)
		text = strings.TrimSuffix(text, "```")
		text = strings.TrimSpace(text)
	}
	return text
}
