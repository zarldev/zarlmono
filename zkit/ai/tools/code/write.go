package code

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/filesystem"
)

// WriteTool creates a new file inside the workspace. Existing paths
// are rejected with a Validation error whose Reason names edit as the
// recovery path — this is a runtime invariant, not prompt guidance:
// the tool physically cannot overwrite, so models that would otherwise
// silently clobber an existing file are forced through the edit/read
// loop. Small-model benchmark runs show the refusal fires on a
// substantial fraction of exercises and consistently improves
// correctness — the model's "rewrite the whole file" instinct is
// rarely the right move once a file exists.
//
// # Content size cap
//
// content is capped at maxWriteContentBytes (default 256KB, tunable
// via CODE_WRITE_MAX_BYTES — see limits.go). Older llama.cpp builds
// dropped characters inside long streaming tool-call JSON; the cap
// is generous now (modern servers handle 256KB cleanly) but the
// scaffold-with-empty-write + chunked write_append fallback path
// stays available for whatever genuine outliers a model might
// produce. The cap is enforced at the tool layer because
// prompt compliance breaks under context pressure.
//
// Execute decodes call.Arguments into a typed WriteArgs struct via
// tools.DecodeArgs at the top of the method, so the body reads
// args.Path / args.Content directly instead of doing
// call.Arguments.String("path", "") accessors. The shared
// DecodeArgs helper routes through repair.Unmarshal, so the same
// small-model JSON quirks (literal newlines in content, trailing
// commas, missing closers) get repaired here too.
type WriteTool struct{ ws Workspace }

// WriteArgs is the typed argument shape WriteTool's Execute decodes
// into. Field tags drive both JSON decoding and SchemaFor schema
// generation — doc tags supply the LLM-facing descriptions.
type WriteArgs struct {
	Path    string `json:"path" doc:"Path relative to workspace root."`
	Content string `json:"content" doc:"Full file contents to write."`
}

// NewWriteTool returns a workspace-scoped write tool. The returned
// *WriteTool satisfies tools.Tool directly via its Execute method,
// which decodes typed WriteArgs at the top through tools.DecodeArgs.
// Constructor stays concrete so callers retain access to any
// tool-specific helpers they might add later.
func NewWriteTool(ws Workspace) *WriteTool { return &WriteTool{ws: ws} }

// Definition advertises write with required path and content; Mutates
// is true because a successful call creates a new file. The spec text
// routes existing-path retries to edit.
func (t *WriteTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name: ToolNameWrite,
		Description: "Create a NEW file in the workspace. Refuses if the path already exists — " +
			"use `edit` to modify an existing file. Creates parent directories as needed.",
		Parameters: tools.SchemaFor[WriteArgs](),
		Mutates:    true,
	}
}

// Execute rejects content over maxWriteContentBytes (256KB default)
// before touching the filesystem, resolves the path, then — under the
// per-path lock — refuses existing paths with an edit recipe and
// writes through the workspace's os.Root handle, emitting a FileCreate
// effect carrying the byte count.
func (t *WriteTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	var args WriteArgs
	if derr := tools.DecodeArgs(call.Arguments, &args); derr != nil {
		return tools.Failure(call.ID, derr), nil
	}
	if args.Path == "" {
		return tools.Failure(call.ID, tools.Validation("write", "path required")), nil
	}
	// Reject oversized content before touching the filesystem. See
	// WriteTool's doc comment — for files larger than the cap, scaffold
	// + write_append is the reliable path. Set CODE_WRITE_MAX_BYTES at
	// startup to raise the cap if your model+server can handle larger
	// streaming args reliably.
	if maxWriteContentBytes > 0 && len(args.Content) > maxWriteContentBytes {
		return tools.Failure(call.ID, tools.Validation("write", fmt.Sprintf(
			"%q: content too large (%d bytes; cap %d bytes). "+
				"Scaffold first with write(%q, \"\") then call write_append repeatedly with chunks up to %d bytes each. "+
				"The cap is the only constraint — chunks well below the cap waste iterations.",
			args.Path, len(args.Content), maxWriteContentBytes, args.Path, maxWriteContentBytes))), nil
	}

	abs, err := t.ws.Resolve(args.Path)
	if err != nil {
		return tools.Failure(call.ID, tools.Permission("write", err.Error())), nil
	}
	// Serialise concurrent writers targeting the same path so a parallel
	// tool batch can't interleave writes and silently drop one update.
	unlock := t.ws.LockPath(abs)
	defer unlock()
	// Refuse on existing paths inside the lock so two concurrent writes
	// to the same new path can't both pass the check and race the
	// os.WriteFile. The Reason carries an edit recipe so the model's
	// next turn lands on the right tool with the right parameter names.
	if _, statErr := os.Stat(abs); statErr == nil {
		return tools.Failure(call.ID, tools.Validation("write", fmt.Sprintf(
			"%q already exists. write only creates NEW files — use edit to modify it:\n"+
				"  edit(path=%q, old_string=<exact current text>, new_string=<replacement>)\n"+
				"If you don't already know the current content, call read first. "+
				"Include 2-3 lines of surrounding context in old_string so it's unique in the file. "+
				"For multiple changes, emit one edit per location — do not retry write, it will be refused again.",
			args.Path, args.Path))), nil
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return tools.Failure(call.ID, tools.Fatal("write", fmt.Errorf("stat %q: %w", args.Path, statErr))), nil
	}
	// Route through the workspace's [*os.Root] handle so the write
	// can't escape the boundary even if a parent directory is
	// swapped for a symlink between Resolve and Write (TOCTOU).
	if err := t.ws.WriteFileInRoot(abs, []byte(args.Content), filesystem.ModePublicFile); err != nil {
		return tools.Failure(call.ID, tools.Fatal("write", fmt.Errorf("%q: %w", args.Path, err))), nil
	}
	effect := tools.NewFileEffect(tools.FileCreate, args.Path)
	effect.File.BytesAfter = int64(len(args.Content))
	return tools.Success(call.ID, fmt.Sprintf("wrote %d bytes to %s", len(args.Content), args.Path), effect), nil
}
