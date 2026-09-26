-- 0006_workflow: link incidents to their durable TrueForge workflow session so
-- a long-running investigation can be resumed after a backend restart.

ALTER TABLE incidents
    ADD COLUMN IF NOT EXISTS workflow_session_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS workflow_run_id     TEXT NOT NULL DEFAULT '';

-- Resume lookups target incidents that have a session and are not finished.
CREATE INDEX IF NOT EXISTS incidents_workflow_resume_idx
    ON incidents (state)
    WHERE workflow_session_id <> '';
