ALTER TABLE jobs ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'scrape';
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS parent_id UUID;
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS request JSONB;
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS idempotency_key TEXT;
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS webhook JSONB;
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS depth INTEGER;
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS lease_expires_at TIMESTAMPTZ;
ALTER TABLE jobs ALTER COLUMN duration_ms TYPE BIGINT;

CREATE INDEX IF NOT EXISTS idx_jobs_parent ON jobs(parent_id, created_at, id);
CREATE INDEX IF NOT EXISTS idx_jobs_kind_status ON jobs(kind, status);
CREATE INDEX IF NOT EXISTS idx_jobs_lease ON jobs(status, lease_expires_at);

CREATE TABLE IF NOT EXISTS job_idempotency (
    key        TEXT PRIMARY KEY,
    job_id     UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS outbox (
    id         BIGSERIAL PRIMARY KEY,
    stream     TEXT NOT NULL,
    payload    JSONB NOT NULL,
    sent_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_outbox_unsent ON outbox(id) WHERE sent_at IS NULL;

CREATE TABLE IF NOT EXISTS schema_migrations (
    version    TEXT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO schema_migrations (version) VALUES ('000002_v2_jobs')
ON CONFLICT (version) DO NOTHING;
