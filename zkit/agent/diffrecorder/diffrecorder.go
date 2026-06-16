// Package diffrecorder wraps a tool source and records the
// unified diff of every file the agent writes or edits during a turn.
// Diffs are pushed back to the consumer via an OnDiff callback; the
// Files pane in the cockpit reads them out of [fileaudit.Audit] for
// per-path inline rendering.
//
// The Recorder classifies each tool call by [Purity]:
//
//   - [Pure] tools (read / ls / grep) pass through unchanged.
//   - [Recordable] tools (write / edit / write_append) trigger a
//     pre-image snapshot of the target path; after the tool runs the
//     post-image is read back and a unified diff is fired via OnDiff.
//   - Unclassified tools (bash, MCP, dynamic tools, …) pass through
//     unchanged — observability without gating is the explicit design.
package diffrecorder

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"iter"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// DiffSink receives a unified diff for one file each time a Recordable
// tool finishes. path is workspace-relative; diff is the rendered
// unified-diff body (already including the "@@ path @@" header). An
// empty diff means the tool reported "wrote N bytes" but the file
// content matched the pre-image — Recorder elides empty diffs before
// firing.
type DiffSink func(path, diff string)

// DiffEvent carries the rendered diff plus the before/after file images used to
// produce it. Consumers that only need display can keep using [DiffSink]; richer
// consumers (checkpoints/rollback) can use [EventSink].
type DiffEvent struct {
	Path          string
	Diff          string
	Before        []byte
	BeforeMissing bool
	After         []byte
	AfterMissing  bool
}

// EventSink receives a rich diff event for one file each time a Recordable tool
// finishes and actually changes content.
type EventSink func(DiffEvent)

// Recorder is a passive diff-capturing wrapper around a tool source.
// Construct one per shell — it has no per-turn state beyond the
// transient pre-image used inside a single Execute call.
type Recorder struct {
	base       tools.Source
	classifier Classifier
	wsRoot     string
	onEvent    EventSink
}

// New returns a Recorder that wraps base. wsRoot resolves relative
// tool-call paths to absolute ones for snapshot read. classifier
// decides which tool names trigger diff capture; pass [NewClassifier]
// for the default set (write / edit / write_append). onDiff may be
// nil — Recorder will run but silently drop captures.
func New(base tools.Source, wsRoot string, classifier Classifier, onDiff DiffSink) *Recorder {
	var onEvent EventSink
	if onDiff != nil {
		onEvent = func(e DiffEvent) { onDiff(e.Path, e.Diff) }
	}
	return NewWithEventSink(base, wsRoot, classifier, onEvent)
}

// NewWithEventSink returns a Recorder that emits rich diff events including the
// pre/post file image for checkpoint consumers.
func NewWithEventSink(base tools.Source, wsRoot string, classifier Classifier, onEvent EventSink) *Recorder {
	return &Recorder{
		base:       base,
		classifier: classifier,
		wsRoot:     wsRoot,
		onEvent:    onEvent,
	}
}

// Tools forwards to the wrapped source unchanged.
func (r *Recorder) Tools(ctx context.Context) iter.Seq[tools.Tool] { return r.base.Tools(ctx) }

// Execute classifies the call and routes it:
//
//   - [Pure] and unclassified tools pass straight through. No
//     overhead beyond a map lookup.
//   - [Recordable] tools have their target path snapshotted before
//     the underlying tool runs; afterwards the post-image is read,
//     diffed against the snapshot, and pushed to OnDiff (if non-empty).
//
// Snapshot or read failures are logged into the returned ToolResult's
// Error field on a best-effort basis but never block the underlying
// tool from running — observability must not break the agent.
func (r *Recorder) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	if r.classifier.Classify(call.ToolName) != Recordable {
		return r.base.Execute(ctx, call)
	}

	// apply_patch can mutate multiple files in one call — the
	// single-"path"-argument extractor doesn't fit. Snapshot every
	// path declared by the patch envelope, run, diff each.
	if call.ToolName == code.ToolNameApplyPatch {
		return r.executeMultiPath(ctx, call, code.PatchPaths(call.Arguments.String("patch", "")))
	}

	rel, abs := extractPath(call, r.wsRoot)
	if abs == "" {
		// No path argument we can snapshot; just run the tool. The
		// tool's own validation will surface any path error.
		return r.base.Execute(ctx, call)
	}
	before, beforeMissing := readSnapshot(abs)

	res, err := r.base.Execute(ctx, call)

	if r.onEvent != nil {
		after, afterMissing := readSnapshot(abs)
		diff := unifiedDiff(rel, before, beforeMissing, after, afterMissing)
		if diff != "" {
			r.onEvent(DiffEvent{
				Path:          rel,
				Diff:          diff,
				Before:        before,
				BeforeMissing: beforeMissing,
				After:         after,
				AfterMissing:  afterMissing,
			})
		}
	}
	return res, err
}

