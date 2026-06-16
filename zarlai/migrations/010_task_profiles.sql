USE zarl;

ALTER TABLE tasks
  ADD COLUMN profile_name VARCHAR(64) NOT NULL DEFAULT 'default';

CREATE INDEX idx_tasks_profile ON tasks(profile_name);

CREATE TABLE IF NOT EXISTS task_profile_overrides (
  profile_name    VARCHAR(64) NOT NULL PRIMARY KEY,
  model           VARCHAR(128) NULL,
  prompt_prefix   TEXT NULL,
  max_iterations  INT NULL,
  updated_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
                  ON UPDATE CURRENT_TIMESTAMP
);
