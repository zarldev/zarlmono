-- +goose Up
-- +goose StatementBegin

-- storage records whether the material in ciphertext is AES-GCM ciphertext
-- ("vault") or plaintext credential bytes ("plaintext"). Existing rows were
-- all written by the vault path, so the default preserves current data.
ALTER TABLE api_keys
    ADD COLUMN storage TEXT NOT NULL DEFAULT 'vault';

-- Plaintext rows do not need an AES-GCM nonce, but the original schema made
-- nonce NOT NULL. Rebuild the table to relax that constraint while preserving
-- existing encrypted rows.
CREATE TABLE api_keys_new (
    workspace   TEXT    NOT NULL DEFAULT '',
    provider    TEXT    NOT NULL,
    ciphertext  BLOB    NOT NULL,
    nonce       BLOB,
    key_version INTEGER NOT NULL DEFAULT 1,
    updated_at  INTEGER NOT NULL,
    storage     TEXT    NOT NULL DEFAULT 'vault',
    PRIMARY KEY (workspace, provider)
);

INSERT INTO api_keys_new (workspace, provider, ciphertext, nonce, key_version, updated_at, storage)
SELECT workspace, provider, ciphertext, nonce, key_version, updated_at, storage
FROM api_keys;

DROP TABLE api_keys;
ALTER TABLE api_keys_new RENAME TO api_keys;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE TABLE api_keys_old (
    workspace   TEXT    NOT NULL DEFAULT '',
    provider    TEXT    NOT NULL,
    ciphertext  BLOB    NOT NULL,
    nonce       BLOB    NOT NULL,
    key_version INTEGER NOT NULL DEFAULT 1,
    updated_at  INTEGER NOT NULL,
    PRIMARY KEY (workspace, provider)
);

INSERT INTO api_keys_old (workspace, provider, ciphertext, nonce, key_version, updated_at)
SELECT workspace, provider, ciphertext, COALESCE(nonce, x''), key_version, updated_at
FROM api_keys;

DROP TABLE api_keys;
ALTER TABLE api_keys_old RENAME TO api_keys;
-- +goose StatementEnd
