-- name: ListMCPServers :many
SELECT name, transport, command, args, env, base_url, auth_token, enabled, created_at, updated_at
FROM mcp_servers
ORDER BY name;

-- name: UpsertMCPServer :exec
INSERT INTO mcp_servers (name, transport, command, args, env, base_url, auth_token, enabled, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (name) DO UPDATE SET
    transport  = excluded.transport,
    command    = excluded.command,
    args       = excluded.args,
    env        = excluded.env,
    base_url   = excluded.base_url,
    auth_token = excluded.auth_token,
    enabled    = excluded.enabled,
    updated_at = excluded.updated_at;

-- name: DeleteMCPServer :exec
DELETE FROM mcp_servers WHERE name = ?;
