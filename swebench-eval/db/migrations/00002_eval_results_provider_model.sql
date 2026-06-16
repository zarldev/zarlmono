-- +goose Up
-- +goose StatementBegin

-- Add provider + model to every result row. Without these, two runs
-- of "zarlcode" against the same task set are indistinguishable in
-- the db even when they used different LLMs underneath — which is
-- the comparison axis that matters most for "small model thesis"
-- validation.
ALTER TABLE eval_results ADD COLUMN provider TEXT NOT NULL DEFAULT '';
ALTER TABLE eval_results ADD COLUMN model    TEXT NOT NULL DEFAULT '';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE eval_results DROP COLUMN provider;
ALTER TABLE eval_results DROP COLUMN model;
-- +goose StatementEnd
