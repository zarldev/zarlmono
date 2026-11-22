-- name: GetRouteProtocol :one
SELECT protocol FROM llm_route_protocols WHERE provider = ? AND alias = ?;

-- name: UpsertRouteProtocol :exec
INSERT INTO llm_route_protocols (provider, alias, protocol) VALUES (?, ?, ?)
ON CONFLICT (provider, alias) DO UPDATE SET protocol = excluded.protocol;

-- name: DeleteRouteProtocol :exec
DELETE FROM llm_route_protocols WHERE provider = ? AND alias = ?;

-- name: DeleteProviderRouteProtocols :exec
DELETE FROM llm_route_protocols WHERE provider = ?;
