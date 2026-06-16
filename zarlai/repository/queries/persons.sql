-- name: ListPersons :many
SELECT id, name, embedding, notes, photo, created_at, updated_at
FROM persons ORDER BY name;

-- name: CreatePerson :exec
INSERT INTO persons (id, name, embedding, notes, photo) VALUES (?, ?, ?, ?, ?);

-- name: UpdatePerson :exec
UPDATE persons SET name = ?, embedding = ?, notes = ?, photo = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: DeletePerson :exec
DELETE FROM persons WHERE id = ?;

-- name: DeletePersonByName :exec
DELETE FROM persons WHERE name = ?;

-- name: GetPersonByName :one
SELECT id, name, embedding, notes, photo, created_at, updated_at
FROM persons WHERE name = ?;

-- name: UpdatePersonNotes :exec
UPDATE persons SET name = ?, notes = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: DeleteAllPersons :execrows
DELETE FROM persons;
