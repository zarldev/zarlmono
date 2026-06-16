-- +migrate Up
CREATE TABLE IF NOT EXISTS tool_description_overrides (
  name VARCHAR(128) NOT NULL PRIMARY KEY,
  description TEXT NOT NULL,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- +migrate Down
DROP TABLE IF EXISTS tool_description_overrides;
