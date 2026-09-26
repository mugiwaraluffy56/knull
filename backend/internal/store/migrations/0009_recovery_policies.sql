-- Recovery policies are immutable snapshots. Assessment records can reference
-- (service_id, version) even after an operator updates the current policy.
CREATE TABLE IF NOT EXISTS recovery_policies (
    service_id UUID NOT NULL REFERENCES services (id),
    version BIGINT NOT NULL CHECK (version > 0),
    digest TEXT NOT NULL,
    policy JSONB NOT NULL,
    configured_by UUID NOT NULL REFERENCES operators (id),
    configured_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (service_id, version)
);

CREATE INDEX IF NOT EXISTS recovery_policies_current_idx
    ON recovery_policies (service_id, version DESC);

CREATE TABLE IF NOT EXISTS recovery_assessments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    incident_id UUID NOT NULL REFERENCES incidents (id),
    service_id UUID NOT NULL,
    policy_version BIGINT NOT NULL,
    policy_digest TEXT NOT NULL,
    window_start TIMESTAMPTZ NOT NULL,
    window_end TIMESTAMPTZ NOT NULL,
    observation JSONB NOT NULL,
    outcome TEXT NOT NULL CHECK (outcome IN ('RECOVERED','NOT_RECOVERED','UNCERTAIN')),
    missing JSONB NOT NULL DEFAULT '[]'::jsonb,
    failed JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (service_id, policy_version) REFERENCES recovery_policies (service_id, version),
    CHECK (window_end > window_start)
);

CREATE INDEX IF NOT EXISTS recovery_assessments_incident_idx
    ON recovery_assessments (incident_id, created_at DESC);
