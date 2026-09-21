-- name: InsertHistoryValue :exec
INSERT INTO session_history_values (id, payload) VALUES (?, ?) ON CONFLICT(id) DO NOTHING;

-- name: GetHistoryValue :one
SELECT payload FROM session_history_values WHERE id = ?;

-- name: InsertHistoryNode :exec
INSERT INTO session_history_nodes (id, parent_id, value_id) VALUES (?, ?, ?) ON CONFLICT(id) DO NOTHING;

-- name: GetHistoryNode :one
SELECT parent_id, value_id FROM session_history_nodes WHERE id = ?;

-- name: GetHistoryHead :one
SELECT head FROM session_history_heads WHERE session_id = ? AND kind = ?;

-- name: SetHistoryHead :exec
INSERT INTO session_history_heads (session_id, kind, head) VALUES (?, ?, ?)
ON CONFLICT(session_id, kind) DO UPDATE SET head = excluded.head;

-- name: InsertCheckpointHistory :exec
INSERT INTO session_checkpoint_history (session_id, checkpoint_id, transcript_head, replay_head, state_id) VALUES (?, ?, ?, ?, ?);

-- name: GetCheckpointHistory :one
SELECT transcript_head, replay_head, state_id FROM session_checkpoint_history WHERE session_id = ? AND checkpoint_id = ?;

-- name: SaveModelContext :exec
INSERT INTO session_model_context (session_id, history_head, generation, request_json) VALUES (?, ?, 1, ?)
ON CONFLICT(session_id) DO UPDATE SET history_head = excluded.history_head,
    generation = session_model_context.generation + 1, request_json = excluded.request_json;

-- name: GetModelContext :one
SELECT history_head, generation, request_json FROM session_model_context WHERE session_id = ?;

-- name: GetHistoryBatch :one
SELECT checksum FROM session_history_batches WHERE session_id = ? AND batch_id = ?;

-- name: InsertHistoryBatch :exec
INSERT INTO session_history_batches (session_id, batch_id, checksum) VALUES (?, ?, ?);
