package code

import (
	"context"
	"fmt"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// UpdatePlanTool is the structured-plan tracker. It coexists with
// save_plan: save_plan persists the narrative markdown archive,
// update_plan owns the live, mutating step list rendered in the plan
// dock tab.
//
// API mirrors Codex's update_plan verbatim: every call carries the
// FULL plan (steps + statuses). No partial-update semantics — the
// model resends the whole list each time. This is what GPT-5 family
// models are trained on, and it's easier to reason about (no
// step-id thread to maintain across calls).
//
// Sequence-of-call expectations:
//
//   - PLAN mode end: call update_plan once with all steps at
//     "pending" to seed the structured list alongside the markdown
//     plan save_plan persisted.
//   - BUILD mode, starting work: call update_plan with the same list,
//     flipping one step to "in_progress".
//   - BUILD mode, finishing a step: call update_plan again with that
//     step at "completed" and (optionally) the next one
//     "in_progress".
//
// The prompt enforces this; the tool itself is permissive — it
// accepts any plan shape, since the model occasionally
// rearranges/extends steps mid-task and we'd rather see the rework
// than reject it.
type UpdatePlanTool struct {
	store PlanStore
}

// UpdatePlanStepArg is one step in the typed update_plan payload.
// Mirrors the JSON Schema's per-item object {step, status}. Status is
// kept as a string here — the strongly-typed StepStatus enum lives
// in the plan-store layer and ParseStepStatus does the conversion.
type UpdatePlanStepArg struct {
	Step   string `json:"step" doc:"The step description."`
	Status string `json:"status" enum:"pending,in_progress,completed" doc:"One of pending, in_progress, or completed."`
}

// UpdatePlanArgs is the typed argument struct UpdatePlanTool.Execute
// decodes into via tools.DecodeArgs. The `plan` array decodes
// directly into a typed slice — no .([]any) / .(map[string]any)
// dance, no per-field type assertions.
type UpdatePlanArgs struct {
	Plan        []UpdatePlanStepArg `json:"plan" doc:"Ordered list of steps. Each step has step (the description) and status (pending | in_progress | completed)."`
	Explanation string              `json:"explanation,omitempty" doc:"Optional one-liner explaining the change (e.g. \"split step 2 into 2a/2b after reading the test file\"). Shown in the plan pane next to the steps."`
}

// NewUpdatePlanTool returns a tool bound to store. Caller is
// responsible for the broadcast behaviour of store.SetPlan (in
// production the shellModel implementation pushes a tea.Msg to the
// TUI).
func NewUpdatePlanTool(store PlanStore) *UpdatePlanTool {
	return &UpdatePlanTool{store: store}
}

// Definition advertises update_plan taking the full plan array
// ({step, status} items, status enum pending|in_progress|completed)
// plus an optional explanation; Mutates is left unset — the plan lives
// in the store, not in workspace files.
func (t *UpdatePlanTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        ToolNameUpdatePlan,
		Description: "Update the live, structured plan rendered in the zarlcode plan pane. Send the FULL plan each call — this tool replaces the prior plan wholesale, not incrementally. Use after producing a markdown plan in PLAN mode to seed the structured list, then again in BUILD mode each time a step's status changes. Step statuses are 'pending', 'in_progress', or 'completed'.",
		Parameters:  tools.SchemaFor[UpdatePlanArgs](),
	}
}

// Execute requires a non-empty plan array, trims each step, treats an
// omitted status as pending, and parses statuses via ParseStepStatus —
// the first empty step or unknown status fails the whole call. On
// success it replaces the stored plan wholesale and returns per-status
// counts.
func (t *UpdatePlanTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	args, derr := tools.DecodeArgs[UpdatePlanArgs](call.Arguments)
	if derr != nil {
		return tools.Failure(call.ID, derr), nil
	}
	if len(args.Plan) == 0 {
		return tools.Failure(call.ID, tools.Validation("update_plan", "plan array required")), nil
	}

	steps := make([]PlanStep, 0, len(args.Plan))
	for i, item := range args.Plan {
		text := strings.TrimSpace(item.Step)
		if text == "" {
			return tools.Failure(
				call.ID,
				tools.Validation("update_plan", fmt.Sprintf("step %d has empty `step`", i+1)),
			), nil
		}
		// Normalise raw model output before parsing: trim surrounding
		// whitespace, fold case, and treat an omitted status as pending.
		// ParseStepStatus itself is the generated exact-match parser
		// (with the in-progress/done aliases baked in).
		raw := strings.ToLower(strings.TrimSpace(item.Status))
		if raw == "" {
			raw = "pending"
		}
		status, err := ParseStepStatus(raw)
		if err == nil {
			steps = append(steps, PlanStep{Text: text, Status: status})
			continue
		}
		// Unknown status is surfaced as a tool-result validation failure, not a
		// Go error — the dispatch layer feeds it back to the model.
		return tools.Failure(call.ID, tools.Validation("update_plan", fmt.Sprintf("step %d: unknown step status %q (want pending|in_progress|completed)", i+1, item.Status))), nil
	}

	plan := Plan{Steps: steps, Explanation: strings.TrimSpace(args.Explanation)}
	t.store.SetPlan(plan)

	// Summary for the tool result: counts per status. Concise — the
	// model is going to call this often and a fat success message
	// just costs tokens.
	var pending, inProgress, completed int
	for _, s := range steps {
		switch s.Status {
		case StepStatuses.PENDING:
			pending++
		case StepStatuses.INPROGRESS:
			inProgress++
		case StepStatuses.COMPLETED:
			completed++
		}
	}
	return tools.Success(call.ID, fmt.Sprintf("plan updated: %d step(s) — %d pending, %d in_progress, %d completed",
		len(steps), pending, inProgress, completed)), nil
}
