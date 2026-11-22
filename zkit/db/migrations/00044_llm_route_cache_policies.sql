-- +goose Up
CREATE TABLE llm_route_cache_policies (
    provider TEXT NOT NULL CHECK (typeof(provider) = 'text' AND length(CAST(provider AS BLOB)) > 0),
    alias TEXT NOT NULL CHECK (typeof(alias) = 'text' AND length(CAST(alias AS BLOB)) > 0),
    version INTEGER NOT NULL CHECK (typeof(version) = 'integer'),
    mode TEXT NOT NULL CHECK (typeof(mode) = 'text'),
    static_ttl TEXT NOT NULL CHECK (typeof(static_ttl) = 'text'),
    rolling_ttl TEXT NOT NULL CHECK (typeof(rolling_ttl) = 'text'),
    system_marker INTEGER NOT NULL CHECK (typeof(system_marker) = 'integer'),
    tool_marker INTEGER NOT NULL CHECK (typeof(tool_marker) = 'integer'),
    PRIMARY KEY (provider, alias)
);

-- +goose Down
DROP TABLE llm_route_cache_policies;
