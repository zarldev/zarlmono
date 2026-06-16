USE zarl;

CREATE TABLE IF NOT EXISTS tasks (
    id VARCHAR(36) PRIMARY KEY,
    prompt TEXT NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    summary TEXT NOT NULL DEFAULT '',
    iterations INT NOT NULL DEFAULT 0,
    max_iterations INT NOT NULL DEFAULT 20,
    person_name VARCHAR(100) NOT NULL DEFAULT '',
    session_id VARCHAR(100) NOT NULL DEFAULT '',
    schedule VARCHAR(50) NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_tasks_status (status),
    INDEX idx_tasks_schedule (schedule)
);
