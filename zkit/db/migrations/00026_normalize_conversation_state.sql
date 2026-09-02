-- +goose Up
-- +goose StatementBegin

-- Existing sessions are retained. Rows created before canonical transcripts
-- remain available for explicit legacy import at the application boundary.

-- The transcript is the conversation thread. This column is only the
-- compactable LLM context projection used for the next model request.
ALTER TABLE sessions RENAME COLUMN history_json TO context_json;

-- Legacy tool traces remain intact so upgrades are non-destructive. Canonical
-- code no longer reads or writes this compatibility column.

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Down restores the prior schema shape. Canonical transcript tables remain
-- independently owned by migration 25.
ALTER TABLE sessions RENAME COLUMN context_json TO history_json;

-- +goose StatementEnd
