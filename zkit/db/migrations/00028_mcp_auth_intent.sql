-- +goose Up
-- +goose StatementBegin

ALTER TABLE mcp_servers ADD COLUMN auth_required INTEGER NOT NULL DEFAULT 0;

-- Preserve authentication intent without reading or rewriting obsolete plaintext
-- token bytes. Current encrypted MCP credentials are keyed as mcp:<server-name>.
UPDATE mcp_servers
SET auth_required = 1
WHERE auth_token <> ''
   OR EXISTS (
       SELECT 1
       FROM api_keys
       WHERE api_keys.provider = 'mcp:' || mcp_servers.name
   );

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE mcp_servers DROP COLUMN auth_required;

-- +goose StatementEnd
