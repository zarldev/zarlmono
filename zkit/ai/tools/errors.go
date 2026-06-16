package tools

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Compile-time guard: *Error must satisfy json.Marshaler so that
// ToolResult.Err (which is *Error) gets the custom wire format.
// If this is ever changed to a value receiver, update ToolResult.Err
// to *Error or add a parallel value-receiver MarshalJSON.
var _ json.Marshaler = (*Error)(nil)

// Error is the typed error every tool returns when classification
// matters. The Kind dictates which fields the constructor populates;
// callers should always build one via the per-Kind constructors below
// rather than the struct literal.
type Error struct {
	// Kind classifies the failure. Consumers switch on this.
	Kind Kind
	// Op is the tool name (or sub-operation) that failed. Empty is fine.
	Op string
	// Reason is a short human-readable explanation. Mirrors what the
	// LLM ultimately sees in the tool message.
	Reason string
	// Wrapped is the underlying cause, if any. Surfaces through Unwrap
	// so deeper errors.Is / errors.AsType chains keep working.
	Wrapped error
}

// Error formats as "[op:] kind[: reason][: wrapped]" — empty pieces
// are omitted so the message stays terse.
func (e *Error) Error() string {
	var b strings.Builder
	if e.Op != "" {
		b.WriteString(e.Op)
		b.WriteString(": ")
	}
	b.WriteString(e.Kind.String())
	if e.Reason != "" {
		b.WriteString(": ")
		b.WriteString(e.Reason)
	}
	if e.Wrapped != nil {
		b.WriteString(": ")
		b.WriteString(e.Wrapped.Error())
	}
	return b.String()
}

// Unwrap exposes the inner cause for errors.Is / errors.AsType walks.
func (e *Error) Unwrap() error { return e.Wrapped }

// errorJSON is the on-wire shape of *Error. Kind serialises as its
// stable string identifier (matching Kind.String) rather than the
// underlying int so reordering the const block doesn't silently
// corrupt persisted data. Wrapped flattens to the inner error's
// .Error() message; the chain is preserved as a sentinel-less
// errors.New on the decode side. Callers that need the original
// typed Wrapped should consult it before serialisation — JSON is
// a projection, not a roundtrip of arbitrary error interfaces.
type errorJSON struct {
	Kind    string `json:"kind"`
	Op      string `json:"op,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Wrapped string `json:"wrapped,omitempty"`
}

// MarshalJSON renders an *Error as a stable, human-readable JSON
// object. Designed so a ToolResult containing an Err field can be
// snapshotted to wire or sqlite and recovered later with errors.Is
// and errors.AsType[*tools.Error] still working — the Kind / Op /
// Reason survive; the Wrapped chain flattens to its string form.
func (e *Error) MarshalJSON() ([]byte, error) {
	out := errorJSON{
		Kind:   e.Kind.String(),
		Op:     e.Op,
		Reason: e.Reason,
	}
	if e.Wrapped != nil {
		out.Wrapped = e.Wrapped.Error()
	}
	return json.Marshal(out)
}

// UnmarshalJSON reconstructs an *Error from the projected JSON
// shape. Wrapped becomes a sentinel-less errors.New(message);
// downstream errors.Is/AsType walks reach it but can't recover its
// original typed identity. Kind comes back via ParseKind so an
// unrecognised value fails loud rather than silently turning into
// Kinds.UNKNOWN; an empty/absent kind stays Kinds.UNKNOWN for legacy
// payloads that predate the field.
func (e *Error) UnmarshalJSON(data []byte) error {
	var raw errorJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	k := Kinds.UNKNOWN
	if raw.Kind != "" {
		parsed, err := ParseKind(raw.Kind)
		if err != nil {
			return err
		}
		k = parsed
	}
	e.Kind = k
	e.Op = raw.Op
	e.Reason = raw.Reason
	if raw.Wrapped != "" {
		e.Wrapped = errors.New(raw.Wrapped)
	}
	return nil
}

// --- per-Kind constructors ---
//
// Each constructor fixes Kind and takes only the fields meaningful for
// that classification. Returning *Error keeps the errors.AsType type
// parameter unambiguous at call sites.

// Validation reports a malformed argument. reason should name the
// offending field and what's wrong with it.
func Validation(op, reason string) *Error {
	return &Error{Kind: Kinds.VALIDATION, Op: op, Reason: reason}
}

// NotFound reports that a requested resource doesn't exist.
func NotFound(op, reason string) *Error {
	return &Error{Kind: Kinds.NOTFOUND, Op: op, Reason: reason}
}

// Permission reports that the operation isn't permitted.
func Permission(op, reason string) *Error {
	return &Error{Kind: Kinds.PERMISSION, Op: op, Reason: reason}
}

// Transient reports a temporary failure. wrapped carries the inner
// cause (network error, lock contention) so retry policy can inspect it.
func Transient(op string, wrapped error) *Error {
	return &Error{Kind: Kinds.TRANSIENT, Op: op, Wrapped: wrapped}
}

// Budget reports a per-task budget exhaustion.
func Budget(op, reason string) *Error {
	return &Error{Kind: Kinds.BUDGET, Op: op, Reason: reason}
}

// Fatal reports a non-recoverable execution failure.
func Fatal(op string, wrapped error) *Error {
	return &Error{Kind: Kinds.FATAL, Op: op, Wrapped: wrapped}
}

// Stale reports a well-formed call whose anchor no longer matches the
// target because it changed since it was read. reason should name the
// stale anchor and tell the caller to re-read; the Kind steers guardrails
// toward "re-read the file" rather than "fix your input format".
func Stale(op, reason string) *Error {
	return &Error{Kind: Kinds.STALE, Op: op, Reason: reason}
}

// Failure packages an error as a failed ToolResult. When err is
// (or wraps) a *Error, Kind and the typed Err field are populated
// structurally; a bare error leaves both at their zero values. The
// projection mirrors what the runner expects after dispatch, so
// every tool returns the same shape regardless of which error type
// the body produced.
func Failure(callID string, err error) *ToolResult {
	res := &ToolResult{
		ToolCallID: callID,
		Success:    false,
		Error:      err.Error(),
		ExecutedAt: time.Now(),
	}
	if e, ok := errors.AsType[*Error](err); ok {
		res.Err = e
	}
	return res
}

// Success packages data as a successful ToolResult. It mirrors [Failure]
// so tools construct result envelopes consistently instead of open-coding
// timestamps and effect slices at each call site.
func Success(callID string, data any, effects ...Effect) *ToolResult {
	return &ToolResult{
		ToolCallID: callID,
		Success:    true,
		Data:       data,
		Effects:    effects,
		ExecutedAt: time.Now(),
	}
}

// KindOf walks err's chain and returns the first *Error's Kind, or
// Kinds.UNKNOWN when no *tools.Error is present. Use this when only the
// classification matters and the full struct doesn't.
func KindOf(err error) Kind {
	if err == nil {
		return Kinds.UNKNOWN
	}
	if e, ok := errors.AsType[*Error](err); ok {
		return e.Kind
	}
	return Kinds.UNKNOWN
}
