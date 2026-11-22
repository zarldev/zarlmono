package engine

import (
	"bytes"
	"context"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/coderunner"
	"github.com/zarldev/zarlmono/zkit/agent/pursue"
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

	// stateMu owns capture identity and immutable pending snapshots. writeMu
	// serializes checkpoints, but never prevents settlement during database I/O.
	stateMu            sync.Mutex
	writeMu            sync.Mutex
	startRecord        db.HeadlessRunStart
	started            bool
	nextAttempt        int
	lease              headlessAttemptLease
	captureErr         error
	pending            []db.HeadlessProviderAccounting
	pendingAuxiliaries []db.HeadlessAuxiliaryProviderAccounting
	pendingChildren    []db.HeadlessChildProviderAccounting
	revision           uint64
	attempts           []headlessAttemptSnapshot
	completion         *db.HeadlessRunSummary
	completionFrozen   bool
}

type headlessAttemptLease struct {
	number int
	active bool
}

type headlessAttemptSnapshot struct {
	revision uint64
	record   db.HeadlessAttemptRecord
}

const headlessCheckpointTimeout = 5 * time.Second

// newHeadlessRecorder returns a recorder bound to the run id, or nil when
// no store is configured.
func (l *LiveRunner) newHeadlessRecorder(id string) *headlessRecorder {
	if l == nil || l.settings == nil || l.settings.Store == nil {
		return nil
	}
	return NewHeadlessRecorder(l.settings.Store, id, l.ws.Root())
}

// start inserts the initial row, capturing the base commit so the eval
// framework can diff against it later. A row with ended_at NULL marks a
// run that started but hasn't finished.
func (r *headlessRecorder) start(ctx context.Context, prompt, provider, model string) {
	if r == nil {
		return
	}
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	r.stateMu.Lock()
	started := r.started
	r.stateMu.Unlock()
	if !started {
		start := db.HeadlessRunStart{
			ID: r.id, Workspace: r.workspace, Prompt: prompt,
			StartedAt: time.Now(), Provider: provider, Model: model,
			BaseCommit: code.GitHead(ctx, r.workspace),
		}
		r.stateMu.Lock()
		r.startRecord, r.started = start, true
		r.stateMu.Unlock()
	}
	r.flushLocked(ctx, "start")
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

// attempt records one completed pursue/REDRIVE attempt and the decision that
// either accepted it or generated feedback for the next attempt.
func (r *headlessRecorder) attempt(ctx context.Context, report pursue.AttemptReport) {
	if r == nil {
		return
	}
	res := report.Attempt.Result
	rec := db.HeadlessAttemptRecord{
		RunID:          r.id,
		AttemptNumber:  report.Attempt.Number,
		Prompt:         report.Attempt.Spec.Prompt,
		TerminalReason: string(res.Reason),
		FinalContent:   res.FinalContent,
		Iterations:     res.Iterations,
		ToolCalls:      coderunner.ToolCallCount(res.Messages),
		DecisionDone:   report.Decision.Done,
		Feedback:       report.Decision.Feedback,
		RecordedAt:     time.Now(),
	}
	if res.Err != nil {
		rec.Error = res.Err.Error()
	}
	if u := res.TotalUsage; u != nil {
		in, out := int64(u.PromptTokens), int64(u.CompletionTokens)
		rec.TokensIn, rec.TokensOut = &in, &out
	}
	r.stateMu.Lock()
	r.revision++
	snapshot := headlessAttemptSnapshot{revision: r.revision, record: rec}
	index := slices.IndexFunc(r.attempts, func(p headlessAttemptSnapshot) bool {
		return p.record.AttemptNumber == rec.AttemptNumber
	})
	// Pending summaries are a latest-arrival UPSERT per number, not immutable
	// occurrences. Compress only unsaved replacements; a writer's detached
	// snapshot remains eligible to commit, and its exact revision ACK cannot
	// discard a newer arrival. writeMu keeps that older commit before the newer.
	if index < 0 {
		r.attempts = append(r.attempts, snapshot)
	} else {
		r.attempts[index] = snapshot
	}
	r.stateMu.Unlock()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), headlessCheckpointTimeout)
	defer cancel()
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	r.flushLocked(ctx, "attempt")
}

