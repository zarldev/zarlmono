-- +goose Up
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN tool_trace_json TEXT NOT NULL DEFAULT 'null';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN tool_trace_json;
-- +goose StatementEnd