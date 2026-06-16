-- +goose Up
-- +goose StatementBegin

-- Custom (OpenAI-compatible) providers differ in how prior-turn assistant
-- reasoning must be echoed back in request history:
--   inline — re-wrap reasoning as <think>…</think> inside content (default)
--   field  — send it in the dedicated reasoning_content message field
--   strip  — drop prior-turn reasoning entirely
-- Thinking models like Moonshot/Kimi and DeepSeek-V4 require "field" — without
-- it the API 400s ("thinking is enabled but reasoning_content is missing in
-- assistant tool call message"). deepseek-reasoner (R1) is the opposite and
-- needs "strip". TEXT NOT NULL DEFAULT 'inline' so existing rows keep today's
-- behaviour.
ALTER TABLE llm_providers ADD COLUMN reasoning_history TEXT NOT NULL DEFAULT 'inline';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE llm_providers DROP COLUMN reasoning_history;
-- +goose StatementEnd