// verifierResult records the structured command-backed oracle result for a
// completed REDRIVE attempt. It can be called before the attempt row itself is
// recorded because pursue evaluates the goal before firing WithOnAttempt.
func (r *headlessRecorder) verifierResult(ctx context.Context, result coderunner.VerifyResult) {
	if r == nil {
		return
	}
	ctx = context.WithoutCancel(ctx)
	rec := db.HeadlessVerifierResultRecord{
		RunID:         r.id,
		AttemptNumber: result.AttemptNumber,
		Command:       result.Command,
		Skipped:       result.Skipped,
		Success:       result.Success,
		Error:         result.Error,
		OutputTail:    result.OutputTail,
		Duration:      result.Duration,
		RecordedAt:    time.Now(),
	}
	if result.ExitCode != nil {
		code := int64(*result.ExitCode)
		rec.ExitCode = &code
	}
	if err := r.store.InsertHeadlessVerifierResult(ctx, rec); err != nil {
		slog.WarnContext(ctx, "headless: insert verifier result", "id", r.id, "attempt", result.AttemptNumber, "err", err)
	}
}

// complete writes the terminal summary, including the final worktree
// diff. Runs on a detached context so a cancelled run still records its
// outcome.
func (r *headlessRecorder) complete(ctx context.Context, res runner.TaskResult) {
	if r == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), headlessCheckpointTimeout)
	defer cancel()
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	r.stateMu.Lock()
	frozen, start := r.completionFrozen, r.startRecord
	r.stateMu.Unlock()
	if !frozen {
		now := time.Now()
		summary := db.HeadlessRunSummary{
			EndedAt: now, TerminalReason: string(res.Reason), FinalContent: res.FinalContent,
			Iterations: res.Iterations, ToolCalls: coderunner.ToolCallCount(res.Messages),
			Duration: now.Sub(start.StartedAt),
		}
		if res.Err != nil {
			summary.Error = res.Err.Error()
		}
		if u := res.TotalUsage; u != nil {
			in, out := int64(u.PromptTokens), int64(u.CompletionTokens)
			summary.TokensIn, summary.TokensOut = &in, &out
		}
		summary.FinalDiff = code.WorktreeDiff(ctx, r.workspace, start.BaseCommit, nil)
		r.stateMu.Lock()
		r.completion, r.completionFrozen = &summary, true
		r.stateMu.Unlock()
	}
	r.flushLocked(ctx, "complete")
}

