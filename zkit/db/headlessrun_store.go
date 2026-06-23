package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/zarldev/zarlmono/zkit/db/gen"
)

// --- headless_runs ---

// HeadlessRunStart is what a fresh headless / spectator task hands to
// InsertHeadlessRun. The summary fields populate later via
// CompleteHeadlessRun once the runner returns.
//
// Provider + Model are captured at start (not completion) because
// they're fixed for the duration of one headless run and an
// observer of a still-in-flight row benefits from knowing them.
type HeadlessRunStart struct {
	ID         string
	Workspace  string
	BaseCommit string // optional — "" when the workspace isn't a git repo
	Prompt     string
	StartedAt  time.Time
	Provider   string // e.g. "openai-codex", "gemini", "llamacpp"; "" when unknown
	Model      string // e.g. "gpt-5.5", "gemini-2.5-pro", "qwen3.6-35b-a3b-mtp"
}

// HeadlessRunSummary is the terminal-state payload CompleteHeadlessRun
// writes after the runner returns. The pointer-typed numeric fields
// distinguish "this run never produced LLM usage" (nil) from "ran but
// reported zero" (0).
type HeadlessRunSummary struct {
	EndedAt        time.Time
	TerminalReason string
	Error          string // empty when reason != error
	FinalContent   string
	FinalDiff      string // empty string = ran but no files changed; nil only via NULL row
	Iterations     int
	ToolCalls      int
	TokensIn       *int64
	TokensOut      *int64
	Duration       time.Duration
	Escalated      bool
}

// HeadlessRunRecord is the read-back shape — what Get / List return.
// Nullable columns become pointer fields so the SWE-bench Driver can
// distinguish "didn't finish" (EndedAt nil) from "finished cleanly".
type HeadlessRunRecord struct {
	ID             string
	Workspace      string
	BaseCommit     string
	Prompt         string
	StartedAt      time.Time
	EndedAt        *time.Time
	TerminalReason string
	Error          string
	FinalContent   string
	FinalDiff      string
	Iterations     int
	ToolCalls      int
	TokensIn       *int64
	TokensOut      *int64
	Duration       time.Duration
	Escalated      bool
	Provider       string
	Model          string
}

// HeadlessAttemptRecord is one completed pursue/REDRIVE attempt within a
// headless run. It stores the runner outcome plus the goal decision that either
// ended the run or produced feedback for the next attempt.
type HeadlessAttemptRecord struct {
	RunID          string
	AttemptNumber  int
	Prompt         string
	TerminalReason string
	Error          string
	FinalContent   string
	Iterations     int
	ToolCalls      int
	TokensIn       *int64
	TokensOut      *int64
	DecisionDone   bool
	Feedback       string
	RecordedAt     time.Time
}

// HeadlessVerifierResultRecord is the structured verifier/oracle verdict for a
// headless attempt. Skipped is true when the oracle was deliberately not run
// (for example, the changed-nothing guard re-used prior feedback).
type HeadlessVerifierResultRecord struct {
	RunID         string
	AttemptNumber int
	Command       string
	Skipped       bool
	Success       bool
	ExitCode      *int64
	Error         string
	OutputTail    string
	Duration      time.Duration
	RecordedAt    time.Time
}

// InsertHeadlessRun records the start of a task. Called immediately
// after the prompt is parsed and the runner is built, so a crashed
// run still leaves a row with ended_at NULL that the eval framework
// can detect as "started but didn't finish".
func (s *Store) InsertHeadlessRun(ctx context.Context, r HeadlessRunStart) error {
	if err := s.q.InsertHeadlessRun(ctx, gen.InsertHeadlessRunParams{
		ID:         r.ID,
		Workspace:  r.Workspace,
		BaseCommit: nullableString(r.BaseCommit),
		Prompt:     r.Prompt,
		StartedAt:  r.StartedAt.Unix(),
		Provider:   r.Provider,
		Model:      r.Model,
	}); err != nil {
		return fmt.Errorf("insert headless run %q: %w", r.ID, err)
	}
	return nil
}

