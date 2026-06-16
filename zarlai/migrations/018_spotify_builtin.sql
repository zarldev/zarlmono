-- migrations/018_spotify_builtin.sql
-- Flips the spotify tool_providers row from `mcp` (uvx-launched Python
-- subprocess) to `builtin` (native Go package). Resets credentials so
-- the operator explicitly re-enters client_id/secret via the admin UI,
-- and defaults the redirect_uri and cache_path to known-good values.
USE zarl;

UPDATE tool_providers
SET type = 'builtin',
    config = JSON_OBJECT(
        'client_id', '',
        'client_secret', '',
        'redirect_uri', 'http://127.0.0.1:8765/callback',
        'cache_path', ''
    ),
    enabled = FALSE
WHERE name = 'spotify';
