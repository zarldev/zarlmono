-- name: CreateTask :exec
INSERT INTO tasks (id, prompt, status, max_iterations, person_name, session_id, schedule, profile_name, workspace_name)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetTask :one
SELECT id, prompt, status, summary, iterations, max_iterations, person_name, session_id, schedule, created_at, updated_at, profile_name, workspace_name
FROM tasks WHERE id = ?;

-- name: ListTasks :many
SELECT id, prompt, status, summary, iterations, max_iterations, person_name, session_id, schedule, created_at, updated_at, profile_name, workspace_name
FROM tasks ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: ListPendingTasks :many
SELECT id, prompt, status, summary, iterations, max_iterations, person_name, session_id, schedule, created_at, updated_at, profile_name, workspace_name
FROM tasks WHERE status = 'pending' ORDER BY created_at ASC;

-- name: ListScheduledTasks :many
SELECT id, prompt, status, summary, iterations, max_iterations, person_name, session_id, schedule, created_at, updated_at, profile_name, workspace_name
FROM tasks WHERE schedule != '' AND status != 'failed';

-- name: UpdateTaskStatus :exec
UPDATE tasks SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: UpdateTaskProgress :exec
UPDATE tasks SET iterations = ?, summary = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: UpdateTaskComplete :exec
UPDATE tasks SET status = 'completed', iterations = ?, summary = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: CountTasks :one
SELECT COUNT(*) as count FROM tasks;

-- name: UpdateTaskSchedule :exec
UPDATE tasks SET schedule = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: DeleteTask :exec
DELETE FROM tasks WHERE id = ?;

-- name: ResetOrphanRunningTasks :execrows
-- Flips any task still marked `running` back to `pending` — used at startup
-- to recover tasks interrupted by a zarl crash/restart. The runner's own
-- Start will then re-enqueue them via ListPendingTasks.
UPDATE tasks SET status = 'pending', updated_at = CURRENT_TIMESTAMP
WHERE status = 'running';

-- name: SetTaskWorkspace :exec
UPDATE tasks SET workspace_name = ? WHERE id = ?;
