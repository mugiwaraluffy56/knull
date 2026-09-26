-- 0003_services: configured services and their environment-scoped mappings.
--
-- A service is a stable product identity in one environment. Every service maps
-- to exactly one Kubernetes workload, one Prometheus label selector, and one
-- GitHub repository/ref. The (key, environment) pair is unique so the same
-- logical service can exist in several environments without a mapping ever
-- silently spanning two of them.

CREATE TABLE IF NOT EXISTS services (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    key                 TEXT NOT NULL,
    display_name        TEXT NOT NULL,
    environment         TEXT NOT NULL,

    k8s_cluster         TEXT NOT NULL,
    k8s_namespace       TEXT NOT NULL,
    k8s_workload        TEXT NOT NULL,

    -- Prometheus label selector as a JSON object of label -> value.
    prometheus_labels   JSONB NOT NULL DEFAULT '{}'::jsonb,

    github_repo         TEXT NOT NULL DEFAULT '',  -- owner/repo, optional
    github_ref          TEXT NOT NULL DEFAULT '',  -- branch or ref, optional

    -- Reference to a recovery policy (defined by a later task). No universal
    -- numeric thresholds are invented here.
    recovery_policy_ref TEXT NOT NULL DEFAULT '',

    enabled             BOOLEAN NOT NULL DEFAULT TRUE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (key, environment)
);

CREATE INDEX IF NOT EXISTS services_environment_idx ON services (environment);
