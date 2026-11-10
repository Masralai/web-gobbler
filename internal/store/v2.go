package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	KindScrape  = "scrape"
	KindCrawl   = "crawl"
	KindBatch   = "batch"
	KindExtract = "extract"
	KindPage    = "page"

	StatusCancelled = "cancelled"
)

type V2Job struct {
	ID             uuid.UUID
	Kind           string
	URL            string
	ParentID       *uuid.UUID
	Request        json.RawMessage
	Webhook        json.RawMessage
	Depth          int
	Status         string
	Result         json.RawMessage
	ErrorMsg       *string
	HTTPStatus     *int
	RetriesUsed    int
	DurationMs     *int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
	CompletedAt    *time.Time
	LeaseExpiresAt *time.Time
}

type ReapedJob struct {
	ID       uuid.UUID
	Kind     string
	ParentID *uuid.UUID
	URL      string
	Depth    int
}

func (s *Store) CreateV2Job(ctx context.Context, job *V2Job) (uuid.UUID, error) {
	kind := job.Kind
	if kind == "" {
		kind = KindScrape
	}
	status := job.Status
	if status == "" {
		status = "queued"
	}
	var id uuid.UUID
	err := s.pool.QueryRow(ctx,
		`INSERT INTO jobs (url, extract, options, status, kind, parent_id, request, webhook, depth)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 RETURNING id, created_at, updated_at`,
		job.URL, []string{}, nil, status, kind, job.ParentID, rawOrNil(job.Request), rawOrNil(job.Webhook), nullInt(job.Depth),
	).Scan(&id, &job.CreatedAt, &job.UpdatedAt)
	if err != nil {
		return uuid.Nil, fmt.Errorf("insert v2 job: %w", err)
	}
	job.ID = id
	job.Kind = kind
	job.Status = status
	return id, nil
}

func (s *Store) GetV2Job(ctx context.Context, id uuid.UUID) (*V2Job, error) {
	var job V2Job
	var parent *uuid.UUID
	var requestJSON, webhookJSON, resultJSON *string
	var depth *int
	err := s.pool.QueryRow(ctx,
		`SELECT id, kind, url, parent_id, request::TEXT, webhook::TEXT, depth,
		        status, result::TEXT, error_msg, http_status, retries_used,
		        duration_ms, created_at, updated_at, completed_at, lease_expires_at
		 FROM jobs WHERE id = $1`, id,
	).Scan(
		&job.ID, &job.Kind, &job.URL, &parent, &requestJSON, &webhookJSON, &depth,
		&job.Status, &resultJSON, &job.ErrorMsg, &job.HTTPStatus, &job.RetriesUsed,
		&job.DurationMs, &job.CreatedAt, &job.UpdatedAt, &job.CompletedAt, &job.LeaseExpiresAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return nil, fmt.Errorf("get v2 job: %w", err)
	}
	job.ParentID = parent
	if depth != nil {
		job.Depth = *depth
	}
	job.Request = rawJSON(requestJSON)
	job.Webhook = rawJSON(webhookJSON)
	job.Result = rawJSON(resultJSON)
	return &job, nil
}

func (s *Store) CreateV2Child(ctx context.Context, parent uuid.UUID, rawURL string, depth int, request json.RawMessage) (uuid.UUID, error) {
	job := &V2Job{
		Kind:     KindPage,
		URL:      rawURL,
		ParentID: &parent,
		Depth:    depth,
		Request:  request,
	}
	return s.CreateV2Job(ctx, job)
}

func (s *Store) SetV2Processing(ctx context.Context, id uuid.UUID) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE jobs SET status = 'processing', lease_expires_at = now() + interval '60 seconds', updated_at = now()
		 WHERE id = $1 AND status = 'queued'`, id)
	if err != nil {
		return false, fmt.Errorf("claim v2 job: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func (s *Store) UpdateV2Job(ctx context.Context, id uuid.UUID, status string, result json.RawMessage, errorMsg *string, httpStatus *int, retries int, durationMs *int64) error {
	var completedAt *time.Time
	if status == "completed" || status == "failed" || status == StatusCancelled {
		now := time.Now()
		completedAt = &now
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE jobs
		 SET status = $1,
		     result = CASE WHEN $2::JSONB IS NULL THEN result ELSE $2::JSONB END,
		     error_msg = $3,
		     http_status = COALESCE($4, http_status),
		     retries_used = $5,
		     duration_ms = COALESCE($6, duration_ms),
		     completed_at = COALESCE($7, completed_at),
		     lease_expires_at = NULL,
		     updated_at = now()
		 WHERE id = $8`,
		status, rawOrNil(result), errorMsg, httpStatus, retries, durationMs, completedAt, id)
	if err != nil {
		return fmt.Errorf("update v2 job: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return nil
}

func (s *Store) ListV2Children(ctx context.Context, parent uuid.UUID, status string, limit, offset int) ([]*V2Job, error) {
	if limit < 1 || limit > 100 {
		limit = 100
	}
	query := `SELECT id, kind, url, parent_id, request::TEXT, webhook::TEXT, depth,
	                 status, result::TEXT, error_msg, http_status, retries_used,
	                 duration_ms, created_at, updated_at, completed_at, lease_expires_at
	          FROM jobs WHERE parent_id = $1`
	args := []any{parent}
	if status != "" {
		query += ` AND status = $2`
		args = append(args, status)
	}
	query += fmt.Sprintf(` ORDER BY created_at, id LIMIT $%d OFFSET $%d`, len(args)+1, len(args)+2)
	args = append(args, limit, offset)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list v2 children: %w", err)
	}
	defer rows.Close()
	var out []*V2Job
	for rows.Next() {
		job, err := scanV2Job(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, job)
	}
	return out, rows.Err()
}

