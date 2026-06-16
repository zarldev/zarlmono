package runner

import (
	"errors"
	"io"
	"strings"
)

// Stream-failure classifiers. These pair with the soft-recovery ladder in
// recoverFromStreamErr, not with the runner's own sentinel vocabulary — they
// match brittle upstream provider body text (a different stability contract
// from the errors.Is-able sentinels in errors.go), so they live apart.
//
// Both are free functions (not methods) so the classification stays testable
// against a synthetic error without standing up a full Runner.

// isUpstreamToolCallJSONError pattern-matches stream errors whose body
// indicates the upstream LLM server rejected the model's tool-call arguments
// as malformed JSON. llama-server's --jinja path emits "Failed to parse tool
// call arguments as JSON" in the 500 body; we match conservatively on that
// exact substring so we don't accidentally recover from unrelated 500s.
func isUpstreamToolCallJSONError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "Failed to parse tool call arguments as JSON")
}

// isEmptyStreamDecodeError reports whether err is the EOF-class decode
// failure the SDK surfaces when a completion stream closes with a truncated
// or empty terminating frame: encoding/json's "unexpected end of JSON input"
// (an unwrappable *json.SyntaxError, so matched by string) or an io.EOF /
// io.ErrUnexpectedEOF wrap. Matched conservatively; the call site must also
// guard on zero content and zero tool calls so only a *wholly* empty stream —
// the transient gateway-cut scenario [ErrEmptyStream] describes — is
// reclassified, never a decode error that interrupts real output mid-flight.
func isEmptyStreamDecodeError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "unexpected end of JSON input") ||
		strings.Contains(msg, "unexpected EOF")
}
