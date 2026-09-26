-- 0004_incidents: incidents, their current state, and an append-only event log.
--
-- The incidents row is the current-state projection; incident_events is the
-- immutable audit history. Every state transition writes both atomically in one
-- transaction, and the row version supports optimistic concurrency so two
-- concurrent transitions cannot silently overwrite each other.

CREATE TABLE IF NOT EXISTS incidents (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    service_id     UUID REFERENCES services (id),
    service_key    TEXT NOT NULL,
    environment    TEXT NOT NULL,
    alert_identity TEXT NOT NULL DEFAULT '',
    summary        TEXT NOT NULL DEFAULT '',
    symptoms       JSONB NOT NULL DEFAULT '{}'::jsonb,
    state          TEXT NOT NULL,
    version        BIGINT NOT NULL DEFAULT 1,
    correlation_id TEXT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    closed_at      TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS incidents_state_idx ON incidents (state);
CREATE INDEX IF NOT EXISTS incidents_service_idx ON incidents (service_key, environment);

CREATE TABLE IF NOT EXISTS incident_events (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    incident_id    UUID NOT NULL REFERENCES incidents (id),
    seq            BIGINT NOT NULL,
    type           TEXT NOT NULL,
    from_state     TEXT NOT NULL DEFAULT '',
    to_state       TEXT NOT NULL DEFAULT '',
    actor          TEXT NOT NULL DEFAULT '',
    reason         TEXT NOT NULL DEFAULT '',
    correlation_id TEXT NOT NULL DEFAULT '',
    data           JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- One sequence number per incident keeps the history strictly ordered and
    -- prevents a duplicate append for the same logical step.
    UNIQUE (incident_id, seq)
);

CREATE INDEX IF NOT EXISTS incident_events_incident_idx ON incident_events (incident_id, seq);
