package engine

import (
	"context"
	"fmt"
	"iter"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/ai/tools/fetch"
	"github.com/zarldev/zarlmono/zkit/ai/tools/search"
)

// PlanAllows reports whether a tool is callable in PLAN mode — pure read /
// explore / research, plus the path-locked save_plan carve-out so plan mode can
// still produce a markdown artifact. Everything else (bash, write,
// write_append, edit) is blocked: failing closed is the right
// default for a mode whose whole purpose is "don't change anything".
func PlanAllows(name tools.ToolName) bool {
	switch name {
	case code.ToolNameRead,
		code.ToolNameGrep,
		code.ToolNameLs,
		code.ToolNameGlob,
		code.ToolNameSavePlan,
		code.ToolNameSavePlanAppend,
		// update_plan mutates only the structured plan store, not the workspace,
		// and the tool's own contract is to seed the step list at PLAN-mode end —
		// so it's callable here even though everything file-mutating is blocked.
		code.ToolNameUpdatePlan,
		search.ToolName,
		fetch.ToolName,
		ToolNameLoadSkill,
		ToolNameListSkills,
		ToolNameListAgents,
		"spawn_agent":
		return true
	default:
		return false
	}
}

// modeFilteredSource wraps a tools.Source and, when plan() reports PLAN mode,
// elides the blocked tools from the iterated list AND returns a clear error
// if the model dispatches one anyway. It holds the mode as a closure (not a
// captured value) so a mid-run toggle takes effect on the very next dispatch,
// matching the runner's pull-based source contract.
type modeFilteredSource struct {
	inner tools.Source
	plan  func() bool
}

func NewModeFilteredSource(inner tools.Source, plan func() bool) *modeFilteredSource {
	return &modeFilteredSource{inner: inner, plan: plan}
}

// Tools yields every inner tool in BUILD mode, and only the allowlisted ones
// in PLAN mode (registration order preserved — the model sees the same list
// shape, just shorter).
func (s *modeFilteredSource) Tools(ctx context.Context) iter.Seq[tools.Tool] {
	return func(yield func(tools.Tool) bool) {
		plan := s.plan()
		for t := range s.inner.Tools(ctx) {
			if plan && !PlanAllows(t.Definition().Name) {
				continue
			}
			if !yield(t) {
				return
			}
		}
	}
}

// Execute dispatches iff the tool is allowed in the current mode. A blocked
// call returns a sentinel error the runner surfaces to the model as the
// call's result, so it can react (announce "switch to BUILD") rather than
// silently retrying.
func (s *modeFilteredSource) Execute(ctx context.Context, c tools.ToolCall) (*tools.ToolResult, error) {
	if s.plan() && !PlanAllows(c.ToolName) {
		return nil, fmt.Errorf(
			"%q is not callable in PLAN mode — switch to BUILD (shift+tab) to run it",
			c.ToolName)
	}
	return s.inner.Execute(ctx, c)
}
