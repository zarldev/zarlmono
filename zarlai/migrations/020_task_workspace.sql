USE zarl;

ALTER TABLE tasks
  ADD COLUMN workspace_name VARCHAR(64) NULL;

CREATE INDEX idx_tasks_workspace ON tasks(workspace_name);
