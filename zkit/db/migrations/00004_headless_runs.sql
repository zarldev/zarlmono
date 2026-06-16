-- +goose Up
-- +goose StatementBegin

-- headless_runs: durable record of every task executed via
-- `zarlcode --headless` or `zarlcode --spectate`. Both modes capture
-- the same shape because they share the same auto-submit + auto-
-- record harness; the only difference is whether a TUI is painted.
--
-- This is the table the SWE-bench eval framework reads to assemble
-- harness Results. Keying off the task ID (a uuid generated when
-- the run starts) means the Driver can shell out `zarlcode --headless`,
-- wait for exit, then look up the row it just wrote without parsing
-- stdout — no JSON sidecars, no log scraping.
--
-- Interactive (composer-driven) sessions don't write here; they live
-- in the `sessions` table with their own restore semantics. A future
-- migration may unify these, but until then they serve different
-- shapes (one row per turn vs. one row per task).
--
-- final_diff is the unified `git diff <base_commit>..HEAD` captured
-- at completion. NULL when the run failed before any tool ran
-- (e.g. the prompt couldn't render). Empty string is meaningful:
-- the agent ran but didn't change any files.
CREATE TABLE headless_runs (
    id              TEXT    PRIMARY KEY,
    workspace       TEXT    NOT NULL,
    base_commit     TEXT,
    prompt          TEXT    NOT NULL,
    started_at      INTEGER NOT NULL,
    ended_at        INTEGER,
    terminal_reason TEXT,
    error           TEXT,
    final_content   TEXT,
    final_diff      TEXT,
    iterations      INTEGER,
    tool_calls      INTEGER,
    tokens_in       INTEGER,
    tokens_out      INTEGER,
    duration_ms     INTEGER,
    escalated       INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX idx_headless_runs_workspace ON headless_runs(workspace);
CREATE INDEX idx_headless_runs_started   ON headless_runs(started_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS headless_runs;
-- +goose StatementEnd
