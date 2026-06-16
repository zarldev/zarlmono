-- +goose Up
-- +goose StatementBegin

-- Custom (OpenAI-compatible) providers have no entry in the per-model context-
-- window table, so the runner falls back to its compiled-in 32K default — which
-- prematurely compacts large-window models (e.g. Kimi K2.5/K2.6 at 262144, or
-- moonshot-v1-128k at 131072). This column lets a custom provider declare its
-- real window. 0 (the default) means "unknown — use the table/probe/default",
-- so existing rows are unchanged.
ALTER TABLE llm_providers ADD COLUMN context_window INTEGER NOT NULL DEFAULT 0;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE llm_providers DROP COLUMN context_window;
-- +goose StatementEnd
