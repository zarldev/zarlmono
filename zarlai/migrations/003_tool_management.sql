USE zarl;

CREATE TABLE IF NOT EXISTS tool_providers (
    id VARCHAR(36) PRIMARY KEY,
    name VARCHAR(100) NOT NULL UNIQUE,
    type VARCHAR(20) NOT NULL DEFAULT 'builtin',
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    config JSON NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS tool_calls (
    id VARCHAR(36) PRIMARY KEY,
    session_id VARCHAR(100) NOT NULL,
    tool_name VARCHAR(100) NOT NULL,
    provider VARCHAR(100) NOT NULL,
    args JSON NOT NULL,
    result TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    duration_ms INT NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_tool_calls_created (created_at DESC),
    INDEX idx_tool_calls_provider (provider)
);

-- Seed builtin providers
INSERT INTO tool_providers (id, name, type, enabled, config) VALUES
    (UUID(), 'ha_mcp', 'mcp', FALSE, '{"url":"","auth_token":""}'),
    (UUID(), 'memory', 'builtin', FALSE, '{"qdrant_url":"http://localhost:6333","embed_model":"nomic-embed-text"}'),
    (UUID(), 'searxng', 'builtin', FALSE, '{"url":"http://localhost:8888"}'),
    (UUID(), 'timer', 'builtin', TRUE, '{}'),
    (UUID(), 'wiki', 'builtin', FALSE, '{"qdrant_url":"http://localhost:6333"}');
