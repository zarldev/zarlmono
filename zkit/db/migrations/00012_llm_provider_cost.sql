-- +goose Up
-- +goose StatementBegin

-- Custom (OpenAI-compatible) providers aren't in the per-model price table, so
-- the cockpit shows no cost for them. These columns let a custom provider
-- declare its token price in USD per 1,000,000 tokens (how providers publish
-- it — e.g. Kimi K2.6 ~ 0.6 in / 2.5 out per 1M). 0 (the default) means
-- unmetered / unknown, leaving existing rows unchanged.
ALTER TABLE llm_providers ADD COLUMN input_cost_per_mtok REAL NOT NULL DEFAULT 0;
ALTER TABLE llm_providers ADD COLUMN output_cost_per_mtok REAL NOT NULL DEFAULT 0;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE llm_providers DROP COLUMN input_cost_per_mtok;
ALTER TABLE llm_providers DROP COLUMN output_cost_per_mtok;
-- +goose StatementEnd
