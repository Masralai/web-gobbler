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
`

// Migrate applies the jobs schema. Idempotent (IF NOT EXISTS).
func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, schemaSQL); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}
