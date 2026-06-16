USE zarl;

CREATE TABLE IF NOT EXISTS workspaces (
  name           VARCHAR(64) PRIMARY KEY,
  root           TEXT NOT NULL,
  default_branch VARCHAR(128) NOT NULL DEFAULT '',
  description    TEXT NOT NULL DEFAULT '',
  created_at     TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at     TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
                 ON UPDATE CURRENT_TIMESTAMP
);

-- Seed the single "default" workspace from the legacy settings row.
-- If the settings row is missing, use a placeholder path that the
-- operator is expected to change via the admin UI before running a
-- coder task.
INSERT INTO workspaces (name, root)
SELECT 'default',
       COALESCE(
         (SELECT value FROM settings WHERE `key` = 'coder.workspace_root' LIMIT 1),
         ''
       )
ON DUPLICATE KEY UPDATE root = root;

DELETE FROM settings WHERE `key` = 'coder.workspace_root';
