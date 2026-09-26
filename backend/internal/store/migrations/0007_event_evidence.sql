-- 0007_event_evidence: enrich incident events so the timeline can separate
-- observed facts, hypotheses, decisions, and actions, and order by event time
-- while retaining ingestion time.

ALTER TABLE incident_events
    ADD COLUMN IF NOT EXISTS category    TEXT NOT NULL DEFAULT 'system',
    ADD COLUMN IF NOT EXISTS source      TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS target      TEXT NOT NULL DEFAULT '',
    -- observed_at is when the event happened (may differ from created_at, the
    -- ingestion time, when events arrive out of order).
    ADD COLUMN IF NOT EXISTS observed_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS incident_events_order_idx
    ON incident_events (incident_id, observed_at, seq);
