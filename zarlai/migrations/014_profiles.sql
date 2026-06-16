USE zarl;

CREATE TABLE IF NOT EXISTS profiles (
  name              VARCHAR(64) PRIMARY KEY,
  model             VARCHAR(128) NOT NULL DEFAULT '',
  prompt_prefix     TEXT NOT NULL,
  max_iterations    INT NOT NULL DEFAULT 0,
  tool_names        TEXT NOT NULL,          -- JSON array of tool names
  provider_whitelist TEXT NOT NULL,         -- JSON array of provider names
  source            ENUM('builtin','user') NOT NULL DEFAULT 'user',
  updated_at        TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
                    ON UPDATE CURRENT_TIMESTAMP
);
