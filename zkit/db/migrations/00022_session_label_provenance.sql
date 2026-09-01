-- +goose Up
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN label_manual INTEGER NOT NULL DEFAULT 0 CHECK (label_manual IN (0, 1));
UPDATE sessions SET label_manual = 1 WHERE TRIM(label) <> '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN label_manual;
-- +goose StatementEnd
