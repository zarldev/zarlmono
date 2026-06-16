-- name: ListProviders :many
SELECT name, display_name, adapter_type, base_url, models_url, default_model, seed_models, enabled, builtin, created_at, updated_at, reasoning_history, context_window, input_cost_per_mtok, output_cost_per_mtok
FROM llm_providers
ORDER BY name;

-- name: GetProvider :one
SELECT name, display_name, adapter_type, base_url, models_url, default_model, seed_models, enabled, builtin, created_at, updated_at, reasoning_history, context_window, input_cost_per_mtok, output_cost_per_mtok
FROM llm_providers
WHERE name = ?;

-- name: UpsertProvider :exec
INSERT INTO llm_providers (name, display_name, adapter_type, base_url, models_url, default_model, seed_models, enabled, builtin, created_at, updated_at, reasoning_history, context_window, input_cost_per_mtok, output_cost_per_mtok)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (name) DO UPDATE SET
    display_name = excluded.display_name,
    adapter_type = excluded.adapter_type,
    base_url     = excluded.base_url,
    models_url   = excluded.models_url,
    default_model = excluded.default_model,
    seed_models  = excluded.seed_models,
    reasoning_history = excluded.reasoning_history,
    context_window = excluded.context_window,
    input_cost_per_mtok = excluded.input_cost_per_mtok,
    output_cost_per_mtok = excluded.output_cost_per_mtok,
    enabled      = excluded.enabled,
    builtin      = excluded.builtin,
    updated_at   = excluded.updated_at;

-- name: DeleteProvider :exec
DELETE FROM llm_providers WHERE name = ?;
