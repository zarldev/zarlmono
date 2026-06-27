// Package zhttp provides minimal HTTP response helpers — JSON envelope
// writers and a shared error shape — for handlers that don't want to
// reach for a full framework.
//
// WriteJSON / WriteError encode into a buffer first, then commit
// headers, so an encode failure surfaces a 500 with the right
// Content-Type instead of a half-written body with the wrong status.
package zhttp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Body-size constants for [DecodeJSON]. Spelled out long-form
// rather than bit-shifted so the value is obvious at the call site
// — `4<<20` mid-handler reads as line noise.
const (
	kb int64 = 1024
	mb int64 = 1024 * kb

	// DefaultMaxBodyBytes is the body-size cap [DecodeJSON] applies
	// when the caller passes 0. 1 MB is generous for JSON APIs while
	// bounding the easiest DoS shape: streaming an unbounded request
	// body to make the server allocate. Override via the maxBytes
	// parameter when an endpoint legitimately accepts larger payloads.
	DefaultMaxBodyBytes int64 = 1 * mb
)

// ErrorResponse is the wire shape for both helpers' error path.
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}

// WriteJSON encodes data as JSON and writes it with the given status.
// On encode failure, falls back to a 500 with an error envelope —
// because we encode before writing headers, the fallback is safe (no
// double WriteHeader, no half-written body).
func WriteJSON(w http.ResponseWriter, status int, data any) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(data); err != nil {
		WriteError(w, http.StatusInternalServerError, "encode response: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// WriteError writes an ErrorResponse with the given status. The "error"
// field is always http.StatusText(status); "message" is the
// caller-provided detail (omitted when empty).
func WriteError(w http.ResponseWriter, status int, message string) {
	body, _ := json.Marshal(ErrorResponse{
		Error:   http.StatusText(status),
		Message: message,
	})
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// DecodeJSON decodes a JSON request body into v with strict
// semantics:
//
//   - The body is capped at maxBytes (or [DefaultMaxBodyBytes] when
//     maxBytes <= 0). An oversized body returns an error mentioning
//     the cap so the caller can map it to 413.
//   - Unknown fields are rejected — typos in client payloads fail
//     loudly instead of silently no-op'ing.
//   - Trailing tokens are rejected — a body of `{...}{...}` doesn't
//     pass as the first object alone.
//   - When Content-Type is provided, it must start with
//     "application/json". Empty Content-Type is tolerated to avoid
//     breaking command-line testers (curl without -H).
//
// Earlier handlers called json.NewDecoder(r.Body).Decode(...) with
// no body limit, no DisallowUnknownFields, no trailing-token
// rejection, and no content-type check. The MCP server + auth
// handlers were the original DoS surfaces flagged by the
// adversarial review.
func DecodeJSON(r *http.Request, v any, maxBytes int64) error {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBodyBytes
	}
	if ct := r.Header.Get("Content-Type"); ct != "" {
		// Trim charset/boundary suffixes before the prefix check.
		base := ct
		if i := strings.IndexByte(ct, ';'); i >= 0 {
			base = strings.TrimSpace(ct[:i])
		}
		if !strings.EqualFold(base, "application/json") {
			return fmt.Errorf("content-type must be application/json, got %q", ct)
		}
	}
	r.Body = http.MaxBytesReader(nil, r.Body, maxBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			return fmt.Errorf("request body exceeds %d-byte limit", maxBytes)
		}
		return fmt.Errorf("decode body: %w", err)
	}
	// Reject trailing tokens. A second Decode beyond the first JSON
	// value should return io.EOF; anything else means the client
	// sent extra data after the document.
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return errors.New("body contains trailing data after JSON value")
	}
	return nil
}