// executeMultiPath snapshots a list of workspace-relative paths,
// dispatches the tool, then diffs each path's post-image against
// its pre-image. Used today only by apply_patch; the shape extends
// to any future tool that mutates a known list of files in one call.
//
// An empty paths list collapses to pass-through (no observation,
// tool still runs) — apply_patch surfaces its own "patch text is
// empty" / parse-error message; we don't preempt it.
func (r *Recorder) executeMultiPath(
	ctx context.Context,
	call tools.ToolCall,
	paths []string,
) (*tools.ToolResult, error) {
	if len(paths) == 0 {
		return r.base.Execute(ctx, call)
	}

	type snapshot struct {
		rel     string
		abs     string
		body    []byte
		missing bool
	}
	pre := make([]snapshot, 0, len(paths))
	for _, raw := range paths {
		abs, err := resolveUnderRoot(r.wsRoot, raw)
		if err != nil {
			// Path escapes the workspace — skip its snapshot but keep
			// scanning the rest. The tool will fail on its own and the
			// user gets a clear message from apply_patch's parser.
			continue
		}
		rel := raw
		if rp, err := filepath.Rel(r.wsRoot, abs); err == nil {
			rel = rp
		}
		body, missing := readSnapshot(abs)
		pre = append(pre, snapshot{rel: rel, abs: abs, body: body, missing: missing})
	}

	res, err := r.base.Execute(ctx, call)

	if r.onEvent != nil {
		for _, p := range pre {
			after, afterMissing := readSnapshot(p.abs)
			diff := unifiedDiff(p.rel, p.body, p.missing, after, afterMissing)
			if diff != "" {
				r.onEvent(DiffEvent{
					Path:          p.rel,
					Diff:          diff,
					Before:        p.body,
					BeforeMissing: p.missing,
					After:         after,
					AfterMissing:  afterMissing,
				})
			}
		}
	}
	return res, err
}

// --- snapshot internals ---

// readSnapshot returns the file content and a flag indicating whether
// the file was missing. Returns nil + true on ENOENT, nil + false on
// read errors (logged as warnings). We elide the diff rather than
// breaking the loop.
func readSnapshot(abs string) ([]byte, bool) {
	data, err := os.ReadFile(abs)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, true
	case err != nil:
		slog.Warn("diffrecorder: failed to read snapshot", "path", abs, "err", err)
		return nil, false
	}
	return data, false
}

// extractPath pulls the path argument out of a tool call's arguments
// and resolves it against the workspace root. Returns ("", "") when
// the call has no "path" argument or the resolved path escapes the
// root.
func extractPath(call tools.ToolCall, root string) (string, string) {
	var rel, abs string
	raw, ok := call.Arguments["path"].(string)
	if !ok || raw == "" {
		return "", ""
	}
	abs, err := resolveUnderRoot(root, raw)
	if err != nil {
		return "", ""
	}
	if r, err := filepath.Rel(root, abs); err == nil {
		rel = r
	} else {
		rel = raw
	}
	return rel, abs
}

// resolveUnderRoot normalises a tool-supplied path and enforces
// that the result stays inside the workspace. Absolute paths are
// cleaned and checked directly; relative paths are joined onto
// root (which itself cleans) and then checked the same way.
//
// The containment check has to run on the post-join result, not
// just the absolute branch: `filepath.Join(root, "../../etc/passwd")`
// returns `/etc/passwd`, so without re-validating, a model-supplied
// `..`-walk silently escaped the workspace boundary and the
// snapshot read landed on whatever sat at the resolved path. Both
// branches now feed the same lexical containment test.
//
// root is cleaned once so the prefix comparison can't be fooled by
// a trailing slash or unclean caller input.
func resolveUnderRoot(root, p string) (string, error) {
	cleanRoot := filepath.Clean(root)
	var clean string
	if filepath.IsAbs(p) {
		clean = filepath.Clean(p)
	} else {
		clean = filepath.Join(cleanRoot, p)
	}
	sep := string(filepath.Separator)
	if clean != cleanRoot && !strings.HasPrefix(clean+sep, cleanRoot+sep) {
		return "", fmt.Errorf("path %q escapes workspace", p)
	}
	return clean, nil
}
