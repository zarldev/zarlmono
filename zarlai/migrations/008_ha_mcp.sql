USE zarl;

-- Home Assistant now speaks MCP natively. Drop the legacy builtin provider
-- and seed an MCP entry pointing at HA's /api/mcp endpoint. Existing installs
-- that already have an ha_mcp row keep their config.
DELETE FROM tool_providers WHERE name = 'home_assistant';

INSERT INTO tool_providers (id, name, type, enabled, config)
SELECT UUID(), 'ha_mcp', 'mcp', FALSE, '{"url":"","auth_token":""}'
WHERE NOT EXISTS (SELECT 1 FROM tool_providers WHERE name = 'ha_mcp');
