package engine

import (
	"context"
	"log/slog"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/coderunner"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/db"
)

// headlessRecorder persists a headless run's lifecycle to the
// headless_runs table: a row at start, progress counters after each
// iteration (so a SIGKILL'd run still shows how far it got), and the
// terminal summary on completion. Persistence is best-effort — a store
// failure logs but never aborts the run — and a nil recorder is a no-op,
// so a LiveRunner without a configured store still runs headless.
type headlessRecorder struct {
	store     *db.Store
	id        string
	workspace string
	base      string // HEAD at start — the diff baseline, captured before the agent can move HEAD
	startedAt time.Time
}

// newHeadlessRecorder returns a recorder bound to the run id, or nil when
// no store is configured.
func (l *LiveRunner) newHeadlessRecorder(id string) *headlessRecorder {
	if l == nil || l.settings == nil || l.settings.Store == nil {
		return nil
	}
	return &headlessRecorder{
		store:     l.settings.Store,
		id:        id,
		workspace: l.ws.Root(),
	}
}

// start inserts the initial row, capturing the base commit so the eval
// framework can diff against it later. A row with ended_at NULL marks a
// run that started but hasn't finished.
func (r *headlessRecorder) start(ctx context.Context, prompt, provider, model string) {
	if r == nil {
		return
	}
	r.startedAt = time.Now()
	r.base = code.GitHead(ctx, r.workspace) // baseline now, before the agent can move HEAD
	err := r.store.InsertHeadlessRun(ctx, db.HeadlessRunStart{
		ID:         r.id,
		Workspace:  r.workspace,
		BaseCommit: r.base,
		Prompt:     prompt,
		StartedAt:  r.startedAt,
		Provider:   provider,
		Model:      model,
	})
	if err != nil {
		slog.WarnContext(ctx, "headless: insert run row", "id", r.id, "err", err)
	}
}

// progress is the runner.ProgressUpdater: it persists the live counters
// after each iteration so a killed run leaves a trail of real progress.
func (r *headlessRecorder) progress(ctx context.Context, iter, toolCalls int) {
	if r == nil {
		return
	}
	if err := r.store.UpdateHeadlessRunProgress(ctx, r.id, iter, toolCalls); err != nil {
		slog.WarnContext(ctx, "headless: update progress", "id", r.id, "iter", iter, "err", err)
	}
}

// complete writes the terminal summary, including the final worktree
// diff. Runs on a detached context so a cancelled run still records its
// outcome.
func (r *headlessRecorder) complete(ctx context.Context, res runner.TaskResult) {
	if r == nil {
		return
	}
	ctx = context.WithoutCancel(ctx)
	summary := db.HeadlessRunSummary{
		EndedAt:        time.Now(),
		TerminalReason: string(res.Reason),
		FinalContent:   res.FinalContent,
		FinalDiff:      code.WorktreeDiff(ctx, r.workspace, r.base, nil),
		Iterations:     res.Iterations,
		ToolCalls:      coderunner.ToolCallCount(res.Messages),
		Duration:       time.Since(r.startedAt),
	}
	if res.Err != nil {
		summary.Error = res.Err.Error()
	}
	if u := res.TotalUsage; u != nil {
		in, out := int64(u.PromptTokens), int64(u.CompletionTokens)
		summary.TokensIn, summary.TokensOut = &in, &out
	}
	if err := r.store.CompleteHeadlessRun(ctx, r.id, summary); err != nil {
		slog.WarnContext(ctx, "headless: complete run row", "id", r.id, "err", err)
	}
}
