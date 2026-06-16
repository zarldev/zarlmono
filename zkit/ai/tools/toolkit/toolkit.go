// Package toolkit makes authoring a dynamic tool a few lines of Go.
// The runner spawns each registered binary with `--describe` to learn
// its ToolSpec and with `--call` to invoke it; this package handles
// both flags so the consumer only writes the typed handler.
//
// Canonical shape:
//
//	package main
//
//	import (
//	    "context"
//	    "crypto/sha256"
//	    "fmt"
//
//	    "github.com/zarldev/zarlmono/zkit/ai/tools/toolkit"
//	)
//
//	type Args struct {
//	    Text string `json:"text" doc:"the input string to hash"`
//	}
//
//	func main() {
//	    toolkit.Run(toolkit.Tool[Args, string]{
//	        Name:        "sha256_hex",
//	        Description: "Compute the SHA-256 hex digest of a string.",
//	        Func: func(_ context.Context, a Args) (string, error) {
//	            return fmt.Sprintf("%x", sha256.Sum256([]byte(a.Text))), nil
//	        },
//	    })
//	}
//
// The Args struct's JSON tags drive the schema the LLM sees:
//
//	`json:"name"`            field name (drop with `json:"-"`)
//	`json:",omitempty"`      optional
//	pointer / interface type optional
//	`doc:"..."`              description shown to the LLM
//	`enum:"a,b,c"`           restrict to a fixed set
//
// Tool[Args, Out] is the lazy path; for tools whose schema needs
// hand-crafted JSON Schema features (oneOf, allOf, format, $ref),
// implement [Handler] directly and pass to [RunHandler] (or to Run —
// it accepts any Handler).
package toolkit

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// Handler is what the binary's --describe / --call flags dispatch
// against. The Tool builder satisfies this; consumers needing
// hand-crafted schemas can implement it directly.
type Handler interface {
	// Describe returns the tool spec emitted on --describe.
	Describe() tools.ToolSpec

	// Call invokes the tool with raw JSON args. Return any value
	// that JSON-encodes; non-nil errors land in the {"error": ...}
	// envelope.
	Call(ctx context.Context, args json.RawMessage) (any, error)
}

// Tool is the typed handler builder. Args is the JSON shape the
// runner sends; Out is whatever the handler returns. SchemaFor[Args]
// derives the JSON Schema automatically.
type Tool[Args any, Out any] struct {
	// Name is the registered tool name. Must match
	// ^[a-z][a-z0-9_]{1,63}$ — the runner refuses other shapes.
	Name tools.ToolName

	// Description is the one-line summary shown to the LLM.
	Description string

	// Func is the typed handler. Args arrives JSON-decoded; the
	// returned Out gets JSON-encoded back to the runner.
	Func func(ctx context.Context, a Args) (Out, error)
}

// Describe returns the spec the runner sees on --describe.
func (t Tool[Args, Out]) Describe() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        t.Name,
		Description: t.Description,
		Parameters:  SchemaFor[Args](),
	}
}

// Call decodes the args, invokes the handler, returns the result or
// the error.
func (t Tool[Args, Out]) Call(ctx context.Context, raw json.RawMessage) (any, error) {
	var args Args
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, fmt.Errorf("decode args: %w", err)
		}
	}
	if t.Func == nil {
		return nil, fmt.Errorf("toolkit: tool %q has nil Func", t.Name)
	}
	return t.Func(ctx, args)
}

// Run is the entrypoint a dynamic-tool main() calls. It dispatches on
// os.Args[1] — `--describe` writes the ToolSpec JSON to stdout,
// `--call` reads args JSON from stdin, runs the handler, writes the
// {"data": ...} or {"error": ...} envelope to stdout. Exits 0 on
// success, non-zero on transport-level errors. Tool-level failures
// produce the {"error": ...} envelope and exit 0 (the runner reads
// the envelope, not the exit code).
//
// Generic over Tool[Args, Out] for ergonomics; under the hood
// delegates to RunHandler so a consumer with a custom Handler can
// reuse the same dispatch.
func Run[Args any, Out any](t Tool[Args, Out]) {
	RunHandler(t)
}

// RunHandler is Run's escape hatch — accepts any Handler. Use when
// the typed Tool[Args, Out] can't express the schema you need.
func RunHandler(h Handler) {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: %s --describe | --call\n", binName())
		os.Exit(2)
	}
	switch os.Args[1] {
	case "--describe":
		spec := h.Describe()
		if err := json.NewEncoder(os.Stdout).Encode(spec); err != nil {
			failTransportf("encode describe: %v", err)
		}
	case "--call":
		body, err := io.ReadAll(os.Stdin)
		if err != nil {
			failTransportf("read stdin: %v", err)
		}
		out, err := h.Call(context.Background(), body)
		if err != nil {
			emit(map[string]string{"error": err.Error()})
			return
		}
		emit(map[string]any{"data": out})
	default:
		fmt.Fprintf(os.Stderr, "unknown mode %q (want --describe or --call)\n", os.Args[1])
		os.Exit(2)
	}
}

// emit writes one JSON envelope to stdout. Encoding failures are
// transport-level and exit non-zero.
func emit(v any) {
	if err := json.NewEncoder(os.Stdout).Encode(v); err != nil {
		failTransportf("encode: %v", err)
	}
}

// failTransport prints to stderr and exits with code 1. Used for
// envelope/IO failures that the runner can't wrap as a tool result.
func failTransportf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func binName() string {
	if len(os.Args) > 0 {
		return os.Args[0]
	}
	return "tool"
}
