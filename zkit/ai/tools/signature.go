package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// CallSignature returns a stable, fixed-size key derived from a tool
// call's name and canonicalized arguments. Wrappers and guardrails
// that bucket per-call state (memoization, failure counting,
// repeat-call caps) all share this helper so semantically identical
// calls produce identical keys regardless of argument map ordering.
//
// The key is a hex-encoded SHA-256 over (toolname, NUL, canonical
// JSON of args). Stdlib json.Marshal already sorts map[string]any
// keys; we recurse through nested values via canonicalize so the
// "same content, different ordering" guarantee holds at every depth.
func CallSignature(call ToolCall) string {
	args, _ := json.Marshal(canonicalize(call.Arguments))
	h := sha256.New()
	h.Write([]byte(call.ToolName))
	h.Write([]byte{0})
	h.Write(args)
	return hex.EncodeToString(h.Sum(nil))
}

// canonicalize returns value with map keys recursively sorted, so
// JSON marshaling of identical-content maps with different insertion
// orders produces the same bytes.
func canonicalize(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, sub := range v {
			out[k] = canonicalize(sub)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, sub := range v {
			out[i] = canonicalize(sub)
		}
		return out
	case ToolParameters:
		return canonicalize(map[string]any(v))
	default:
		return v
	}
}
