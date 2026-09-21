-- +goose Up
ALTER TABLE tool_output_history ADD COLUMN parent_execution_id TEXT NOT NULL DEFAULT '';
ALTER TABLE tool_output_history ADD COLUMN task_id TEXT NOT NULL DEFAULT '';
ALTER TABLE tool_output_history ADD COLUMN attempt INTEGER NOT NULL DEFAULT 0 CHECK (attempt >= 0);
ALTER TABLE tool_output_history ADD COLUMN sequence INTEGER NOT NULL DEFAULT 0 CHECK (sequence >= 0);
ALTER TABLE tool_output_history ADD COLUMN dispatched INTEGER CHECK (dispatched IN (0, 1));

-- +goose Down
ALTER TABLE tool_output_history DROP COLUMN dispatched;
ALTER TABLE tool_output_history DROP COLUMN sequence;
ALTER TABLE tool_output_history DROP COLUMN attempt;
ALTER TABLE tool_output_history DROP COLUMN task_id;
ALTER TABLE tool_output_history DROP COLUMN parent_execution_id;