// UpdateHeadlessRunProgress persists intermediate counters for an
// in-flight run. Called from the runner's progress updater so a
// SIGKILL'd subprocess still leaves a row reflecting the agent's
// real progress. iter is 1-based here (the count of completed
// iterations), toolCalls is cumulative across all of them.
func (s *Store) UpdateHeadlessRunProgress(ctx context.Context, id string, iter, toolCalls int) error {
	if err := s.q.UpdateHeadlessRunProgress(ctx, gen.UpdateHeadlessRunProgressParams{
		ID:         id,
		Iterations: nullableInt64Value(int64(iter)),
		ToolCalls:  nullableInt64Value(int64(toolCalls)),
	}); err != nil {
		return fmt.Errorf("update progress %q: %w", id, err)
	}
	return nil
}

// InsertHeadlessAttempt records one completed REDRIVE attempt. The underlying
// query upserts on (run_id, attempt_number), so repeated recorder calls replace
// the row with the latest summary instead of failing the whole run.
func (s *Store) InsertHeadlessAttempt(ctx context.Context, r HeadlessAttemptRecord) error {
	decisionDone := int64(0)
	if r.DecisionDone {
		decisionDone = 1
	}
	if r.RecordedAt.IsZero() {
		r.RecordedAt = time.Now()
	}
	if err := s.q.InsertHeadlessAttempt(ctx, gen.InsertHeadlessAttemptParams{
		RunID:          r.RunID,
		AttemptNumber:  int64(r.AttemptNumber),
		Prompt:         r.Prompt,
		TerminalReason: nullableString(r.TerminalReason),
		Error:          nullableString(r.Error),
		FinalContent:   nullableString(r.FinalContent),
		Iterations:     nullableInt64Value(int64(r.Iterations)),
		ToolCalls:      nullableInt64Value(int64(r.ToolCalls)),
		TokensIn:       nullableInt64Ptr(r.TokensIn),
		TokensOut:      nullableInt64Ptr(r.TokensOut),
		DecisionDone:   decisionDone,
		Feedback:       nullableString(r.Feedback),
		RecordedAt:     r.RecordedAt.Unix(),
	}); err != nil {
		return fmt.Errorf("insert headless attempt %q/%d: %w", r.RunID, r.AttemptNumber, err)
	}
	return nil
}

// InsertHeadlessVerifierResult records the structured oracle result for one
// REDRIVE attempt. The query upserts on (run_id, attempt_number) for idempotent
// recorder retries.
func (s *Store) InsertHeadlessVerifierResult(ctx context.Context, r HeadlessVerifierResultRecord) error {
	skipped, success := int64(0), int64(0)
	if r.Skipped {
		skipped = 1
	}
	if r.Success {
		success = 1
	}
	if r.RecordedAt.IsZero() {
		r.RecordedAt = time.Now()
	}
	if err := s.q.InsertHeadlessVerifierResult(ctx, gen.InsertHeadlessVerifierResultParams{
		RunID:         r.RunID,
		AttemptNumber: int64(r.AttemptNumber),
		Command:       r.Command,
		Skipped:       skipped,
		Success:       success,
		ExitCode:      nullableInt64Ptr(r.ExitCode),
		Error:         nullableString(r.Error),
		OutputTail:    nullableString(r.OutputTail),
		DurationMs:    r.Duration.Milliseconds(),
		RecordedAt:    r.RecordedAt.Unix(),
	}); err != nil {
		return fmt.Errorf("insert headless verifier result %q/%d: %w", r.RunID, r.AttemptNumber, err)
	}
	return nil
}

// ListHeadlessVerifierResults returns structured oracle results for one
// headless run in attempt order.
func (s *Store) ListHeadlessVerifierResults(ctx context.Context, runID string) ([]HeadlessVerifierResultRecord, error) {
	rows, err := s.q.ListHeadlessVerifierResults(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("list headless verifier results for %q: %w", runID, err)
	}
	out := make([]HeadlessVerifierResultRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, headlessVerifierResultRowToRecord(r))
	}
	return out, nil
}

// ListHeadlessAttempts returns all attempt trace rows for one headless run in
// attempt order.
func (s *Store) ListHeadlessAttempts(ctx context.Context, runID string) ([]HeadlessAttemptRecord, error) {
	rows, err := s.q.ListHeadlessAttempts(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("list headless attempts for %q: %w", runID, err)
	}
	out := make([]HeadlessAttemptRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, headlessAttemptRowToRecord(r))
	}
	return out, nil
}

