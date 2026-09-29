-- The durable cursor prevents one busy tenant from monopolizing the database
-- recovery worker when a broker outage or restart leaves many queued reviews.
CREATE TABLE IF NOT EXISTS tenant_review_dispatches (
  tenant_id UUID PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
  last_claimed_at TIMESTAMPTZ NOT NULL,
  last_job_id UUID NOT NULL REFERENCES review_jobs(id) ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS tenant_review_dispatches_last_claimed_idx
  ON tenant_review_dispatches (last_claimed_at, tenant_id);
