CREATE DATABASE IF NOT EXISTS zarl;
USE zarl;

CREATE TABLE IF NOT EXISTS prompts (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    name VARCHAR(255) NOT NULL UNIQUE,
    content TEXT NOT NULL,
    active BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS persons (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    name VARCHAR(255) NOT NULL UNIQUE,
    embedding JSON NOT NULL,
    notes TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT IGNORE INTO prompts (id, name, content, active) VALUES (
    'default-prompt-001',
    'default',
    'You are zarl, a friendly conversational AI assistant. You can see the user through their camera and recognize faces. Keep your responses to 1-4 short sentences. Be natural and conversational.',
    TRUE
);
