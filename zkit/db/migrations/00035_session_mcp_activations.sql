-- +goose Up
ALTER TABLE sessions
ADD COLUMN mcp_activations_json TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE sessions
DROP COLUMN mcp_activations_json;
