-- name: GetRouteCachePolicy :one
SELECT CAST(version AS TEXT) AS version, typeof(version) AS version_type,
       CAST(mode AS TEXT) AS mode, typeof(mode) AS mode_type,
       CAST(static_ttl AS TEXT) AS static_ttl, typeof(static_ttl) AS static_ttl_type,
       CAST(rolling_ttl AS TEXT) AS rolling_ttl, typeof(rolling_ttl) AS rolling_ttl_type,
       CAST(system_marker AS TEXT) AS system_marker, typeof(system_marker) AS system_marker_type,
       CAST(tool_marker AS TEXT) AS tool_marker, typeof(tool_marker) AS tool_marker_type
FROM llm_route_cache_policies WHERE provider = ? AND alias = ?;

-- name: UpsertRouteCachePolicy :exec
INSERT INTO llm_route_cache_policies
(provider, alias, version, mode, static_ttl, rolling_ttl, system_marker, tool_marker)
VALUES (?, ?, 1, ?, ?, ?, ?, ?)
ON CONFLICT (provider, alias) DO UPDATE SET
version = excluded.version, mode = excluded.mode,
static_ttl = excluded.static_ttl, rolling_ttl = excluded.rolling_ttl,
system_marker = excluded.system_marker, tool_marker = excluded.tool_marker;

-- name: DeleteRouteCachePolicy :exec
DELETE FROM llm_route_cache_policies WHERE provider = ? AND alias = ?;
