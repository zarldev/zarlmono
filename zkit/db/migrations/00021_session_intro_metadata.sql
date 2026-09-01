-- +goose Up
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN changed_file_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sessions ADD COLUMN plan_completed_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sessions ADD COLUMN plan_total_count INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN plan_total_count;
ALTER TABLE sessions DROP COLUMN plan_completed_count;
ALTER TABLE sessions DROP COLUMN changed_file_count;
-- +goose StatementEnd
