-- +goose Up
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN message_count INTEGER NOT NULL DEFAULT 0;
UPDATE sessions
SET message_count = CASE
    WHEN json_valid(history_json) AND json_type(history_json) = 'array'
        THEN json_array_length(history_json)
    ELSE 0
END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN message_count;
-- +goose StatementEnd
