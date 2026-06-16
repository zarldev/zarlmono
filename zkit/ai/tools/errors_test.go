package tools_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestKind_String(t *testing.T) {
	cases := map[tools.Kind]string{
		tools.Kinds.UNKNOWN:    "unknown",
		tools.Kinds.VALIDATION: "validation",
		tools.Kinds.NOTFOUND:   "not_found",
		tools.Kinds.PERMISSION: "permission",
		tools.Kinds.TRANSIENT:  "transient",
		tools.Kinds.BUDGET:     "budget",
		tools.Kinds.FATAL:      "fatal",
	}
	for k, want := range cases {
		if got := k.String(); got != want {
			t.Errorf("Kind(%d).String() = %q, want %q", k, got, want)
		}
	}
}

func TestError_Format(t *testing.T) {
	cases := []struct {
		name string
		err  *tools.Error
		want string
	}{
		{
			name: "validation_with_op_and_reason",
			err:  tools.Validation("bash", "missing command"),
			want: "bash: validation: missing command",
		},
		{
			name: "transient_wraps_cause",
			err:  tools.Transient("read", errors.New("EAGAIN")),
			want: "read: transient: EAGAIN",
		},
		{
			name: "no_op",
			err:  &tools.Error{Kind: tools.Kinds.FATAL, Reason: "boom"},
			want: "fatal: boom",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestError_Unwrap(t *testing.T) {
	inner := errors.New("inner cause")
	err := tools.Transient("bash", inner)
	if !errors.Is(err, inner) {
		t.Errorf("errors.Is should reach the wrapped cause")
	}
}

func TestError_AsType(t *testing.T) {
	original := tools.Validation("bash", "missing command")
	wrapped := fmt.Errorf("outer: %w", original)

	e, ok := errors.AsType[*tools.Error](wrapped)
	if !ok {
		t.Fatal("errors.AsType[*tools.Error] should find the wrapped Error")
	}
	if e.Kind != tools.Kinds.VALIDATION {
		t.Errorf("Kind = %v, want %v", e.Kind, tools.Kinds.VALIDATION)
	}
	if e.Op != "bash" {
		t.Errorf("Op = %q, want %q", e.Op, "bash")
	}
	if !strings.Contains(e.Reason, "missing command") {
		t.Errorf("Reason = %q, want to contain %q", e.Reason, "missing command")
	}
}

func TestKindOf(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want tools.Kind
	}{
		{name: "nil", err: nil, want: tools.Kinds.UNKNOWN},
		{name: "plain", err: errors.New("plain"), want: tools.Kinds.UNKNOWN},
		{name: "validation", err: tools.Validation("op", "r"), want: tools.Kinds.VALIDATION},
		{name: "wrapped", err: fmt.Errorf("ctx: %w", tools.NotFound("op", "r")), want: tools.Kinds.NOTFOUND},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := tools.KindOf(tt.err); got != tt.want {
				t.Errorf("KindOf = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConstructors_FixKind(t *testing.T) {
	cases := map[tools.Kind]*tools.Error{
		tools.Kinds.VALIDATION: tools.Validation("o", "r"),
		tools.Kinds.NOTFOUND:   tools.NotFound("o", "r"),
		tools.Kinds.PERMISSION: tools.Permission("o", "r"),
		tools.Kinds.TRANSIENT:  tools.Transient("o", errors.New("x")),
		tools.Kinds.BUDGET:     tools.Budget("o", "r"),
		tools.Kinds.FATAL:      tools.Fatal("o", errors.New("x")),
	}
	for want, got := range cases {
		if got.Kind != want {
			t.Errorf("constructor for %v produced Kind=%v", want, got.Kind)
		}
	}
}
