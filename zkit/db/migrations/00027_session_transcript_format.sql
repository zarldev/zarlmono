-- +goose Up
-- +goose StatementBegin

-- Version the renderer-independent transcript payload contract so readers can
-- reject newer formats deliberately and migrate older formats explicitly.
ALTER TABLE session_transcripts
ADD COLUMN format_version INTEGER NOT NULL DEFAULT 1 CHECK (format_version > 0);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE session_transcripts DROP COLUMN format_version;

-- +goose StatementEnd
