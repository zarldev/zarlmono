-- name: GetSkill :one
SELECT id, name, description, markdown, profile_binding, enabled, created_at, updated_at
FROM skills WHERE id = ?;

-- name: GetSkillByName :one
SELECT id, name, description, markdown, profile_binding, enabled, created_at, updated_at
FROM skills WHERE name = ?;

-- name: ListSkills :many
SELECT id, name, description, markdown, profile_binding, enabled, created_at, updated_at
FROM skills ORDER BY name;

-- name: ListEnabledSkills :many
SELECT id, name, description, markdown, profile_binding, enabled, created_at, updated_at
FROM skills WHERE enabled = TRUE ORDER BY name;

-- name: CreateSkill :exec
INSERT INTO skills (id, name, description, markdown, profile_binding, enabled)
VALUES (?, ?, ?, ?, ?, ?);

-- name: UpdateSkill :exec
UPDATE skills
SET name = ?, description = ?, markdown = ?, profile_binding = ?, enabled = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: DeleteSkill :exec
DELETE FROM skills WHERE id = ?;

-- name: CreateSkillProposal :exec
INSERT INTO skill_proposals
  (id, target_skill_id, proposed_name, proposed_description, proposed_markdown, proposed_binding, rationale)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: ListSkillProposals :many
SELECT id, target_skill_id, proposed_name, proposed_description, proposed_markdown, proposed_binding,
       rationale, status, created_at, reviewed_at
FROM skill_proposals ORDER BY created_at DESC;

-- name: ListPendingSkillProposals :many
SELECT id, target_skill_id, proposed_name, proposed_description, proposed_markdown, proposed_binding,
       rationale, status, created_at, reviewed_at
FROM skill_proposals WHERE status = 'pending' ORDER BY created_at DESC;

-- name: GetSkillProposal :one
SELECT id, target_skill_id, proposed_name, proposed_description, proposed_markdown, proposed_binding,
       rationale, status, created_at, reviewed_at
FROM skill_proposals WHERE id = ?;

-- name: SetSkillProposalStatus :exec
UPDATE skill_proposals SET status = ?, reviewed_at = CURRENT_TIMESTAMP WHERE id = ?;
