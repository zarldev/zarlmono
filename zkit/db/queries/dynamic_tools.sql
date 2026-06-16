-- name: ListDynamicTools :many
-- All registered tools for a workspace. Caller (dynamic.Manifest)
-- decodes spec_json back into tools.ToolSpec when restoring at
-- startup. Order is stable (registration order is the natural read
-- order for /tools listings).
SELECT name, spec_json, binary_path FROM dynamic_tools
WHERE workspace = ?
ORDER BY name;

-- name: UpsertDynamicTool :exec
-- Insert or replace by (workspace, name). The manifest layer treats
-- a re-Register as "replace in place" so this needs ON CONFLICT DO
-- UPDATE rather than rejecting duplicates.
INSERT INTO dynamic_tools (workspace, name, spec_json, binary_path, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (workspace, name) DO UPDATE SET
    spec_json   = excluded.spec_json,
    binary_path = excluded.binary_path,
    updated_at  = excluded.updated_at;

-- name: DeleteDynamicTool :exec
DELETE FROM dynamic_tools WHERE workspace = ? AND name = ?;

-- name: DeleteDynamicToolsByWorkspace :exec
-- Bulk drop for `zarlcode keys ...`-style reset paths. Not used by
-- the manifest layer today but cheap to ship for completeness.
DELETE FROM dynamic_tools WHERE workspace = ?;
