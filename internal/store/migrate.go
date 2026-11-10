package store

import (
	"context"
	"fmt"
)

// schemaSQL mirrors migrations/000001_create_jobs_table.up.sql.
// ponytail: run on boot via Migrate; no golang-migrate binary in distroless.
const schemaSQL = `
CREATE TABLE IF NOT EXISTS jobs (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    url           TEXT NOT NULL,
    extract       TEXT[] NOT NULL,
    options       JSONB,
    status        TEXT NOT NULL DEFAULT 'queued',
    result        JSONB,
    error_msg     TEXT,
    http_status   INT,
    retries_used  INT DEFAULT 0,
    duration_ms   INT,
    created_at    TIMESTAMPTZ DEFAULT now(),
    updated_at    TIMESTAMPTZ DEFAULT now(),
    completed_at  TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs(status);
CREATE INDEX IF NOT EXISTS idx_jobs_created_at ON jobs(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_jobs_queued ON jobs(status) WHERE status = 'queued';

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
`

// Migrate applies the jobs schema. Idempotent (IF NOT EXISTS).
func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, schemaSQL); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}
