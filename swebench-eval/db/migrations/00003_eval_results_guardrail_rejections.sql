-- +goose Up
-- +goose StatementBegin

-- Per-guardrail rejection counts for the run, serialized as a JSON
-- object ({"fanout": 3, "shell_policy": 1}); empty string when the
-- driver surfaced no transcript. This is the ablation trigger
-- telemetry: a guardrail that never fired can't show a resolve-rate
-- delta, so arms are only comparable alongside how often each
-- mechanism actually engaged.
ALTER TABLE eval_results ADD COLUMN guardrail_rejections TEXT NOT NULL DEFAULT '';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE eval_results DROP COLUMN guardrail_rejections;
-- +goose StatementEnd
