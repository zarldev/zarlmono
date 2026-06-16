package code

import (
	"context"
	"fmt"
	"os"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/filesystem"
)

// WriteAppendTool appends bytes to a file, creating it if missing.
// It exists as the fallback path for files larger than the `write`
// cap — modern llama.cpp builds + hosted providers handle the full
// 256KB default cleanly, but a model that needs to emit something
// larger (or hits a server that struggles with long streaming args)
// can scaffold with an empty `write` and stream chunks via
// `write_append`.
//
// Historical note: older llama.cpp builds dropped characters
// inside multi-KB streaming tool-call JSON; the tool was originally
// authored to chunk at ~500 bytes for Qwen3.6. That regime is gone
// — the cap is now 256KB by default (tunable via
// CODE_APPEND_MAX_BYTES). Don't chunk smaller than necessary; the
// extra round-trips cost iterations.
//
// Pattern for an oversized file (>256KB):
//
//	write(path, "")                            // empty scaffold
//	write_append(path, "<up to ~256KB chunk>") // chunk 1
//	write_append(path, "<up to ~256KB chunk>") // chunk 2
//	... etc
//
// No state is tracked between calls — the tool is stateless and just
// opens the file in append mode each time. Concurrent calls to the
// same path race; the workspace's per-call serialisation should
// keep that from happening, but if you parallelise tool calls in the
// future, add a mutex keyed by absolute path.
type WriteAppendTool struct{ ws Workspace }

// WriteAppendArgs is the typed argument struct WriteAppendTool.Execute
// decodes into via tools.DecodeArgs. Field tags drive both JSON decoding
// and SchemaFor schema generation.
type WriteAppendArgs struct {
	Path    string `json:"path" doc:"Path relative to workspace root."`
	Content string `json:"content" doc:"Bytes to append. Keep each call under the configured cap (256KB default)."`
}

// NewWriteAppendTool returns the append-or-create file tool bound to ws.
func NewWriteAppendTool(ws Workspace) *WriteAppendTool { return &WriteAppendTool{ws: ws} }

// Definition advertises write_append with required path and content;
// Mutates is true because each call appends bytes, creating the file
// and parent directories when missing.
func (t *WriteAppendTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name: ToolNameWriteAppend,
		Description: "Append content to a file in the workspace; creates the file (and parent directories) if missing. " +
			"Use this for files larger than the write cap (256KB by default): scaffold with write(path, \"\"), then " +
			"call write_append repeatedly with chunks under the cap. No chunk indices or finalize step — each call " +
			"independently appends.",
		Parameters: tools.SchemaFor[WriteAppendArgs](),
		Mutates:    true,
	}
}

// Execute caps each chunk at maxAppendContentBytes (256KB default),
// resolves the path, and — under the per-path lock — mkdirs the parent
// and opens O_APPEND|O_CREATE through the workspace root handle.
// Success reports the running file size and emits a FileAppend effect.
func (t *WriteAppendTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	var args WriteAppendArgs
	if derr := tools.DecodeArgs(call.Arguments, &args); derr != nil {
		return tools.Failure(call.ID, derr), nil
	}
	if args.Path == "" {
		return tools.Failure(call.ID, tools.Validation("write_append", "path required")), nil
	}
	// write_append exists to dodge the multi-KB streaming-arg failure
	// mode in the first place, so capping its `content` would seem
	// circular — but a model that calls it with one giant chunk hits
	// exactly the problem write_append was supposed to avoid. Cap
	// here too so the failure surfaces with a clear chunked-write
	// hint instead of a JSON-parse 500. CODE_APPEND_MAX_BYTES tunes
	// the threshold; 0 disables.
	if maxAppendContentBytes > 0 && len(args.Content) > maxAppendContentBytes {
		return tools.Failure(call.ID, tools.Validation("write_append", fmt.Sprintf(
			"%q: chunk too large (%d bytes; cap %d bytes). "+
				"Split into multiple write_append calls each up to %d bytes. "+
				"The cap is the only constraint — chunks well below the cap waste iterations.",
			args.Path, len(args.Content), maxAppendContentBytes, maxAppendContentBytes))), nil
	}

	abs, err := t.ws.Resolve(args.Path)
	if err != nil {
		return tools.Failure(call.ID, tools.Permission("write_append", err.Error())), nil
	}
	// Two concurrent appends to the same file would interleave at the
	// kernel level even with O_APPEND; from the model's perspective the
	// running total it gets back is non-deterministic. Serialise.
	unlock := t.ws.LockPath(abs)
	defer unlock()
	// Route through the workspace's [*os.Root] handle so the open
	// can't traverse a symlinked parent out of the boundary even if
	// the directory tree changes between Resolve and OpenFile.
	if err := t.ws.MkdirParentInRoot(abs); err != nil {
		return tools.Failure(call.ID, tools.Fatal("write_append", fmt.Errorf("mkdir %q: %w", args.Path, err))), nil
	}
	f, err := t.ws.OpenFileInRoot(abs, os.O_APPEND|os.O_CREATE|os.O_WRONLY, filesystem.ModePublicFile)
	if err != nil {
		return tools.Failure(call.ID, tools.Fatal("write_append", fmt.Errorf("open %q: %w", args.Path, err))), nil
	}
	defer func() { _ = f.Close() }()
	n, err := f.WriteString(args.Content)
	if err != nil {
		return tools.Failure(call.ID, tools.Fatal("write_append", fmt.Errorf("%q: %w", args.Path, err))), nil
	}

	// Surface the running total so the model can sanity-check it
	// matches the source-of-truth size it had in mind. Cheaper than
	// asking the model to re-read the file each time.
	info, statErr := os.Stat(abs)
	if statErr == nil {
		effect := tools.NewFileEffect(tools.FileAppend, args.Path)
		effect.File.BytesAfter = info.Size()
		return tools.Success(
			call.ID,
			fmt.Sprintf("appended %d bytes to %s (file size now %d)", n, args.Path, info.Size()),
			effect,
		), nil
	}
	return tools.Success(call.ID, fmt.Sprintf("appended %d bytes to %s", n, args.Path), tools.NewFileEffect(tools.FileAppend, args.Path)), nil
}