func (s *Store) V2ChildCounts(ctx context.Context, parent uuid.UUID) (completed, processing, queued, total int, err error) {
	err = s.pool.QueryRow(ctx,
		`SELECT
		   count(*) FILTER (WHERE status = 'completed'),
		   count(*) FILTER (WHERE status = 'processing'),
		   count(*) FILTER (WHERE status = 'queued'),
		   count(*)
		 FROM jobs WHERE parent_id = $1`, parent,
	).Scan(&completed, &processing, &queued, &total)
	return
}

func (s *Store) CancelV2Job(ctx context.Context, id uuid.UUID) (string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var status string
	err = tx.QueryRow(ctx,
		`UPDATE jobs SET status = 'cancelled', error_msg = 'cancelled', updated_at = now()
		 WHERE id = $1 AND status IN ('queued', 'processing') RETURNING status`, id,
	).Scan(&status)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return "", err
		}
		if err := tx.QueryRow(ctx, `SELECT status FROM jobs WHERE id = $1`, id).Scan(&status); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return "", fmt.Errorf("%w: %s", ErrNotFound, id)
			}
			return "", err
		}
		return status, nil
	}
	if _, err := tx.Exec(ctx,
		`UPDATE jobs SET status = 'cancelled', error_msg = 'cancelled', updated_at = now()
		 WHERE parent_id = $1 AND status IN ('queued', 'processing')`, id); err != nil {
		return "", err
	}
	return status, tx.Commit(ctx)
}

func (s *Store) ListOngoingCrawls(ctx context.Context) ([]*V2Job, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, kind, url, parent_id, request::TEXT, webhook::TEXT, depth,
		        status, result::TEXT, error_msg, http_status, retries_used,
		        duration_ms, created_at, updated_at, completed_at, lease_expires_at
		 FROM jobs WHERE kind = 'crawl' AND status IN ('queued', 'processing')
		 ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*V2Job
	for rows.Next() {
		job, err := scanV2Job(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, job)
	}
	return out, rows.Err()
}

func (s *Store) FindIdempotency(ctx context.Context, key string) (uuid.UUID, bool, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx,
		`SELECT job_id FROM job_idempotency
		 WHERE key = $1 AND created_at > now() - interval '24 hours'`, key).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	return id, true, nil
}

func (s *Store) SaveIdempotency(ctx context.Context, key string, jobID uuid.UUID) (bool, error) {
	if _, err := s.pool.Exec(ctx,
		`DELETE FROM job_idempotency WHERE key = $1 AND created_at <= now() - interval '24 hours'`, key); err != nil {
		return false, err
	}
	tag, err := s.pool.Exec(ctx,
		`INSERT INTO job_idempotency (key, job_id) VALUES ($1, $2)
		 ON CONFLICT (key) DO NOTHING`, key, jobID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (s *Store) ReapV2Leases(ctx context.Context) ([]ReapedJob, error) {
	rows, err := s.pool.Query(ctx,
		`UPDATE jobs SET status = 'queued', lease_expires_at = NULL, updated_at = now()
		 WHERE status = 'processing' AND lease_expires_at IS NOT NULL AND lease_expires_at < now()
		 RETURNING id, kind, parent_id, url, COALESCE(depth, 0)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReapedJob
	for rows.Next() {
		var r ReapedJob
		if err := rows.Scan(&r.ID, &r.Kind, &r.ParentID, &r.URL, &r.Depth); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) CleanupV2(ctx context.Context, minutes int) error {
	if minutes < 1 {
		minutes = 60
	}
	if _, err := s.pool.Exec(ctx,
		`DELETE FROM jobs WHERE created_at < now() - ($1 || ' minutes')::interval`, minutes); err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx,
		`DELETE FROM job_idempotency WHERE created_at < now() - interval '24 hours'`); err != nil {
		return err
	}
	return nil
}

func (s *Store) InFlightCount(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE status = 'processing'`).Scan(&n)
	return n, err
}

func scanV2Job(rows pgx.Rows) (*V2Job, error) {
	var job V2Job
	var parent *uuid.UUID
	var requestJSON, webhookJSON, resultJSON *string
	var depth *int
	if err := rows.Scan(
		&job.ID, &job.Kind, &job.URL, &parent, &requestJSON, &webhookJSON, &depth,
		&job.Status, &resultJSON, &job.ErrorMsg, &job.HTTPStatus, &job.RetriesUsed,
		&job.DurationMs, &job.CreatedAt, &job.UpdatedAt, &job.CompletedAt, &job.LeaseExpiresAt,
	); err != nil {
		return nil, err
	}
	job.ParentID = parent
	if depth != nil {
		job.Depth = *depth
	}
	job.Request = rawJSON(requestJSON)
	job.Webhook = rawJSON(webhookJSON)
	job.Result = rawJSON(resultJSON)
	return &job, nil
}

func rawOrNil(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return []byte(raw)
}

func rawJSON(s *string) json.RawMessage {
	if s == nil || *s == "" {
		return nil
	}
	return json.RawMessage(*s)
}

func nullInt(n int) any {
	if n == 0 {
		return nil
	}
	return n
}
