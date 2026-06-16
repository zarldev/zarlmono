-- name: GetWorkspace :one
SELECT name, root, default_branch, description, created_at, updated_at
FROM workspaces WHERE name = ?;

-- name: ListWorkspaces :many
SELECT name, root, default_branch, description, created_at, updated_at
FROM workspaces ORDER BY name;

-- name: UpsertWorkspace :exec
INSERT INTO workspaces (name, root, default_branch, description)
VALUES (?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
  root = VALUES(root),
  default_branch = VALUES(default_branch),
  description = VALUES(description);

-- name: DeleteWorkspace :exec
DELETE FROM workspaces WHERE name = ?;
