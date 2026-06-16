-- name: InsertToolProposal :exec
INSERT INTO tool_proposals (id, tool_name, description, mcp_url, rationale, status, created_at)
VALUES (?, ?, ?, ?, ?, 'pending', NOW());

-- name: ListToolProposals :many
SELECT id, tool_name, description, mcp_url, rationale, status, created_at
FROM tool_proposals
ORDER BY created_at DESC;

-- name: GetToolProposal :one
SELECT id, tool_name, description, mcp_url, rationale, status, created_at
FROM tool_proposals
WHERE id = ?;

-- name: UpdateToolProposalStatus :exec
UPDATE tool_proposals SET status = ? WHERE id = ?;
