package rewind

import (
	"bytes"
	"encoding/json"
)

// The Anthropic adapter reconstructs a closed native block rather than replaying
// raw JSON. Reject fields it would discard, including citations until their SDK
// union has a lossless, version-bound checkpoint contract.
func anthropicNativeShape(data []byte, kind string) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return false
	}
	allowed := map[string]bool{"type": true}
	switch kind {
	case continuationThinking:
		allowed[continuationThinking], allowed["signature"] = true, true
	case "redacted_thinking":
		allowed["data"] = true
	case continuationText:
		allowed["text"] = true
	default:
		return false
	}
	if len(fields) != len(allowed) {
		return false
	}
	for key, value := range fields {
		if !allowed[key] {
			return false
		}
		var text string
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, &text) != nil {
			return false
		}
	}
	return true
}
