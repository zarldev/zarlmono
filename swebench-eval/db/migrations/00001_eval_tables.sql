-- +goose Up
-- +goose StatementBegin

-- eval_runs: one row per `swebench-eval` invocation. Captures the
-- run's parameters and lifecycle bookends so we can compare runs
-- over time ("did the resolution rate improve after the decompose
-- refactor?").
--
-- task_timeout_ms is recorded as a property of the run because changes
-- to that knob materially affect resolution rate — a stricter
-- timeout makes more tasks fail with no diff. Filtering reports by
-- timeout becomes the apples-to-apples comparator across runs.
CREATE TABLE eval_runs (
    id              TEXT    PRIMARY KEY,
    started_at      INTEGER NOT NULL,
    ended_at        INTEGER,
    dataset_name    TEXT    NOT NULL,
    language_filter TEXT,
    sample_size     INTEGER NOT NULL,
    drivers         TEXT    NOT NULL,
    task_timeout_ms INTEGER NOT NULL,
    notes           TEXT
);

-- eval_results: one row per (run, task, driver). The (run_id,
-- instance_id, driver_name) triple is the natural key — the same
-- task running against the same driver in the same run only happens
-- once.
--
-- diff is stored verbatim (TEXT, may be many KB) so a future
-- inspection can diff harnesses without re-running. SWE-bench
-- patches are typically under 10 KB; the few outliers are still
-- cheap relative to the cost of re-cloning + re-running the agent.
--
-- resolved is nullable: NULL means "scoring hasn't run for this row
-- yet" (or the driver-level error skipped scoring). The report
-- distinguishes the two states.
CREATE TABLE eval_results (
    run_id           TEXT    NOT NULL,
    instance_id      TEXT    NOT NULL,
    driver_name      TEXT    NOT NULL,
    language         TEXT,
    worktree_path    TEXT,
    diff             TEXT,
    duration_ms      INTEGER NOT NULL DEFAULT 0,
    iterations       INTEGER NOT NULL DEFAULT 0,
    tool_calls       INTEGER NOT NULL DEFAULT 0,
    tokens_in        INTEGER NOT NULL DEFAULT 0,
    tokens_out       INTEGER NOT NULL DEFAULT 0,
    terminal_reason  TEXT,
    error            TEXT,
    resolved         INTEGER,
    evaluator_error  TEXT,
    created_at       INTEGER NOT NULL,
    PRIMARY KEY (run_id, instance_id, driver_name),
    FOREIGN KEY (run_id) REFERENCES eval_runs(id)
);

CREATE INDEX idx_eval_results_run         ON eval_results(run_id);
CREATE INDEX idx_eval_results_driver      ON eval_results(driver_name);
CREATE INDEX idx_eval_results_instance    ON eval_results(instance_id);
CREATE INDEX idx_eval_runs_started        ON eval_runs(started_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS eval_results;
DROP TABLE IF EXISTS eval_runs;
-- +goose StatementEnd
