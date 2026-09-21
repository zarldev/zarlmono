-- +goose Up
-- Empty means the invocation predates manifest capture. Do not infer its inputs.
ALTER TABLE eval_runs ADD COLUMN manifest_json TEXT NOT NULL DEFAULT '';

-- +goose Down
-- Downgrade discards the captured task definitions and configuration.
ALTER TABLE eval_runs DROP COLUMN manifest_json;
