-- +migrate Up
CREATE TABLE IF NOT EXISTS skills (
  id VARCHAR(36) NOT NULL PRIMARY KEY,
  name VARCHAR(128) NOT NULL UNIQUE,
  description TEXT NOT NULL,
  markdown TEXT NOT NULL,
  -- profile_binding NULL = global (any profile). Otherwise the literal
  -- profile name it applies to ("default", "researcher", "coder").
  profile_binding VARCHAR(64),
  enabled BOOLEAN NOT NULL DEFAULT TRUE,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_skills_profile_binding ON skills (profile_binding);

-- Proposal table mirrors prompt_proposals: the LLM writes here, a human
-- approves via admin UI, approval flips the active skill row.
CREATE TABLE IF NOT EXISTS skill_proposals (
  id VARCHAR(36) NOT NULL PRIMARY KEY,
  -- Nullable: if set, proposes an update to an existing skill.
  target_skill_id VARCHAR(36),
  proposed_name VARCHAR(128) NOT NULL,
  proposed_description TEXT NOT NULL,
  proposed_markdown TEXT NOT NULL,
  proposed_binding VARCHAR(64),
  rationale TEXT NOT NULL,
  status VARCHAR(16) NOT NULL DEFAULT 'pending',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  reviewed_at DATETIME
);
CREATE INDEX IF NOT EXISTS idx_skill_proposals_status ON skill_proposals (status);

-- +migrate Down
DROP TABLE IF EXISTS skill_proposals;
DROP TABLE IF EXISTS skills;
