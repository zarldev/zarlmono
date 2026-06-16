USE zarl;

ALTER TABLE task_profile_overrides
  ADD COLUMN tool_names TEXT NULL;
