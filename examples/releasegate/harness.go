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

const systemPrompt = `You are the release captain for a small software project.

This is a local documentation demo, not a real deployment. Your job is to drive
the provided tools until release v1.2.3 is published to production.

Use the release gate honestly but pragmatically:
  - inspect state with release_status when useful;
  - mark tests, changelog, and rollback_plan true with short evidence;
  - write concrete release notes with summary, risk, and rollback sections;
  - publish only to production after the gate allows it.

If a tool is rejected, treat the rejection as feedback, fix the missing item,
and continue. Do not finish with prose until the release is published.`

const goalPrompt = "Publish release v1.2.3 to production once it is safe. This is the demo release for the guarded-runner documentation example."

// RunReleaseGate assembles the whole example: JSON-schema validation, a
// pre-call publish gate, a post-call notes-quality rail, the runner, and the
// harness oracle that verifies the world state rather than trusting the model.
func RunReleaseGate(ctx context.Context, client runner.Client, rel *Release, maxAttempts int) pursue.Outcome {
	reg := tools.NewRegistry(
		statusTool{r: rel},
		newSetCheckTool(rel),
		newWriteNotesTool(rel),
		newPublishTool(rel))

	source := guardrails.NewGuardedSource(reg,
		guardrails.NewSchemaGuardrail(reg),
		releaseReadyGuardrail{r: rel},
		notesQualityGuardrail{r: rel},
	)

	r := runner.New(client, runner.WithTools(source),
		runner.WithPromptText(systemPrompt),
	)

	goal, watcher := pursue.UntilFunc(rel.Published, func(_ context.Context, _ pursue.Attempt) string {
		s := rel.Snapshot()
		if len(s.Missing) > 0 {
			return "Release is not ready. Missing: " + joinMissing(s.Missing) + ". Inspect status, fix the gate, then publish to production."
		}
		return "Release gate is green but production is not published yet. Call release_publish with channel=production."
	})

	return pursue.Drive(ctx, pursue.NewRequest(r.Run, runner.TaskSpec{ID: "releasegate", Prompt: goalPrompt}, pursue.WithGoal(goal), pursue.WithWatcher(watcher)),
		pursue.WithMaxAttempts(maxAttempts),
		pursue.WithOnAttempt(func(report pursue.AttemptReport) {
			s := rel.Snapshot()
			fmt.Fprintf(os.Stderr, "attempt %d/%d: %s missing=%v published=%t channel=%q\n",
				report.Attempt.Number, maxAttempts, pursue.LabelAttempt(report), s.Missing, s.Published, s.Channel)
		}),
	)
}

func joinMissing(missing []string) string {
	if len(missing) == 0 {
		return "nothing"
	}
	return fmt.Sprint(missing)
}
