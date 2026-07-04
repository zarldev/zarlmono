package tools_test

import (
	"context"
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

type fakeArgs struct {
	Path  string `json:"path"`
	Count int    `json:"count"`
}

type fakeResult struct {
	Message string `json:"message"`
}

func TestDecodeArgs_PopulatesStruct(t *testing.T) {
	t.Parallel()
	got, err := tools.DecodeArgs[fakeArgs](tools.ToolParameters{
		"path":  "foo.go",
		"count": float64(42),
	})
	if err != nil {
		t.Fatalf("DecodeArgs: %v", err)
	}
	if got.Path != "foo.go" || got.Count != 42 {
		t.Errorf("got = %+v", got)
	}
}

func TestDecodeArgs_EmptyParamsLeavesZeroValue(t *testing.T) {
	t.Parallel()
	got, err := tools.DecodeArgs[fakeArgs](tools.ToolParameters{})
	if err != nil {
		t.Fatalf("DecodeArgs: %v", err)
	}
	if got != (fakeArgs{}) {
		t.Errorf("got = %+v, want zero-value", got)
	}
}

func TestDecodeArgs_TypeMismatchReturnsValidationError(t *testing.T) {
	t.Parallel()
	_, err := tools.DecodeArgs[fakeArgs](tools.ToolParameters{"path": "x", "count": "not a number"})
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

func TestNewTyped_DecodesArgsAndReturnsTypedResult(t *testing.T) {
	t.Parallel()
	tool := tools.NewTyped(tools.ToolSpec{
		Name:        "fake",
		Description: "fake typed tool",
		Parameters:  tools.SchemaFor[fakeArgs](),
	}, func(_ context.Context, args fakeArgs) (fakeResult, error) {
		if args.Path != "foo.go" || args.Count != 2 {
			t.Fatalf("args = %+v", args)
		}
		return fakeResult{Message: args.Path}, nil
	})

	res, err := tool.Execute(t.Context(), tools.ToolCall{
		ID:       "call-1",
		ToolName: "fake",
		Arguments: tools.ToolParameters{
			"path":  "foo.go",
			"count": 2,
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.Success {
		t.Fatalf("Success = false, error = %q", res.Error)
	}
	got, ok := res.Data.(fakeResult)
	if !ok {
		t.Fatalf("Data has type %T, want fakeResult", res.Data)
	}
	if got.Message != "foo.go" {
		t.Errorf("Message = %q, want foo.go", got.Message)
	}
}

func TestNewTyped_DecodeFailureReturnsToolFailure(t *testing.T) {
	t.Parallel()
	tool := tools.NewTyped(tools.ToolSpec{Name: "fake"}, func(_ context.Context, _ fakeArgs) (fakeResult, error) {
		t.Fatal("handler should not run")
		return fakeResult{}, nil
	})

	res, err := tool.Execute(t.Context(), tools.ToolCall{
		ID:        "call-1",
		Arguments: tools.ToolParameters{"count": "wrong"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Success {
		t.Fatal("Success = true, want failure")
	}
	if res.Err == nil || res.Err.Kind != tools.Kinds.VALIDATION {
		t.Fatalf("Err = %+v, want validation", res.Err)
	}
}

func TestNewTyped_AttachesDerivedEffects(t *testing.T) {
	t.Parallel()
	tool := tools.NewTyped(
		tools.ToolSpec{Name: "fake"},
		func(_ context.Context, args fakeArgs) (fakeResult, error) {
			return fakeResult{Message: args.Path}, nil
		},
		tools.WithTypedEffects(func(result fakeResult) []tools.Effect {
			return []tools.Effect{tools.NewFileEffect(tools.FileRead, result.Message)}
		}),
	)

	res, err := tool.Execute(t.Context(), tools.ToolCall{
		ID:        "call-1",
		Arguments: tools.ToolParameters{"path": "foo.go"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	files := res.FileEffects()
	if len(files) != 1 || files[0].Path != "foo.go" || files[0].Op != tools.FileRead {
		t.Fatalf("FileEffects = %+v", files)
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