// flushLocked requires writeMu, snapshots under stateMu and releases it before
// database work. A failed write retains exactly the original timestamps/bytes.
func (r *headlessRecorder) flushLocked(ctx context.Context, checkpoint string) {
	r.stateMu.Lock()
	if r.captureErr != nil {
		r.stateMu.Unlock()
		slog.WarnContext(ctx, "headless: accounting capture rejected", "id", r.id, "checkpoint", checkpoint)
		return
	}
	start := r.startRecord
	attempts := slices.Clone(r.attempts)
	records := make([]db.HeadlessAttemptRecord, len(attempts))
	for i, snapshot := range attempts {
		records[i] = snapshot.record
		records[i].TokensIn = cloneHeadlessTokens(snapshot.record.TokensIn)
		records[i].TokensOut = cloneHeadlessTokens(snapshot.record.TokensOut)
		attempts[i].record = records[i]
	}
	batch := db.HeadlessAccountingBatch{
		Providers:   make([]db.HeadlessProviderAccounting, len(r.pending)),
		Auxiliaries: make([]db.HeadlessAuxiliaryProviderAccounting, len(r.pendingAuxiliaries)),
		Children:    make([]db.HeadlessChildProviderAccounting, len(r.pendingChildren)),
	}
	for i, row := range r.pending {
		batch.Providers[i] = row.Clone()
	}
	for i, row := range r.pendingAuxiliaries {
		batch.Auxiliaries[i] = row.Clone()
	}
	for i, row := range r.pendingChildren {
		batch.Children[i] = row.Clone()
	}
	completionIdentity := r.completion
	var completion *db.HeadlessRunSummary
	if completionIdentity != nil {
		owned := *completionIdentity
		owned.TokensIn = cloneHeadlessTokens(owned.TokensIn)
		owned.TokensOut = cloneHeadlessTokens(owned.TokensOut)
		completion = &owned
	}
	r.stateMu.Unlock()
	if err := r.store.CommitHeadlessRecordingWithAccountingBatch(ctx, start, records, batch, completion); err != nil {
		// Do not log database diagnostics: input payloads and private provider
		// representations must not reach this best-effort warning boundary.
		slog.WarnContext(ctx, "headless: recording checkpoint rejected", "id", r.id, "checkpoint", checkpoint)
		return
	}
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	r.attempts = slices.DeleteFunc(r.attempts, func(p headlessAttemptSnapshot) bool {
		return slices.ContainsFunc(attempts, func(saved headlessAttemptSnapshot) bool {
			return p.revision == saved.revision && equalHeadlessAttempt(p.record, saved.record)
		})
	})
	r.pending = slices.DeleteFunc(r.pending, func(p db.HeadlessProviderAccounting) bool {
		return slices.ContainsFunc(batch.Providers, func(saved db.HeadlessProviderAccounting) bool {
			return p.PursueAttempt == saved.PursueAttempt && p.ProviderOrdinal == saved.ProviderOrdinal &&
				p.Version == saved.Version && bytes.Equal(p.PayloadJSON, saved.PayloadJSON)
		})
	})
	r.pendingAuxiliaries = slices.DeleteFunc(r.pendingAuxiliaries, func(p db.HeadlessAuxiliaryProviderAccounting) bool {
		return slices.ContainsFunc(batch.Auxiliaries, func(saved db.HeadlessAuxiliaryProviderAccounting) bool {
			return p.InvocationID == saved.InvocationID && p.Version == saved.Version && bytes.Equal(p.PayloadJSON, saved.PayloadJSON)
		})
	})
	r.pendingChildren = slices.DeleteFunc(r.pendingChildren, func(p db.HeadlessChildProviderAccounting) bool {
		return slices.ContainsFunc(batch.Children, func(saved db.HeadlessChildProviderAccounting) bool {
			return p.ChildTaskID == saved.ChildTaskID && p.Attempt == saved.Attempt &&
				p.Version == saved.Version && bytes.Equal(p.PayloadJSON, saved.PayloadJSON)
		})
	})
	if r.completion == completionIdentity {
		r.completion = nil
	}
}

func cloneHeadlessTokens(tokens *int64) *int64 {
	if tokens == nil {
		return nil
	}
	owned := *tokens
	return &owned
}

func equalHeadlessAttempt(a, b db.HeadlessAttemptRecord) bool {
	if !equalHeadlessTokens(a.TokensIn, b.TokensIn) || !equalHeadlessTokens(a.TokensOut, b.TokensOut) {
		return false
	}
	a.TokensIn, a.TokensOut, b.TokensIn, b.TokensOut = nil, nil, nil, nil
	return a == b
}

func equalHeadlessTokens(a, b *int64) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

// HeadlessRecorder persists a headless run lifecycle. A nil recorder is a no-op.
type HeadlessRecorder = headlessRecorder

// NewHeadlessRecorder creates a recorder for a run and workspace.
func NewHeadlessRecorder(store *db.Store, id, workspace string) *HeadlessRecorder {
	return &headlessRecorder{store: store, id: id, workspace: workspace, nextAttempt: 1}
}

// Start persists the initial run row.
func (r *headlessRecorder) Start(ctx context.Context, prompt, provider, model string) {
	r.start(ctx, prompt, provider, model)
}

// Progress persists current iteration and tool-call counts.
func (r *headlessRecorder) Progress(ctx context.Context, iterations, toolCalls int) {
	r.progress(ctx, iterations, toolCalls)
}

// Attempt persists a completed pursue attempt.
func (r *headlessRecorder) Attempt(ctx context.Context, report pursue.AttemptReport) {
	r.attempt(ctx, report)
}

// VerifierResult persists a verifier result.
func (r *headlessRecorder) VerifierResult(ctx context.Context, result coderunner.VerifyResult) {
	r.verifierResult(ctx, result)
}

// Complete persists the terminal run summary.
func (r *headlessRecorder) Complete(ctx context.Context, result runner.TaskResult) {
	r.complete(ctx, result)
}
