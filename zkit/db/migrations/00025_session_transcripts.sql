-- +goose Up
-- +goose StatementBegin

-- Canonical renderer-independent thread metadata.
CREATE TABLE session_transcripts (
    session_id       TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
    revision         INTEGER NOT NULL DEFAULT 0 CHECK (revision >= 0),
    checksum         TEXT NOT NULL DEFAULT '',
    created_at_ms    INTEGER NOT NULL,
    updated_at_ms    INTEGER NOT NULL
);
-- Ordered semantic entries. Sequence is assigned once; entry content may be
-- replaced only by a higher transcript revision.
CREATE TABLE session_transcript_entries (
    session_id       TEXT NOT NULL REFERENCES session_transcripts(session_id) ON DELETE CASCADE,
    sequence         INTEGER NOT NULL CHECK (sequence > 0),
    entry_id         TEXT NOT NULL,
    parent_id        TEXT NOT NULL DEFAULT '',
    turn_id          TEXT NOT NULL DEFAULT '',
    kind             TEXT NOT NULL,
    payload_json     TEXT NOT NULL,
    revision         INTEGER NOT NULL CHECK (revision > 0),
    PRIMARY KEY (session_id, entry_id),
    UNIQUE (session_id, sequence)
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS session_transcript_entries;
DROP TABLE IF EXISTS session_transcripts;

-- +goose StatementEnd
