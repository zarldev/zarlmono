-- name: InsertConversationSummary :exec
INSERT INTO conversation_summaries (id, person_name, summary, session_id, created_at)
VALUES (?, ?, ?, ?, NOW());

-- name: ListRecentSummariesByPerson :many
SELECT id, person_name, summary, session_id, created_at
FROM conversation_summaries
WHERE person_name = ?
ORDER BY created_at DESC
LIMIT ?;

-- name: DeleteConversationSummary :exec
DELETE FROM conversation_summaries WHERE id = ?;

-- name: ListConversationSummariesPaged :many
SELECT id, person_name, summary, session_id, created_at
FROM conversation_summaries
ORDER BY created_at DESC
LIMIT ? OFFSET ?;

-- name: ListConversationSummariesPagedByPerson :many
SELECT id, person_name, summary, session_id, created_at
FROM conversation_summaries
WHERE person_name = ?
ORDER BY created_at DESC
LIMIT ? OFFSET ?;

-- name: CountConversationSummaries :one
SELECT COUNT(*) FROM conversation_summaries;

-- name: CountConversationSummariesByPerson :one
SELECT COUNT(*) FROM conversation_summaries WHERE person_name = ?;

-- name: DeleteAllConversationSummaries :execrows
DELETE FROM conversation_summaries;
