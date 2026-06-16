-- name: InsertSensorProposal :exec
INSERT INTO sensor_proposals (id, kind, tool_name, tool_args, interval_seconds, entity_id, rationale, status, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, 'pending', NOW());

-- name: ListSensorProposals :many
SELECT id, kind, tool_name, tool_args, interval_seconds, entity_id, rationale, status, created_at
FROM sensor_proposals
ORDER BY created_at DESC;

-- name: ListApprovedSensorProposals :many
SELECT id, kind, tool_name, tool_args, interval_seconds, entity_id, rationale, status, created_at
FROM sensor_proposals
WHERE status = 'approved'
ORDER BY created_at ASC;

-- name: GetSensorProposal :one
SELECT id, kind, tool_name, tool_args, interval_seconds, entity_id, rationale, status, created_at
FROM sensor_proposals
WHERE id = ?;

-- name: UpdateSensorProposalStatus :exec
UPDATE sensor_proposals SET status = ? WHERE id = ?;

-- name: CountPendingSensorProposals :one
SELECT COUNT(*) FROM sensor_proposals WHERE status = 'pending';
