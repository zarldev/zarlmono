-- name: InsertHeadlessRun :exec
-- Records the start of a headless (or spectator) task run. Called
-- immediately after the prompt is parsed and the runner is built,
-- before runner.Run, so a crashed run still leaves a row with
-- ended_at NULL and terminal_reason NULL that the eval framework
-- can detect as "started but didn't finish cleanly".
-- provider + model are captured here (rather than at complete-time)
-- because they're fixed for the duration of a single headless run and
-- knowing them up-front lets a still-in-flight row be grouped
-- correctly by any reader.
INSERT INTO headless_runs (
    id, workspace, base_commit, prompt, started_at, provider, model
) VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: CompleteHeadlessRun :exec
-- Records the terminal state of a previously-Inserted run. Called
-- once when the runner returns (or when ctx is cancelled). All
-- summary fields populate here in one write so the eval framework
-- sees a coherent row or no row at all.
UPDATE headless_runs SET
    ended_at        = ?,
    terminal_reason = ?,
    error           = ?,
    final_content   = ?,
    final_diff      = ?,
    iterations      = ?,
    tool_calls      = ?,
    tokens_in       = ?,
    tokens_out      = ?,
    duration_ms     = ?,
    escalated       = ?
WHERE id = ?;

-- name: GetHeadlessRun :one
-- Fetches a single run by id. The SWE-bench Driver calls this with
-- the uuid it generated before shelling out, to assemble the harness
-- Result struct.
SELECT
    id, workspace, base_commit, prompt,
    started_at, ended_at,
    terminal_reason, error, final_content, final_diff,
    iterations, tool_calls, tokens_in, tokens_out, duration_ms,
    escalated, provider, model
FROM headless_runs
WHERE id = ?;

-- name: ListHeadlessRunsByWorkspace :many
-- Returns runs for one workspace newest-first. Used by future
-- "show recent runs" UX in zarlcode itself (not load-bearing for
-- the eval framework, but cheap to ship now that the table exists).
SELECT
    id, workspace, base_commit, prompt,
    started_at, ended_at,
    terminal_reason, error, final_content, final_diff,
    iterations, tool_calls, tokens_in, tokens_out, duration_ms,
    escalated, provider, model
FROM headless_runs
WHERE workspace = ?
ORDER BY started_at DESC
LIMIT ?;

-- name: UpdateHeadlessRunProgress :exec
-- Patches the iter/tool counters mid-run so a SIGKILL'd subprocess
-- leaves a recoverable trail of "made it to iter N with M tool
-- calls" instead of the initial-insert iter=NULL state. Called by
-- the runner's progress updater after every iteration's tool
-- dispatch completes. terminal_reason / final_content / final_diff
-- stay NULL until CompleteHeadlessRun fires at clean termination.
UPDATE headless_runs
SET iterations = ?, tool_calls = ?
WHERE id = ?;

-- name: BackfillHeadlessRunProviderModel :exec
-- One-shot backfill for rows that pre-date the provider/model
-- columns: takes a workspace + provider/model pair and stamps every
-- row matching that workspace that still has empty values. Used by
-- the eval framework after migration to retroactively complete the
-- rows the in-flight run wrote before the schema knew about these
-- columns.
UPDATE headless_runs
SET provider = ?, model = ?
WHERE workspace = ? AND provider = '' AND model = '';

-- name: InsertHeadlessAttempt :exec
-- Records one completed REDRIVE attempt for a headless run. Upsert keeps
-- recorder retries/idempotent tests from failing on the composite key.
INSERT INTO headless_attempts (
    run_id, attempt_number, prompt,
    terminal_reason, error, final_content,
    iterations, tool_calls, tokens_in, tokens_out,
    decision_done, feedback, recorded_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(run_id, attempt_number) DO UPDATE SET
    prompt          = excluded.prompt,
    terminal_reason = excluded.terminal_reason,
    error           = excluded.error,
    final_content   = excluded.final_content,
    iterations      = excluded.iterations,
    tool_calls      = excluded.tool_calls,
    tokens_in       = excluded.tokens_in,
    tokens_out      = excluded.tokens_out,
    decision_done   = excluded.decision_done,
    feedback        = excluded.feedback,
    recorded_at     = excluded.recorded_at;

-- name: ListHeadlessAttempts :many
-- Returns attempt trace rows for one headless run in attempt order.
SELECT
    run_id, attempt_number, prompt,
    terminal_reason, error, final_content,
    iterations, tool_calls, tokens_in, tokens_out,
    decision_done, feedback, recorded_at
FROM headless_attempts
WHERE run_id = ?
ORDER BY attempt_number ASC;

-- name: InsertHeadlessVerifierResult :exec
-- Records the structured verifier/oracle result for one REDRIVE attempt.
INSERT INTO headless_verifier_results (
    run_id, attempt_number, command,
    skipped, success, exit_code, error, output_tail,
    duration_ms, recorded_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(run_id, attempt_number) DO UPDATE SET
    command     = excluded.command,
    skipped     = excluded.skipped,
    success     = excluded.success,
    exit_code   = excluded.exit_code,
    error       = excluded.error,
    output_tail = excluded.output_tail,
    duration_ms = excluded.duration_ms,
    recorded_at = excluded.recorded_at;

-- name: ListHeadlessVerifierResults :many
-- Returns verifier/oracle results for one headless run in attempt order.
SELECT
    run_id, attempt_number, command,
    skipped, success, exit_code, error, output_tail,
    duration_ms, recorded_at
FROM headless_verifier_results
WHERE run_id = ?
ORDER BY attempt_number ASC;
