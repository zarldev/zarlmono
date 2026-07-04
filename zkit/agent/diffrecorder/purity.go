package diffrecorder

import (
	"maps"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// Purity classifies a tool call's interaction with the local
// filesystem. The Recorder consults the classifier on every dispatch
// to decide whether to snapshot the target path before running.
type Purity int

const (
	// Pure tools have no filesystem side effects we care to record
	// (read, ls, grep). Pass through unchanged.
	Pure Purity = iota

	// Recordable tools mutate a file at a known "path" argument.
	// Pre-image is snapshotted, the tool runs, post-image is read,
	// and a unified diff is fired via the recorder's OnDiff callback.
	Recordable
)

// String returns the lowercase classification label — "pure" or
// "recordable" — and "unknown" for out-of-range values.
func (p Purity) String() string {
	switch p {
	case Pure:
		return "pure"
	case Recordable:
		return "recordable"
	}
	return "unknown"
}

// Classifier maps a tool name to its [Purity]. Unknown names default
// to [Pure] — observability is opt-in: unless we know how to extract
// the path for snapshotting, we leave the tool alone.
type Classifier struct {
	overrides map[tools.ToolName]Purity
}

// NewClassifier returns the default classifier — write / edit /
// write_append / apply_patch are Recordable; the rest are Pure
// (passthrough). apply_patch is multi-path (the Recorder special-
// cases it to snapshot every file the patch touches); the others
// use the single "path" argument.
func NewClassifier() Classifier {
	return Classifier{
		overrides: map[tools.ToolName]Purity{
			code.ToolNameWrite:       Recordable,
			code.ToolNameEdit:        Recordable,
			code.ToolNameWriteAppend: Recordable,
			code.ToolNameApplyPatch:  Recordable,
		},
	}
}

// WithOverride returns a classifier where name maps to p. Useful when
// a workspace ships custom tools the default registry doesn't know
// about — register them as Recordable so the Files pane picks up
// their diffs.
func (c Classifier) WithOverride(name tools.ToolName, p Purity) Classifier {
	out := Classifier{overrides: make(map[tools.ToolName]Purity, len(c.overrides)+1)}
	maps.Copy(out.overrides, c.overrides)
	out.overrides[name] = p
	return out
}

// Classify returns the purity for the given tool name. Unrecognised
// names return [Pure] — the safest default for an observability
// layer: don't try to capture a diff we don't know how to compute.
func (c Classifier) Classify(name tools.ToolName) Purity {
	if p, ok := c.overrides[name]; ok {
		return p
	}
	return Pure
}
