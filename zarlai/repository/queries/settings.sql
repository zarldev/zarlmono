-- name: GetSetting :one
SELECT `key`, value, updated_at FROM settings WHERE `key` = ?;

-- name: UpsertSetting :exec
INSERT INTO settings (`key`, value, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)
ON DUPLICATE KEY UPDATE value = VALUES(value), updated_at = CURRENT_TIMESTAMP;
