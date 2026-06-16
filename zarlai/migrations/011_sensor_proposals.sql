CREATE TABLE IF NOT EXISTS sensor_proposals (
    id VARCHAR(36) PRIMARY KEY,
    tool_name VARCHAR(100) NOT NULL,
    tool_args JSON NOT NULL,
    interval_seconds INT NOT NULL,
    rationale TEXT NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_sensor_proposals_status (status)
);
