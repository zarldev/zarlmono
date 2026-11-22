package runner

import (
	"bytes"
	"encoding/json"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/repair"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// canonicalizeToolArgs repairs malformed provider history without rewriting
// already-valid JSON, numeric lexemes, whitespace, or duplicate members.
// Unrecoverable input is left for dispatch's Validation result.
func canonicalizeToolArgs(toolCalls map[string]*llm.ToolCall) {
	for _, tc := range toolCalls {
		if tc.Function.Arguments == "" || json.Valid([]byte(tc.Function.Arguments)) {
			continue
		}
		var raw json.RawMessage
		if err := repair.Unmarshal([]byte(tc.Function.Arguments), &raw); err == nil {
			tc.Function.Arguments = string(raw)
		}
	}
}

func decodeToolArguments(arguments string, dst *tools.ToolParameters) error {
	// RawMessage enforces whole-document syntax before Decoder.Decode, which
	// alone would silently accept the first of multiple trailing documents.
	// Keep the existing repair cascade (including flat-object extraction).
	var raw json.RawMessage
	if err := repair.Unmarshal([]byte(arguments), &raw); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	return decoder.Decode(dst)
}
