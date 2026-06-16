-- name: GetProfile :one
SELECT name, model, prompt_prefix, max_iterations, tool_names, provider_whitelist, source, updated_at
FROM profiles WHERE name = ?;

-- name: ListProfiles :many
SELECT name, model, prompt_prefix, max_iterations, tool_names, provider_whitelist, source, updated_at
FROM profiles ORDER BY source, name;

-- name: UpsertProfile :exec
INSERT INTO profiles (name, model, prompt_prefix, max_iterations, tool_names, provider_whitelist, source)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
  model = VALUES(model),
  prompt_prefix = VALUES(prompt_prefix),
  max_iterations = VALUES(max_iterations),
  tool_names = VALUES(tool_names),
  provider_whitelist = VALUES(provider_whitelist),
  source = VALUES(source);

-- name: DeleteProfile :exec
DELETE FROM profiles WHERE name = ?;

-- name: CountProfiles :one
SELECT COUNT(*) FROM profiles;
