package engine

import (
	"context"
	"fmt"
	"iter"

	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	boardtools "github.com/zarldev/zarlmono/zkit/agent/tools/board"
	programtools "github.com/zarldev/zarlmono/zkit/agent/tools/program"
	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	computertools "github.com/zarldev/zarlmono/zkit/ai/tools/computer"
	"github.com/zarldev/zarlmono/zkit/ai/tools/dynamic"
	"github.com/zarldev/zarlmono/zkit/ai/tools/fetch"
	"github.com/zarldev/zarlmono/zkit/ai/tools/search"
)

var planAllowedToolNames = [...]tools.ToolName{
	code.ToolNameRead,
	code.ToolNameGrep,
	code.ToolNameLs,
	code.ToolNameGlob,
	code.ToolNameFileMap,
	code.ToolNameRetrieveCode,
	code.ToolNameSavePlan,
	code.ToolNameSavePlanAppend,
	// update_plan mutates only the structured plan store, not the workspace,
	// and the tool's own contract is to seed the step list at PLAN-mode end.
	code.ToolNameUpdatePlan,
	search.ToolName,
	fetch.ToolName,
	ToolNameLoadSkill,
	ToolNameListSkills,
	ToolNameListAgents,
	ToolNameListInstructions,
	ToolNameLoadInstruction,
	programtools.ToolName,
	programtools.ToolNameOutputRead,
	computertools.ToolNameComputerObserve,
	dynamic.ToolNameMCPList,
	// Shell commands are limited to read-only investigation by the Plan prompt;
	// this name-based gate is not a command-level read-only sandbox.
	code.ToolNameBash,
	code.ToolNameStopProcess,
	code.ToolNameBashOutput,
	code.ToolNameListProcesses,
	spawn.ToolNameAgentSpawn,
	spawn.ToolNameAgentStatus,
	spawn.ToolNameAgentAwait,
	spawn.ToolNameAgentStop,
	ToolNameSetMode,
	spawn.ToolNameListAgentTasks,
	// Host-bound board tools share bounded in-process findings and can wake
	// existing peers; they grant no workspace, process, or network authority.
	boardtools.PostName,
	boardtools.ListName,
	boardtools.ReadName,
}

var planAllowedTools = func() map[tools.ToolName]struct{} {
	allowed := make(map[tools.ToolName]struct{}, len(planAllowedToolNames))
	for _, name := range planAllowedToolNames {
		allowed[name] = struct{}{}
	}
	return allowed
}()

// PlanAllows reports whether a tool passes the PLAN-mode dispatch gate, not
// whether it is advertised. Shell command intent is governed by the Plan prompt
// and independent guardrails, not enforced here.
func PlanAllows(name tools.ToolName) bool {
	_, ok := planAllowedTools[name]
	return ok
}

// semanticHostToolNames returns the stable host names that must remain reserved
// even when their optional local implementation is disabled for this turn.
func semanticHostToolNames() []tools.ToolName {
	names := make([]tools.ToolName, 0, len(planAllowedToolNames)+1)
	names = append(names, planAllowedToolNames[:]...)
	return append(names, computertools.ToolNameComputerAct)
}

func isSemanticHostTool(name tools.ToolName) bool {
	return PlanAllows(name) || name == computertools.ToolNameComputerAct
}

// modeFilteredSource keeps the advertised tool surface stable across workflow
// modes while gating dispatch. The live closure makes mid-run toggles take
// effect on the next dispatch without changing tool definitions or their order.
type modeFilteredSource struct {
	inner    tools.Source
	plan     func() bool
	readOnly bool // immutable authority ceiling; unlike workflow Plan, forbids shell access
}

// NewModeFilteredSource preserves inner's tool definitions in both modes and
// restricts PLAN dispatch to PlanAllows. Independent authority gates still apply.
func NewModeFilteredSource(inner tools.Source, plan func() bool) *modeFilteredSource {
	return &modeFilteredSource{inner: inner, plan: plan}
}

// Tools forwards the unchanged inner surface so mode switches do not invalidate
// the tool-schema cache prefix. Execution restrictions live in Execute.
func (s *modeFilteredSource) Tools(ctx context.Context) iter.Seq[tools.Tool] {
	return s.inner.Tools(ctx)
}

// Execute dispatches iff the tool is allowed in the current mode. A blocked
// call returns a sentinel error the runner surfaces to the model as the
// call's result, so it can react (announce "switch to BUILD") rather than
// silently retrying.
func (s *modeFilteredSource) Execute(ctx context.Context, c tools.ToolCall) (*tools.ToolResult, error) {
	if s.readOnly && (c.ToolName == code.ToolNameBash || c.ToolName == code.ToolNameStopProcess) {
		return nil, tools.Permission(c.ToolName.String(), "tool is blocked by the inspect-only authority ceiling")
	}
	if s.plan() && !PlanAllows(c.ToolName) {
		return nil, fmt.Errorf(
			"%q is not callable in PLAN mode — use set_mode to enter Build only for an authorized implementation task",
			c.ToolName)
	}
	return s.inner.Execute(ctx, c)
}

// ForgetTask forwards task lifecycle cleanup through the mode filter.
func (s *modeFilteredSource) ForgetTask(id taskscope.ID) {
	if forgetter, ok := s.inner.(interface{ ForgetTask(taskscope.ID) }); ok {
		forgetter.ForgetTask(id)
	}
}

// reservedRemoteSource prevents an external source from claiming stable host
// names. Local tools are composed separately and are never filtered here.
type reservedRemoteSource struct {
	inner tools.Source
}

func newReservedRemoteSource(inner tools.Source) tools.Source {
	return &reservedRemoteSource{inner: inner}
}

func (s *reservedRemoteSource) Tools(ctx context.Context) iter.Seq[tools.Tool] {
	return func(yield func(tools.Tool) bool) {
		for tool := range s.inner.Tools(ctx) {
			if isSemanticHostTool(tool.Definition().Name) {
				continue
			}
			if !yield(tool) {
				return
			}
		}
	}
}

func (s *reservedRemoteSource) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	if isSemanticHostTool(call.ToolName) {
		return nil, tools.Permission(call.ToolName.String(), "tool name is reserved by the host")
	}
	return s.inner.Execute(ctx, call)
}

func (s *reservedRemoteSource) ForgetTask(id taskscope.ID) {
	if forgetter, ok := s.inner.(interface{ ForgetTask(taskscope.ID) }); ok {
		forgetter.ForgetTask(id)
	}
}
