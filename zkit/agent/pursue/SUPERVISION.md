# Owned pursuit

`pursue.Drive` remains a bounded decision loop. Its supervision grace can expire before an attempt returns. Code must not close dependencies merely because that call returned.

`pursue.Supervisor` adds an explicit one-use owner:

```go
owner := pursue.NewSupervisor()
outcome := owner.Drive(ctx, request, opts...)
// Ownership must be retained if a bounded Join times out.
err := owner.Join(cleanupCtx)
settled, available := owner.LastResult()
```

- `Drive` registers before launching, runs the decision loop, and seals/cancels admission on return.
- `Stop` is idempotent and cancels both Drive and owner-created polling work.
- `Join` waits for all registered attempts, the decision loop and owned probes. A caller deadline bounds only that wait; another Join can complete the same drain. Keep borrowed dependencies alive until joining succeeds.
- `LastResult` returns a detached snapshot of the latest actual settled attempt, including history, usage and timing. It never turns a timeout without a result into settled evidence. The decision outcome and settled evidence are separate: retain the decision error when overlaying a late result.
- `Supervisor.PollWatcher` binds probes to operation and owner cancellation, serializes invocations of the same returned watcher across retries, and joins cancellation callbacks. Separate factories do not serialize an intentionally shared external probe.
- Ordinary external `Request.Watcher` implementations remain externally owned. The supervisor cannot join a goroutine for which no completion contract was supplied.
- Attempt and probe callbacks must not panic; translate panics at the host boundary when recovery is required. They must cooperate with cancellation for bounded shutdown. Go cannot safely kill a goroutine; ownership is retained if a callback remains blocked.

The headless engine uses this owner instead of its former local admission mutex/wait-group/result tracking. It retains panic-to-result translation, joins before closing dependencies, and bounds its Git worktree-state probe independently of the command-verification timeout.

This is not a retry scheduler or a worker pool. It preserves existing goal evaluation, attempt limits, threading and bounded-grace decisions while making execution lifetime explicit.
