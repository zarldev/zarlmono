package main

import (
	"context"
	"fmt"
	"os"

	"github.com/zarldev/zarlmono/zkit/agent/guardrails"
	"github.com/zarldev/zarlmono/zkit/agent/pursue"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

const systemPrompt = `You are a health-check operator for a small server farm.

You have one tool:
  check_endpoint(name) — returns healthy, degraded, down, or transient.

Your job is to verify every endpoint (api, db, cache) is healthy. Degraded
endpoints are acceptable but worth noting. Down endpoints mean the check is
not complete — report them and the harness will re-drive.

If a check returns "transient", the failure is temporary. Call check_endpoint
again for that endpoint — the retry should succeed.

Report status after each check. You are done only when all endpoints are
healthy.`

const goalPrompt = "Check all endpoints and confirm the farm is healthy."

// RunHealthCheck assembles the health-check example: a ServerFarm world,
// a single check_endpoint tool, SchemaGuardrail + FanoutGuardrail, a runner,
// and a harness oracle that verifies all endpoints are healthy.
func RunHealthCheck(ctx context.Context, client runner.Client, farm *ServerFarm, maxAttempts int) pursue.Outcome {
	reg := tools.NewRegistry(&checkEndpointTool{f: farm})

	source := guardrails.NewGuardedSource(reg,
		guardrails.NewSchemaGuardrail(reg),
		guardrails.NewFanoutGuardrail(map[tools.ToolName]int{
			ToolCheckEndpoint: 5,
		}),
	)

	r := runner.New(client, runner.WithTools(source),
		runner.WithPromptText(systemPrompt),
	)

	goal, watcher := pursue.UntilFunc(farm.AllHealthy, func(_ context.Context, _ pursue.Attempt) string {
		eps, _ := farm.Snapshot()
		return fmt.Sprintf("Not all endpoints are healthy yet: %v. Keep checking.", eps)
	})

	return pursue.Drive(ctx, pursue.NewRequest(r.Run, runner.TaskSpec{ID: "healthcheck", Prompt: goalPrompt}, pursue.WithGoal(goal), pursue.WithWatcher(watcher)),
		pursue.WithMaxAttempts(maxAttempts),
		pursue.WithOnAttempt(func(report pursue.AttemptReport) {
			eps, checked := farm.Snapshot()
			fmt.Fprintf(os.Stderr, "attempt %d/%d: %s endpoints=%v checked=%v\n",
				report.Attempt.Number, maxAttempts, pursue.LabelAttempt(report), eps, checked)
		}),
	)
}
