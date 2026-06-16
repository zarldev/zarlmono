package tools_test

import (
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

type fakeArgs struct {
	Path  string `json:"path"`
	Count int    `json:"count"`
}

func TestDecodeArgs_PopulatesStruct(t *testing.T) {
	t.Parallel()
	var got fakeArgs
	if err := tools.DecodeArgs(tools.ToolParameters{
		"path":  "foo.go",
		"count": float64(42),
	}, &got); err != nil {
		t.Fatalf("DecodeArgs: %v", err)
	}
	if got.Path != "foo.go" || got.Count != 42 {
		t.Errorf("got = %+v", got)
	}
}

func TestDecodeArgs_EmptyParamsLeavesZeroValue(t *testing.T) {
	t.Parallel()
	var got fakeArgs
	if err := tools.DecodeArgs(tools.ToolParameters{}, &got); err != nil {
		t.Fatalf("DecodeArgs: %v", err)
	}
	if got != (fakeArgs{}) {
		t.Errorf("got = %+v, want zero-value", got)
	}
}

func TestDecodeArgs_TypeMismatchReturnsValidationError(t *testing.T) {
	t.Parallel()
	var got fakeArgs
	err := tools.DecodeArgs(tools.ToolParameters{"path": "x", "count": "not a number"}, &got)
	if err == nil {
		t.Fatal("want validation error on type mismatch")
	}
	te, ok := errors.AsType[*tools.Error](err)
	if !ok {
		t.Fatalf("want *tools.Error, got %T", err)
	}
	if te.Kind != tools.Kinds.VALIDATION {
		t.Errorf("Kind = %v, want Validation", te.Kind)
	}
}

func TestFailure_PopulatesErrFromTypedError(t *testing.T) {
	t.Parallel()
	res := tools.Failure("c", tools.Validation("op", "bad arg"))
	if res.Success {
		t.Fatal("want failure")
	}
	if res.Err == nil || res.Err.Kind != tools.Kinds.VALIDATION {
		t.Errorf("Err = %+v, want typed *Error with Validation", res.Err)
	}
}

func TestFailure_BareErrorLeavesErrNil(t *testing.T) {
	t.Parallel()
	res := tools.Failure("c", errors.New("raw failure"))
	if res.Success {
		t.Fatal("want failure")
	}
	if res.Err != nil {
		t.Errorf("Err = %+v, want nil for bare error", res.Err)
	}
	if res.Error != "raw failure" {
		t.Errorf("Error = %q, want 'raw failure'", res.Error)
	}
}
