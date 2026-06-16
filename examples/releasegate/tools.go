package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// The tool names the release-gate agent is given.
const (
	ToolReleaseStatus tools.ToolName = "release_status"
	ToolSetCheck      tools.ToolName = "release_set_check"
	ToolWriteNotes    tools.ToolName = "release_write_notes"
	ToolPublish       tools.ToolName = "release_publish"
)

type statusTool struct{ r *Release }

func (t statusTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        ToolReleaseStatus,
		Description: "Inspect current release gate state: checks, notes approval, missing requirements, and publish status.",
		Parameters:  noArgsSchema(),
	}
}

func (t statusTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	return tools.Success(call.ID, t.r.Snapshot()), nil
}

type setCheckTool struct{ r *Release }

type setCheckArgs struct {
	Name     string `json:"name" enum:"tests,changelog,rollback_plan" doc:"Release gate check name."`
	OK       bool   `json:"ok" doc:"Whether the check passes."`
	Evidence string `json:"evidence" doc:"Short evidence explaining the check state."`
}

func (t setCheckTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        ToolSetCheck,
		Description: "Mark a release gate check as passing or failing, with short evidence.",
		Parameters:  tools.SchemaFor[setCheckArgs](),
	}
}

func (t setCheckTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	var args setCheckArgs
	if err := tools.DecodeArgs(call.Arguments, &args); err != nil {
		return tools.Failure(call.ID, err), nil
	}
	if strings.TrimSpace(args.Evidence) == "" {
		return tools.Failure(call.ID, tools.Validation(ToolSetCheck.String(), "evidence must explain why the check is true or false")), nil
	}
	if err := t.r.SetCheck(args.Name, args.OK, args.Evidence); err != nil {
		return tools.Failure(call.ID, tools.Validation(ToolSetCheck.String(), err.Error())), nil
	}
	return tools.Success(call.ID, fmt.Sprintf("%s=%t (%s)", args.Name, args.OK, args.Evidence)), nil
}

type writeNotesTool struct{ r *Release }

type writeNotesArgs struct {
	Summary  string `json:"summary" doc:"Short release summary."`
	Risk     string `json:"risk" doc:"Known risks and mitigations."`
	Rollback string `json:"rollback" doc:"Rollback plan."`
}

func (t writeNotesTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        ToolWriteNotes,
		Description: "Write structured release notes. A post-call guardrail approves or rejects their quality.",
		Parameters:  tools.SchemaFor[writeNotesArgs](),
	}
}

func (t writeNotesTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	var args writeNotesArgs
	if err := tools.DecodeArgs(call.Arguments, &args); err != nil {
		return tools.Failure(call.ID, err), nil
	}
	notes := ReleaseNotes(args)
	t.r.SetNotes(notes)
	return tools.Success(call.ID, notes), nil
}

type publishTool struct{ r *Release }

type publishArgs struct {
	Channel string `json:"channel" enum:"staging,production" doc:"Publish channel."`
}

func (t publishTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        ToolPublish,
		Description: "Publish the release after every release gate requirement passes.",
		Parameters:  tools.SchemaFor[publishArgs](),
	}
}

func (t publishTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	var args publishArgs
	if err := tools.DecodeArgs(call.Arguments, &args); err != nil {
		return tools.Failure(call.ID, err), nil
	}
	t.r.Publish(args.Channel)
	return tools.Success(call.ID, "release published to "+args.Channel), nil
}

func noArgsSchema() llm.Schema {
	return tools.SchemaFor[struct{}]()
}
