package toolkit_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/toolkit"
)

func TestCallStrictIntegerCompatibility(t *testing.T) {
	t.Parallel()
	type args struct {
		N      int64       `json:"n"`
		Any    any         `json:"any"`
		Number json.Number `json:"number"`
	}
	calls := 0
	tool := toolkit.Tool[args, args]{Name: "exact", Func: func(_ context.Context, a args) (args, error) { calls++; return a, nil }}
	for _, raw := range []string{`{"n":9007199254740993,"any":42e0,"number":42.00}`, `{"n":9007199254740993.0,"any":42e0,"number":42.00}`, `{"n":9.007199254740993e15,"any":42e0,"number":42.00}`} {
		out, err := tool.Call(t.Context(), json.RawMessage(raw))
		if err != nil {
			t.Fatal(err)
		}
		got := out.(args)
		if got.N != 9007199254740993 || got.Any != float64(42) || got.Number != "42.00" {
			t.Fatalf("got=%+v", got)
		}
	}
	for _, raw := range []string{`{"n":42,}`, `{"n":42`, `{"n":42} {"n":43}`, `{"n":42.5}`, `{"n":9223372036854775808.0}`} {
		_, err := tool.Call(t.Context(), json.RawMessage(raw))
		var validation *tools.Error
		if err == nil || !strings.HasPrefix(err.Error(), "decode args: ") || errors.As(err, &validation) {
			t.Errorf("%s: %v", raw, err)
		}
	}
	if calls != 3 {
		t.Fatalf("invalid input executed: %d", calls)
	}
	out, err := tool.Call(t.Context(), nil)
	if err != nil || out.(args).N != 0 || calls != 4 {
		t.Fatalf("empty compatibility: %v %v calls=%d", out, err, calls)
	}
	_, err = tool.Call(t.Context(), json.RawMessage(" "))
	var syntax *json.SyntaxError
	if !errors.As(err, &syntax) || calls != 4 {
		t.Fatalf("whitespace input: %v calls=%d", err, calls)
	}
}
