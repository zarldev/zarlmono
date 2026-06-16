-- +migrate Up
CREATE TABLE IF NOT EXISTS prompt_templates (
  -- Stable identifier used by Go code to look up the template — e.g.
  -- "report_frontmatter", "report_header", "task_prompt", etc. Code
  -- is the authority on which keys exist; admin UI only edits them.
  template_key VARCHAR(128) NOT NULL PRIMARY KEY,
  content TEXT NOT NULL,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- +migrate Down
DROP TABLE IF EXISTS prompt_templates;
