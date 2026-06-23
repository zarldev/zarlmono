-- +goose Up
-- +goose StatementBegin

-- headless_attempts records each pursue/REDRIVE attempt for a headless run.
-- The parent headless_runs row remains the run summary; this table is the
-- attempt-level trace needed to explain verifier feedback and retry history.
CREATE TABLE headless_attempts (
    run_id          TEXT    NOT NULL,
    attempt_number  INTEGER NOT NULL,
    prompt          TEXT    NOT NULL,
    terminal_reason TEXT,
    error           TEXT,
    final_content   TEXT,
    iterations      INTEGER,
    tool_calls      INTEGER,
    tokens_in       INTEGER,
    tokens_out      INTEGER,
    decision_done   INTEGER NOT NULL DEFAULT 0,
    feedback        TEXT,
    recorded_at     INTEGER NOT NULL,
    PRIMARY KEY (run_id, attempt_number),
    FOREIGN KEY (run_id) REFERENCES headless_runs(id) ON DELETE CASCADE
);

CREATE INDEX idx_headless_attempts_run ON headless_attempts(run_id, attempt_number);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS headless_attempts;
-- +goose StatementEnd
