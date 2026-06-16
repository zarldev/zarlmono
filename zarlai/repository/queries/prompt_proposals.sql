-- name: InsertPromptProposal :exec
INSERT INTO prompt_proposals (id, current_prompt_id, proposed_content, rationale, status, created_at)
VALUES (?, ?, ?, ?, 'pending', NOW());

-- name: ListPromptProposals :many
SELECT id, current_prompt_id, proposed_content, rationale, status, created_at
FROM prompt_proposals
ORDER BY created_at DESC;

-- name: GetPromptProposal :one
SELECT id, current_prompt_id, proposed_content, rationale, status, created_at
FROM prompt_proposals
WHERE id = ?;

-- name: UpdatePromptProposalStatus :exec
UPDATE prompt_proposals SET status = ? WHERE id = ?;

-- name: CountPendingPromptProposals :one
SELECT COUNT(*) FROM prompt_proposals WHERE status = 'pending';
