-- +goose Up
-- +goose StatementBegin

-- Verify-loop telemetry. Run 4 produced an unexplainable
-- regression — a task that resolved 3-for-3 single-shot failed under
-- verified re-drive, and nothing recorded whether the in-run verifier
-- approved attempt 1 (grader flake) or every attempt failed (model
-- limit). These columns make that distinction durable:
--   verified         — success confirmed by the world-checking goal
--   attempts         — agent attempts consumed (1 = single-shot)
--   attempt_verdicts — JSON array of per-attempt goal verdicts
ALTER TABLE eval_results ADD COLUMN verified         INTEGER NOT NULL DEFAULT 0;
ALTER TABLE eval_results ADD COLUMN attempts         INTEGER NOT NULL DEFAULT 0;
ALTER TABLE eval_results ADD COLUMN attempt_verdicts TEXT    NOT NULL DEFAULT '';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE eval_results DROP COLUMN verified;
ALTER TABLE eval_results DROP COLUMN attempts;
ALTER TABLE eval_results DROP COLUMN attempt_verdicts;
-- +goose StatementEnd
