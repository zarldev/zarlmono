USE zarl;

CREATE TABLE IF NOT EXISTS prompt_proposals (
    id VARCHAR(36) PRIMARY KEY,
    current_prompt_id VARCHAR(36) NOT NULL,
    proposed_content TEXT NOT NULL,
    rationale TEXT NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_prompt_proposals_status (status)
);
