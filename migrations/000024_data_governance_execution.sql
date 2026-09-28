ALTER TABLE data_governance_jobs
    ADD COLUMN worker_id TEXT,
    ADD COLUMN locked_until TIMESTAMPTZ,
    ADD COLUMN attempt INTEGER NOT NULL DEFAULT 0 CHECK (attempt >= 0);

DROP INDEX IF EXISTS data_governance_jobs_claim_idx;
CREATE INDEX data_governance_jobs_claim_idx
    ON data_governance_jobs (state, reversible_until, created_at)
    WHERE state IN ('queued', 'running');

CREATE TABLE data_governance_artifacts (
    job_id UUID PRIMARY KEY REFERENCES data_governance_jobs(id) ON DELETE CASCADE,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    filename TEXT NOT NULL,
    content_type TEXT NOT NULL,
    key_version TEXT NOT NULL,
    nonce BYTEA NOT NULL,
    ciphertext BYTEA NOT NULL,
    plaintext_sha256 TEXT NOT NULL,
    plaintext_bytes BIGINT NOT NULL CHECK (plaintext_bytes >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    CHECK (expires_at > created_at)
);

CREATE INDEX data_governance_artifacts_expiry_idx
    ON data_governance_artifacts (expires_at);
