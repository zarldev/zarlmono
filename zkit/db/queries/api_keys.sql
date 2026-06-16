-- name: GetAPIKey :one
-- Single (workspace, provider) lookup. Store layer does the workspace
-- to global fallback in Go to keep the query simple.
SELECT ciphertext, nonce, key_version, storage FROM api_keys
WHERE workspace = ? AND provider = ?;

-- name: UpsertAPIKey :exec
INSERT INTO api_keys (workspace, provider, ciphertext, nonce, key_version, storage, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (workspace, provider) DO UPDATE SET
    ciphertext  = excluded.ciphertext,
    nonce       = excluded.nonce,
    key_version = excluded.key_version,
    storage     = excluded.storage,
    updated_at  = excluded.updated_at;

-- name: DeleteAPIKey :exec
DELETE FROM api_keys WHERE workspace = ? AND provider = ?;

-- name: ListAPIKeyProvidersByWorkspace :many
-- Names only - never returns the ciphertext. The store layer dedupes
-- by calling this twice (workspace + global) and unioning.
SELECT provider FROM api_keys WHERE workspace = ? ORDER BY provider;

-- name: ListAllAPIKeys :many
-- Every stored credential across all workspaces, WITH credential material.
-- Used only for credential-protection migrations; ordinary reads go through
-- GetAPIKey.
SELECT workspace, provider, ciphertext, nonce, key_version, storage FROM api_keys
ORDER BY workspace, provider;
