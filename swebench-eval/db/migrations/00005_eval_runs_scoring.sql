-- +goose Up
-- Historical rows carry no evidence about whether scoring was requested.
ALTER TABLE eval_runs ADD COLUMN score_status TEXT NOT NULL DEFAULT 'not_recorded';
ALTER TABLE eval_runs ADD COLUMN score_error TEXT NOT NULL DEFAULT '';

-- +goose Down
-- Downgrade discards recorded scoring lifecycle evidence.
ALTER TABLE eval_runs DROP COLUMN score_error;
ALTER TABLE eval_runs DROP COLUMN score_status;
