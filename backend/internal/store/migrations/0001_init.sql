-- 0001_init: baseline schema for the incident API workspace.
--
-- This baseline establishes the foundational tables so a clean checkout can
-- start with a real, migrated database. Incident, event, and service tables
-- are introduced by later implementation tasks; this migration keeps the
-- workspace bootable and proves the migration runner end to end.

CREATE TABLE IF NOT EXISTS app_info (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO app_info (key, value)
VALUES ('schema', 'knull-incident-api')
ON CONFLICT (key) DO NOTHING;
