-- name: GetPromptTemplate :one
SELECT template_key, content, updated_at FROM prompt_templates WHERE template_key = ?;

-- name: ListPromptTemplates :many
SELECT template_key, content, updated_at FROM prompt_templates ORDER BY template_key;

-- name: UpsertPromptTemplate :exec
INSERT INTO prompt_templates (template_key, content)
VALUES (?, ?)
ON DUPLICATE KEY UPDATE content = VALUES(content), updated_at = CURRENT_TIMESTAMP;

-- name: DeletePromptTemplate :exec
DELETE FROM prompt_templates WHERE template_key = ?;
