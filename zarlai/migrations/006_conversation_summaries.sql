CREATE TABLE IF NOT EXISTS conversation_summaries (
    id VARCHAR(36) PRIMARY KEY,
    person_name VARCHAR(255) NOT NULL,
    summary TEXT NOT NULL,
    session_id VARCHAR(255) NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_summaries_person (person_name),
    INDEX idx_summaries_created (created_at)
);
