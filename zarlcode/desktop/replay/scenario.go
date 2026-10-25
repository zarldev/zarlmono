package replay

import (
	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func scenario() []any {
	const root = "demo-turn"
	const child = "demo-tests"
	const (
		readTool       = "read"
		agentTool      = "agent"
		editTool       = "edit"
		testTool       = "test"
		spawnExecution = "spawn"
	)
	plan := func(stage int) transcript.PlanUpdated {
		steps := []code.PlanStep{
			{Text: "Inspect the retry policy and callers", Status: code.StepStatuses.PENDING},
			{Text: "Bound backoff and respect cancellation", Status: code.StepStatuses.PENDING},
			{Text: "Verify retry and cancellation contracts", Status: code.StepStatuses.PENDING},
		}
		for i := range steps {
			if i < stage {
				steps[i].Status = code.StepStatuses.COMPLETED
			} else if i == stage {
				steps[i].Status = code.StepStatuses.INPROGRESS
			}
		}
		return transcript.PlanUpdated{TurnID: root, Plan: code.Plan{Steps: steps}}
	}
	return []any{
		transcript.UserSubmitted{Text: "Make the retry loop respect cancellation and cap exponential backoff. Keep the change focused and verify the behavior."},
		transcript.TurnStarted{TurnID: root},
		transcript.AssistantDelta{TurnID: root, Delta: "I’ll inspect the retry policy first, "},
		transcript.AssistantDelta{TurnID: root, Delta: "then make the smallest change that bounds the delay and respects cancellation.\n\n"},
		transcript.AssistantDelta{TurnID: root, Delta: "Verification will cover both normal retries and a cancelled wait."},
		plan(0),
		transcript.ToolStarted{TurnID: root, ExecutionID: "read-main", ToolID: readTool, Name: readTool, Argument: `{"path":"internal/retry/retry.go"}`},
		transcript.ToolFinished{ExecutionID: "read-main", ToolID: readTool, Effect: "package retry\n\nfunc wait(attempt int) {\n    time.Sleep(time.Second << attempt)\n}\n\n// Untrusted strings must stay text:\n// <img src=x onerror=alert('unsafe')>", DurationMS: 12},
		transcript.ToolStarted{TurnID: root, ExecutionID: spawnExecution, ToolID: agentTool, Name: "agent_spawn", Argument: `{"agent":"tester","mode":"verify"}`},
		transcript.SubagentStarted{TurnID: child, SpawnExecutionID: spawnExecution, SpawnToolID: agentTool, AgentName: "tester", Provider: "fixture", Model: "replay", Prompt: "Inspect cancellation and backoff coverage. Do not edit files."},
		transcript.AssistantDelta{TurnID: child, Delta: "Checking existing tests for cancellation while sleeping and the maximum delay."},
		transcript.ToolStarted{TurnID: child, ExecutionID: "read-child", ToolID: readTool, Name: readTool, Argument: `{"path":"internal/retry/retry_test.go"}`},
		transcript.ToolFinished{ExecutionID: "read-child", ToolID: readTool, Effect: "Existing: success after transient failure.\nMissing: cancellation during wait; capped delay for high attempt counts.", DurationMS: 8},
		plan(1),
		transcript.AssistantDelta{TurnID: root, Delta: "The current wait ignores context and shifts without a bound. I’ll use a capped delay and a cancellable timer."},
		transcript.ToolStarted{TurnID: root, ExecutionID: editTool, ToolID: editTool, Name: editTool, Argument: `{"path":"internal/retry/retry.go"}`},
		transcript.DiffAdded{TurnID: root, Path: "internal/retry/retry.go", Diff: "--- a/internal/retry/retry.go\n+++ b/internal/retry/retry.go\n@@ -1,5 +1,14 @@\n package retry\n \n-func wait(attempt int) {\n-    time.Sleep(time.Second << attempt)\n+func wait(ctx context.Context, attempt uint) error {\n+    delay := min(time.Second << min(attempt, 5), 30*time.Second)\n+    timer := time.NewTimer(delay)\n+    defer timer.Stop()\n+    select {\n+    case <-ctx.Done():\n+        return ctx.Err()\n+    case <-timer.C:\n+        return nil\n+    }\n }"},
		transcript.ToolFinished{ExecutionID: editTool, ToolID: editTool, Effect: "Updated internal/retry/retry.go", DurationMS: 21},
		transcript.AssistantDelta{TurnID: child, Delta: "\n\nAdd black-box cases for cancellation during wait and a bounded high-attempt delay."},
		transcript.TurnFinished{TurnID: child},
		transcript.SubagentFinished{TurnID: child, Status: transcript.SubagentCompleted},
		transcript.ToolFinished{ExecutionID: spawnExecution, ToolID: agentTool, Effect: "Tester completed the coverage review.", DurationMS: 4100},
		plan(2),
		transcript.ToolStarted{TurnID: root, ExecutionID: testTool, ToolID: testTool, Name: "bash", Argument: `{"command":"go test -race ./internal/retry/..."}`},
		transcript.ToolFinished{ExecutionID: testTool, ToolID: testTool, Effect: "=== RUN   TestWaitCancellation\n--- PASS: TestWaitCancellation\n=== RUN   TestWaitBound\n--- PASS: TestWaitBound\nok   example/internal/retry   1.024s", DurationMS: 1024},
		plan(3),
		transcript.AssistantDelta{TurnID: root, Delta: "The retry wait now respects cancellation and caps the delay at 30 seconds.\n\n"},
		transcript.AssistantDelta{TurnID: root, Delta: "Focused tests and the race check pass in this scripted example. "},
		transcript.AssistantDelta{TurnID: root, Delta: "No files or commands were executed by the workbench: everything here is replay data."},
		transcript.TurnFinished{TurnID: root},
	}
}
