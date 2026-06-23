-- +goose Up
-- +goose StatementBegin

-- headless_verifier_results stores the structured oracle verdict behind each
-- REDRIVE attempt. The attempt row carries the model-visible feedback; this
-- table preserves command/runtime/output data for trace UI and product APIs.
CREATE TABLE headless_verifier_results (
    run_id          TEXT    NOT NULL,
    attempt_number  INTEGER NOT NULL,
    command         TEXT    NOT NULL,
    skipped         INTEGER NOT NULL DEFAULT 0,
    success         INTEGER NOT NULL DEFAULT 0,
    exit_code       INTEGER,
    error           TEXT,
    output_tail     TEXT,
    duration_ms     INTEGER NOT NULL DEFAULT 0,
    recorded_at     INTEGER NOT NULL,
    PRIMARY KEY (run_id, attempt_number));

CREATE INDEX idx_headless_verifier_results_run ON headless_verifier_results(run_id, attempt_number);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS headless_verifier_results;
-- +goose StatementEnd
