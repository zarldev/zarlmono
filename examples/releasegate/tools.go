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

type setCheckArgs struct {
	Name     string `json:"name" enum:"tests,changelog,rollback_plan" doc:"Release gate check name."`
	OK       bool   `json:"ok" doc:"Whether the check passes."`
	Evidence string `json:"evidence" doc:"Short evidence explaining the check state."`
}

type writeNotesArgs struct {
	Summary  string `json:"summary" doc:"Short release summary."`
	Risk     string `json:"risk" doc:"Known risks and mitigations."`
	Rollback string `json:"rollback" doc:"Rollback plan."`
}

type publishArgs struct {
	Channel string `json:"channel" enum:"staging,production" doc:"Publish channel."`
}

func newSetCheckTool(r *Release) tools.Tool {
	return tools.NewTyped(
		tools.ToolSpec{
			Name:        ToolSetCheck,
			Description: "Mark a release gate check as passing or failing, with short evidence.",
			Parameters:  tools.SchemaFor[setCheckArgs](),
		},
		func(_ context.Context, args setCheckArgs) (string, error) {
			if strings.TrimSpace(args.Evidence) == "" {
				return "", tools.Validation(ToolSetCheck.String(), "evidence must explain why the check is true or false")
			}
			if err := r.SetCheck(args.Name, args.OK, args.Evidence); err != nil {
				return "", tools.Validation(ToolSetCheck.String(), err.Error())
			}
			return fmt.Sprintf("%s=%t (%s)", args.Name, args.OK, args.Evidence), nil
		},
	)
}

func newWriteNotesTool(r *Release) tools.Tool {
	return tools.NewTyped(
		tools.ToolSpec{
			Name:        ToolWriteNotes,
			Description: "Write structured release notes. A post-call guardrail approves or rejects their quality.",
			Parameters:  tools.SchemaFor[writeNotesArgs](),
		},
		func(_ context.Context, args writeNotesArgs) (ReleaseNotes, error) {
			notes := ReleaseNotes(args)
			r.SetNotes(notes)
			return notes, nil
		},
	)
}

func newPublishTool(r *Release) tools.Tool {
	return tools.NewTyped(
		tools.ToolSpec{
			Name:        ToolPublish,
			Description: "Publish the release after every release gate requirement passes.",
			Parameters:  tools.SchemaFor[publishArgs](),
		},
		func(_ context.Context, args publishArgs) (string, error) {
			r.Publish(args.Channel)
			return "release published to " + args.Channel, nil
		},
	)
}

func noArgsSchema() llm.Schema {
	return tools.SchemaFor[struct{}]()
}
