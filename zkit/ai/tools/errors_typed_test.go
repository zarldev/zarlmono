package tools_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// These tests cover the typed-error contract on *tools.Error and its
// projection into ToolResult.Err — the field guardrails switch on
// instead of substring-matching ToolResult.Error.

func TestError_AsTypeRecoversKind(t *testing.T) {
	t.Parallel()
	e := tools.Validation("write", "path required")
	// errors.AsType is the 1.26 generic API the codebase standardises on.
	got, ok := errors.AsType[*tools.Error](error(e))
	if !ok {
		t.Fatal("AsType[*tools.Error]: want ok=true")
	}
	if got.Kind != tools.Kinds.VALIDATION {
		t.Errorf("Kind = %v, want Validation", got.Kind)
	}
	if got.Op != "write" || got.Reason != "path required" {
		t.Errorf("Op=%q Reason=%q, want (write, path required)", got.Op, got.Reason)
	}
}

func TestError_IsTraversesWrappedChain(t *testing.T) {
	t.Parallel()
	// errors.Is walks through Unwrap, which *tools.Error implements
	// against its Wrapped field. A consumer with a sentinel can use
	// errors.Is on a fatal error that wraps it.
	sentinel := errors.New("sentinel-x")
	e := tools.Fatal("bash", sentinel)
	if !errors.Is(e, sentinel) {
		t.Error("errors.Is(fatal, sentinel): want true, sentinel must be reachable via Unwrap")
	}
}

func TestError_JSONRoundTrip(t *testing.T) {
	t.Parallel()
	// ToolResult.Err sits inside a struct that may be marshalled (mcp
	// responses, headless run snapshots, future state.db tool-result
	// caches). Custom MarshalJSON / UnmarshalJSON must preserve Kind,
	// Op, Reason; Wrapped flattens to its string form on the wire and
	// becomes a sentinel-less errors.New on the way back.
	in := tools.Fatal("edit", errors.New("disk full"))
	in.Op = "edit"
	in.Reason = "see wrapped"
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var out tools.Error
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out.Kind != tools.Kinds.FATAL {
		t.Errorf("Kind = %v, want Fatal", out.Kind)
	}
	if out.Op != "edit" || out.Reason != "see wrapped" {
		t.Errorf("Op=%q Reason=%q", out.Op, out.Reason)
	}
	if out.Wrapped == nil || out.Wrapped.Error() != "disk full" {
		t.Errorf("Wrapped = %v, want 'disk full'", out.Wrapped)
	}
}

func TestError_JSONUnmarshalRejectsUnknownKind(t *testing.T) {
	t.Parallel()
	// parseKind fails loud — a stale snapshot from a future build
	// that renamed a Kind shouldn't silently downgrade to Unknown.
	var out tools.Error
	err := json.Unmarshal([]byte(`{"kind":"some-new-thing"}`), &out)
	if err == nil {
		t.Fatal("Unmarshal(unknown kind): want error, got nil")
	}
}

func TestError_JSONKindAsStableString(t *testing.T) {
	t.Parallel()
	// Kind ships on the wire as its String identifier, not its int.
	// This is the contract that lets us reorder the const block safely.
	in := tools.Validation("op", "reason")
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var probe struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatal(err)
	}
	if probe.Kind != "validation" {
		t.Errorf("wire kind = %q, want 'validation'", probe.Kind)
	}
}

func TestError_RoundTripFromBareError(t *testing.T) {
	t.Parallel()
	// Bare errors (no Wrapped, no Op, no Reason) should still survive
	// the trip — a Validation with empty Reason produces a valid JSON
	// payload that decodes back to the same Kind.
	in := &tools.Error{Kind: tools.Kinds.BUDGET}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out tools.Error
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.Kind != tools.Kinds.BUDGET {
		t.Errorf("Kind = %v, want Budget", out.Kind)
	}
}

func TestError_ToolResultJSONUsesCustomMarshaller(t *testing.T) {
	t.Parallel()
	// Guard against the pointer-receiver footgun: if MarshalJSON is
	// defined on *Error but ToolResult.Err were ever changed to a value
	// type, encoding/json would silently skip the custom marshaller.
	// This test encodes a full ToolResult and verifies the Err field
	// carries the projected shape ("kind" as string).
	tr := tools.Failure("call-1", tools.Validation("write", "missing path"))
	raw, err := json.Marshal(tr)
	if err != nil {
		t.Fatalf("Marshal ToolResult: %v", err)
	}
	var probe struct {
		Err struct {
			Kind string `json:"kind"`
		} `json:"err"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("Unmarshal probe: %v", err)
	}
	if probe.Err.Kind != "validation" {
		t.Fatalf("ToolResult.Err.Kind = %q, want 'validation' — custom marshaller missed", probe.Err.Kind)
	}
}
