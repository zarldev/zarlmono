package toolkit_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools/toolkit"
)

type sha256Args struct {
	Text string `json:"text" doc:"input string"`
}

func sha256Tool() toolkit.Tool[sha256Args, string] {
	return toolkit.Tool[sha256Args, string]{
		Name:        "sha256_hex",
		Description: "Compute the SHA-256 hex digest of a string.",
		Func: func(_ context.Context, a sha256Args) (string, error) {
			return fmt.Sprintf("%x", sha256.Sum256([]byte(a.Text))), nil
		},
	}
}

func TestTool_Describe(t *testing.T) {
	t.Parallel()
	spec := sha256Tool().Describe()
	if spec.Name != "sha256_hex" {
		t.Errorf("Name = %q, want sha256_hex", spec.Name)
	}
	if spec.Description == "" {
		t.Error("Description empty")
	}
	if spec.Parameters.IsZero() {
		t.Fatal("Parameters nil")
	}
	if got := spec.Parameters.Map()["type"]; got != "object" {
		t.Errorf("Parameters.type = %v, want object", got)
	}
	props, ok := spec.Parameters.Map()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("Parameters.properties wrong shape: %T", spec.Parameters.Map()["properties"])
	}
	textSchema, ok := props["text"].(map[string]any)
	if !ok {
		t.Fatalf("text property missing or wrong shape: %T", props["text"])
	}
	if textSchema["type"] != "string" {
		t.Errorf("text.type = %v, want string", textSchema["type"])
	}
	if textSchema["description"] != "input string" {
		t.Errorf("text.description = %v, want from doc tag", textSchema["description"])
	}
}

func TestTool_Call_Success(t *testing.T) {
	t.Parallel()
	in, _ := json.Marshal(sha256Args{Text: "hello"})
	got, err := sha256Tool().Call(context.Background(), in)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	want := fmt.Sprintf("%x", sha256.Sum256([]byte("hello")))
	if got != want {
		t.Errorf("Call = %q, want %q", got, want)
	}
}

func TestTool_Call_BadJSON(t *testing.T) {
	t.Parallel()
	_, err := sha256Tool().Call(context.Background(), json.RawMessage(`{not json`))
	if err == nil {
		t.Error("expected decode error, got nil")
	}
	if !strings.Contains(err.Error(), "decode args") {
		t.Errorf("error = %v, want substring 'decode args'", err)
	}
}

func TestTool_Call_NilFunc(t *testing.T) {
	t.Parallel()
	tool := toolkit.Tool[sha256Args, string]{Name: "x", Description: "y"}
	_, err := tool.Call(context.Background(), nil)
	if err == nil {
		t.Error("expected error for nil Func")
	}
}

// Compile-time check: Tool[Args, Out] satisfies Handler.
var _ toolkit.Handler = toolkit.Tool[sha256Args, string]{}