// BackfillHeadlessRunProviderModel sets provider + model on every
// row matching workspace that still has empty values for these
// columns. Used immediately after the migration adds the columns
// so rows the in-flight run wrote with default values get
// retroactively completed.
func (s *Store) BackfillHeadlessRunProviderModel(ctx context.Context, workspace, provider, model string) error {
	if err := s.q.BackfillHeadlessRunProviderModel(ctx, gen.BackfillHeadlessRunProviderModelParams{
		Workspace: workspace,
		Provider:  provider,
		Model:     model,
	}); err != nil {
		return fmt.Errorf("backfill provider/model for %q: %w", workspace, err)
	}
	return nil
}

// CompleteHeadlessRun writes the terminal state. Called once per run,
// regardless of how it terminated; on a panic-recovered crash the
// caller still does this with reason="error" + the recovery message.
func (s *Store) CompleteHeadlessRun(ctx context.Context, id string, summary HeadlessRunSummary) error {
	escalated := int64(0)
	if summary.Escalated {
		escalated = 1
	}
	if err := s.q.CompleteHeadlessRun(ctx, gen.CompleteHeadlessRunParams{
		ID:             id,
		EndedAt:        nullableInt64(summary.EndedAt.Unix()),
		TerminalReason: nullableString(summary.TerminalReason),
		Error:          nullableString(summary.Error),
		FinalContent:   nullableString(summary.FinalContent),
		FinalDiff:      nullableString(summary.FinalDiff),
		Iterations:     nullableInt64Value(int64(summary.Iterations)),
		ToolCalls:      nullableInt64Value(int64(summary.ToolCalls)),
		TokensIn:       nullableInt64Ptr(summary.TokensIn),
		TokensOut:      nullableInt64Ptr(summary.TokensOut),
		DurationMs:     nullableInt64Value(summary.Duration.Milliseconds()),
		Escalated:      escalated,
	}); err != nil {
		return fmt.Errorf("complete headless run %q: %w", id, err)
	}
	return nil
}

// GetHeadlessRun fetches one run by id. Returns ErrNotFound when no
// row exists.
func (s *Store) GetHeadlessRun(ctx context.Context, id string) (HeadlessRunRecord, error) {
	row, err := s.q.GetHeadlessRun(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return HeadlessRunRecord{}, ErrNotFound
		}
		return HeadlessRunRecord{}, fmt.Errorf("get headless run %q: %w", id, err)
	}
	return headlessRowToRecord(row), nil
}

// ListHeadlessRunsByWorkspace returns the most recent N runs for a
// workspace, newest first. limit ≤ 0 caps at 100 — enough for the
// "show me recent runs" UX without unbounded scans.
func (s *Store) ListHeadlessRunsByWorkspace(
	ctx context.Context,
	workspace string,
	limit int,
) ([]HeadlessRunRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.q.ListHeadlessRunsByWorkspace(ctx, gen.ListHeadlessRunsByWorkspaceParams{
		Workspace: workspace,
		Limit:     int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list headless runs for %q: %w", workspace, err)
	}
	out := make([]HeadlessRunRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, headlessRowToRecord(r))
	}
	return out, nil
}

// --- null-handling helpers ---
//
// sqlc emits sql.NullString / sql.NullInt64 for nullable columns;
// the domain side prefers plain string / *int64. These wrap that
// translation in one place so the row-mapping helpers above stay
// focused on field-by-field layout.

func nullableString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

func nullableInt64(v int64) sql.NullInt64 {
	return sql.NullInt64{Int64: v, Valid: true}
}

func nullableInt64Value(v int64) sql.NullInt64 {
	return sql.NullInt64{Int64: v, Valid: true}
}

func nullableInt64Ptr(v *int64) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *v, Valid: true}
}

func stringFromNull(n sql.NullString) string {
	if !n.Valid {
		return ""
	}
	return n.String
}

func int64FromNull(n sql.NullInt64) int64 {
	if !n.Valid {
		return 0
	}
	return n.Int64
}

func int64PtrFromNull(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}
