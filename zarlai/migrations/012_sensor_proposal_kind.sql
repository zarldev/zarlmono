-- Extend sensor_proposals to cover reactive (event-driven) sensors alongside
-- the existing poll kind. `kind` discriminates which runtime class will
-- instantiate the sensor; `entity_id` is reactive-only (HA state_changed,
-- MQTT topic, etc.) and NULL for poll kinds.
--
-- Existing rows (all poll) backfill to kind='poll' via the DEFAULT.

ALTER TABLE sensor_proposals
    ADD COLUMN kind VARCHAR(32) NOT NULL DEFAULT 'poll',
    ADD COLUMN entity_id VARCHAR(255) NULL;

-- Poll sensors own tool_name+tool_args+interval. Reactive sensors may still
-- carry tool_name (e.g. a downstream tool to invoke when the event fires) but
-- most won't; relax NOT NULL by changing the columns where useful. Leave
-- interval_seconds as-is: it's ignored for reactive kinds.
ALTER TABLE sensor_proposals
    MODIFY COLUMN tool_name VARCHAR(100) NULL,
    MODIFY COLUMN tool_args JSON NULL,
    MODIFY COLUMN interval_seconds INT NULL;

CREATE INDEX idx_sensor_proposals_kind ON sensor_proposals (kind);
