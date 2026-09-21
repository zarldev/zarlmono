-- name: InsertHistoryPin :exec
INSERT INTO session_history_pins (id, transcript_head, replay_head, state_id) VALUES (?, ?, ?, ?);

-- name: ReleaseHistoryPin :exec
DELETE FROM session_history_pins WHERE id = ? AND transcript_head = ? AND replay_head = ? AND state_id = ?;

-- name: ListHistoryRootHeads :many
SELECT head FROM session_history_heads
UNION SELECT transcript_head FROM session_checkpoint_history
UNION SELECT replay_head FROM session_checkpoint_history
UNION SELECT history_head FROM session_model_context
UNION SELECT transcript_head FROM session_history_pins
UNION SELECT replay_head FROM session_history_pins;

-- name: ListHistoryRootValues :many
SELECT state_id FROM session_checkpoint_history
UNION SELECT state_id FROM session_history_pins;

-- name: ListHistoryNodeIDs :many
SELECT id FROM session_history_nodes ORDER BY id;

-- name: ListHistoryValueIDs :many
SELECT id FROM session_history_values ORDER BY id;

-- name: DeleteHistoryNode :execrows
DELETE FROM session_history_nodes WHERE id = ?;

-- name: DeleteHistoryValue :execrows
DELETE FROM session_history_values WHERE id = ?;
