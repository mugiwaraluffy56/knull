-- Decisions are immutable and unique per proposed action. A denied or expired
-- action needs a new plan, validation, and explicit operator decision.
CREATE TABLE IF NOT EXISTS action_approvals (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    incident_id UUID NOT NULL REFERENCES incidents (id),
    action_event_id UUID NOT NULL REFERENCES incident_events (id),
    action_digest TEXT NOT NULL,
    decision TEXT NOT NULL CHECK (decision IN ('APPROVED', 'DENIED')),
    operator_id UUID NOT NULL REFERENCES operators (id),
    operator_email TEXT NOT NULL,
    reason TEXT NOT NULL DEFAULT '',
    decided_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ,
    invalidated_at TIMESTAMPTZ,
    invalidation_reason TEXT NOT NULL DEFAULT '',
    UNIQUE (action_event_id),
    CHECK ((decision = 'APPROVED' AND expires_at IS NOT NULL)
        OR (decision = 'DENIED' AND expires_at IS NULL))
);

CREATE INDEX IF NOT EXISTS action_approvals_incident_idx
    ON action_approvals (incident_id, decided_at DESC);
