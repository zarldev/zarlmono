CREATE TABLE IF NOT EXISTS tool_proposals (
    id VARCHAR(36) PRIMARY KEY,
    tool_name VARCHAR(100) NOT NULL,
    description TEXT NOT NULL,
    mcp_url VARCHAR(500) NOT NULL,
    rationale TEXT NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_proposals_status (status)
);
