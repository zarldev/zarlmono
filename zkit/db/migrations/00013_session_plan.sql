-- +goose Up
-- +goose StatementBegin

-- plan_json stores the session's latest structured plan (from the
-- update_plan tool) as a JSON blob, so the plan overlay (ctrl+p) and the
-- transcript plan notices reappear on -continue. 'null' when the session
-- never produced a plan.
ALTER TABLE sessions
    ADD COLUMN plan_json TEXT NOT NULL DEFAULT 'null';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN plan_json;
-- +goose StatementEnd
