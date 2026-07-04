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
type WriteTool struct {
	ws   Workspace
	tool tools.Tool
}

// WriteArgs is the typed argument shape WriteTool's Execute decodes
// into. Field tags drive both JSON decoding and SchemaFor schema
// generation — doc tags supply the LLM-facing descriptions.
type WriteArgs struct {
	Path    string `json:"path" doc:"Path relative to workspace root."`
	Content string `json:"content" doc:"Full file contents to write."`
}

// NewWriteTool returns a workspace-scoped write tool. The returned concrete
// type caches a typed-tool adapter so Execute stays a thin dispatch boundary.
func NewWriteTool(ws Workspace) *WriteTool {
	t := &WriteTool{ws: ws}
	t.tool = tools.NewTyped(
		writeSpec(),
		t.executeTyped,
		tools.WithTypedEffects(func(result WriteResult) []tools.Effect {
			effect := tools.NewFileEffect(tools.FileCreate, result.Path)
			effect.File.BytesAfter = int64(result.Bytes)
			return []tools.Effect{effect}
		}),
	)
	return t
}

// Definition advertises write with required path and content; Mutates
// is true because a successful call creates a new file. The spec text
// routes existing-path retries to edit.
func (t *WriteTool) Definition() tools.ToolSpec { return writeSpec() }

func writeSpec() tools.ToolSpec {
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
func (t *WriteTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	return t.tool.Execute(ctx, call)
}

// WriteResult is write's structured success payload.
type WriteResult struct {
	Path  string `json:"path"`
	Bytes int    `json:"bytes"`
}

// String renders the model-facing success text for WriteResult.
func (r WriteResult) String() string { return fmt.Sprintf("wrote %d bytes to %s", r.Bytes, r.Path) }

func (t *WriteTool) executeTyped(_ context.Context, args WriteArgs) (WriteResult, error) {
	if args.Path == "" {
		return WriteResult{}, tools.Validation("write", "path required")
	}
	// Reject oversized content before touching the filesystem. See
	// WriteTool's doc comment — for files larger than the cap, scaffold
	// + write_append is the reliable path. Set CODE_WRITE_MAX_BYTES at
	// startup to raise the cap if your model+server can handle larger
	// streaming args reliably.
	if maxWriteContentBytes > 0 && len(args.Content) > maxWriteContentBytes {
		return WriteResult{}, tools.Validation("write", fmt.Sprintf(
			"%q: content too large (%d bytes; cap %d bytes). "+
				"Scaffold first with write(%q, \"\") then call write_append repeatedly with chunks up to %d bytes each. "+
				"The cap is the only constraint — chunks well below the cap waste iterations.",
			args.Path, len(args.Content), maxWriteContentBytes, args.Path, maxWriteContentBytes))
	}

	abs, err := t.ws.Resolve(args.Path)
	if err != nil {
		return WriteResult{}, tools.Permission("write", err.Error())
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
		return WriteResult{}, tools.Validation("write", fmt.Sprintf(
			"%q already exists. write only creates NEW files — use edit to modify it:\n"+
				"  1) read(path=%q, ...) to get line/hash anchors\n"+
				"  2) edit(path=%q, start_line=<from read>, start_hash=<from read>, new_string=<replacement> ...)\n"+
				"If you don't already know the current content, call read first. "+
				"Use the anchors from read; they usually survive line-number shifts from earlier edits, but if the file changed underneath you then re-read and retry with fresh anchors. "+
				"For multiple changes, emit one edit per location — do not retry write, it will be refused again.",
			args.Path, args.Path, args.Path))
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return WriteResult{}, tools.Fatal("write", fmt.Errorf("stat %q: %w", args.Path, statErr))
	}
	// Route through the workspace's [*os.Root] handle so the write
	// can't escape the boundary even if a parent directory is
	// swapped for a symlink between Resolve and Write (TOCTOU).
	if err := t.ws.WriteFileInRoot(abs, []byte(args.Content), filesystem.ModePublicFile); err != nil {
		return WriteResult{}, tools.Fatal("write", fmt.Errorf("%q: %w", args.Path, err))
	}
	return WriteResult{Path: args.Path, Bytes: len(args.Content)}, nil
}
