-- +goose Up
CREATE TABLE llm_route_protocols (
    provider TEXT NOT NULL CHECK (length(CAST(provider AS BLOB)) > 0),
    alias TEXT NOT NULL CHECK (length(CAST(alias AS BLOB)) > 0),
    protocol TEXT NOT NULL CHECK (protocol IN ('chat_completions', 'responses', 'messages', 'generate_content')),
    PRIMARY KEY (provider, alias)
);

-- +goose Down
DROP TABLE llm_route_protocols;
