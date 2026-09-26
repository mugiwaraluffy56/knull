-- 0005_alert_intake: record rejected/malformed alert deliveries and speed up
-- deduplication lookups.

CREATE TABLE IF NOT EXISTS alert_intake_failures (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    reason      TEXT NOT NULL,
    remote_addr TEXT NOT NULL DEFAULT '',
    detail      TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Deduplication looks up an active incident by service, environment, and alert
-- identity; this index keeps that lookup cheap.
CREATE INDEX IF NOT EXISTS incidents_alert_dedup_idx
    ON incidents (service_key, environment, alert_identity, state);
