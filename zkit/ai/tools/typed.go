package tools

import (
	"encoding/json"
	"fmt"

	"github.com/zarldev/zarlmono/zkit/ai/llm/repair"
)

// DecodeArgs decodes a ToolParameters map into a typed args struct
// via a JSON round-trip through repair.Unmarshal, so small-model
// quirks (literal newlines, trailing commas, missing closers) get
// repaired at the decode boundary. Returns a *Error of Kinds.VALIDATION
// on failure; callers pass it straight to Failure.
func DecodeArgs[T any](params ToolParameters, into *T) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return Validation("decode", fmt.Sprintf("re-encode arguments: %v", err))
	}
	if err := repair.Unmarshal(raw, into); err != nil {
		return Validation("decode", fmt.Sprintf(
			"tool arguments did not decode into the expected struct: %v. "+
				"Check field names and types against the tool's JSON Schema.", err))
	}
	return nil
}
