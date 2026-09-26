-- 0002_auth: operator identities and encrypted integration credentials.

-- Operators are the humans who sign in and later approve or deny production
-- actions. Identity comes from the customer's OIDC provider; the subject is the
-- stable per-issuer identifier.
CREATE TABLE IF NOT EXISTS operators (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    issuer     TEXT NOT NULL,
    subject    TEXT NOT NULL,
    email      TEXT NOT NULL DEFAULT '',
    name       TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_login TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (issuer, subject)
);

-- Integration credentials are stored encrypted at rest with AES-256-GCM. Only
-- ciphertext and nonce are persisted; plaintext never touches the database, an
-- API response, or a log line. The scope column enforces least privilege:
-- read, sandbox, and production credentials are distinct and never fetched by
-- the wrong scope.
CREATE TABLE IF NOT EXISTS integration_credentials (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    kind        TEXT NOT NULL,   -- kubernetes | prometheus | github | openai
    scope       TEXT NOT NULL,   -- read | sandbox | production
    ciphertext  BYTEA NOT NULL,
    nonce       BYTEA NOT NULL,
    fingerprint TEXT NOT NULL,   -- SHA-256 prefix of plaintext, for display only
    created_by  UUID NOT NULL REFERENCES operators (id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (scope IN ('read', 'sandbox', 'production')),
    CHECK (kind IN ('kubernetes', 'prometheus', 'github', 'openai')),
    UNIQUE (kind, scope)
);
