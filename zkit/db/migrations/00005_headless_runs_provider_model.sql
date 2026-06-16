-- +goose Up
-- +goose StatementBegin

-- Add provider + model columns to headless_runs so the SWE-bench
-- eval framework (and future "show me what I ran last week" UX)
-- can group results by the LLM that produced them. Without these
-- columns, "zarlcode + openai-codex/gpt-5.5" and "zarlcode + local
-- llamacpp/qwen3.6" look identical in the row, which defeats the
-- whole point of run-over-run comparison.
--
-- Both columns are TEXT NOT NULL DEFAULT '' so old rows (pre-this-
-- migration) read as empty rather than NULL — the runtime treats
-- "" as "unknown" and the eval-side report renders it as "?".
ALTER TABLE headless_runs ADD COLUMN provider TEXT NOT NULL DEFAULT '';
ALTER TABLE headless_runs ADD COLUMN model    TEXT NOT NULL DEFAULT '';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- sqlite doesn't support DROP COLUMN before 3.35; we ship sqlite via
-- modernc which is recent enough, but the Down path is rarely used.
-- Recreate the table without the columns if a downgrade is ever needed.
ALTER TABLE headless_runs DROP COLUMN provider;
ALTER TABLE headless_runs DROP COLUMN model;
-- +goose StatementEnd
