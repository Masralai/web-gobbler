// Package store implements PostgreSQL persistence for scrape jobs using pgx/v5.
// It provides CRUD operations for jobs (CreateJob, GetJob, UpdateJob, ListJobs, CancelJob),
// a Ping method for health checks, and handles JSONB serialisation of scrape results and options.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Masralai/web-gobbler/internal/scraper"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// JobStatus represents the lifecycle state of a scrape job.
type JobStatus string

const (
	// JobStatusQueued indicates the job has been created and enqueued but not yet picked up by a worker.
	JobStatusQueued JobStatus = "queued"
	// JobStatusProcessing indicates a worker has dequeued the job and is actively scraping.
	JobStatusProcessing JobStatus = "processing"
	// JobStatusCompleted indicates the scrape succeeded and the result has been persisted.
	JobStatusCompleted JobStatus = "completed"
	// JobStatusFailed indicates all retry attempts were exhausted or the job was cancelled.
	JobStatusFailed JobStatus = "failed"
)

// JobOptions holds optional parameters attached to a job at creation time.
// Each field is a pointer so that nil can distinguish "not set" from a zero value.
type JobOptions struct {
	TimeoutSeconds  *int  `json:"timeout_seconds,omitempty"`
	MaxRetries      *int  `json:"max_retries,omitempty"`
	FollowRedirects *bool `json:"follow_redirects,omitempty"`
}

type Job struct {
	ID          uuid.UUID       `json:"id"`
	URL         string          `json:"url"`
	Extract     []string        `json:"extract"`
	Options     *JobOptions     `json:"options,omitempty"`
	Status      JobStatus       `json:"status"`
	Result      *scraper.Result `json:"result,omitempty"`
	ErrorMsg    *string         `json:"error_msg,omitempty"`
	HTTPStatus  *int            `json:"http_status,omitempty"`
	RetriesUsed int             `json:"retries_used"`
	DurationMs  *int64          `json:"duration_ms,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
	CompletedAt *time.Time      `json:"completed_at,omitempty"`
}

var (
	// ErrNotFound is returned when a job ID does not exist in the database.
	ErrNotFound = errors.New("job not found")
	// ErrCannotCancel is returned when attempting to cancel a job whose current
	// status is not "queued" (e.g. it is already processing, completed, or failed).
	ErrCannotCancel = errors.New("cannot cancel job with status")
)

// Store wraps a pgxpool connection pool and provides CRUD operations on the jobs table.
type Store struct {
	pool *pgxpool.Pool
}

// New connects to PostgreSQL, verifies the connection with a ping, and returns a Store.
// The pool is configured with a maximum of 10 concurrent connections.
func New(ctx context.Context, databaseURL string) (*Store, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	config.MaxConns = 10

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}

	return &Store{pool: pool}, nil
}

// Ping verifies the database connection is still alive.
func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

// Close shuts down the connection pool and releases all database connections.
func (s *Store) Close() {
	s.pool.Close()
}

// Pool returns the underlying pgxpool.Pool for advanced use cases.
func (s *Store) Pool() *pgxpool.Pool {
	return s.pool
}

// CreateJob inserts a new job row with status "queued" and returns the generated UUID and creation timestamp.
func (s *Store) CreateJob(ctx context.Context, job *Job) (uuid.UUID, error) {
	var id uuid.UUID
	var createdAt time.Time
	err := s.pool.QueryRow(ctx,
		`INSERT INTO jobs (url, extract, options, status)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, created_at`,
		job.URL, job.Extract, job.Options, JobStatusQueued,
	).Scan(&id, &createdAt)
	if err != nil {
		return uuid.Nil, fmt.Errorf("insert job: %w", err)
	}
	job.CreatedAt = createdAt
	job.ID = id
	return id, nil
}

// GetJob retrieves a single job by its UUID. Returns ErrNotFound if the job does not exist.
// The result and options JSONB columns are deserialised from TEXT casts in the query.
func (s *Store) GetJob(ctx context.Context, id uuid.UUID) (*Job, error) {
	var job Job
	var resultJSON, errorMsg *string
	var httpStatus *int
	var durationMs *int64
	var completedAt *time.Time
	var optsJSON *string

	err := s.pool.QueryRow(ctx,
		`SELECT id, url, extract, options::TEXT, status, result::TEXT, error_msg,
		        http_status, retries_used, duration_ms, created_at, updated_at, completed_at
		 FROM jobs WHERE id = $1`, id,
	).Scan(
		&job.ID, &job.URL, &job.Extract, &optsJSON, &job.Status,
		&resultJSON, &errorMsg, &httpStatus, &job.RetriesUsed, &durationMs,
		&job.CreatedAt, &job.UpdatedAt, &completedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return nil, fmt.Errorf("query job: %w", err)
	}

	job.Result = parseResultJSON(resultJSON)
	job.Options = parseOptionsJSON(optsJSON)
	job.ErrorMsg = errorMsg
	job.HTTPStatus = httpStatus
	job.DurationMs = durationMs
	job.CompletedAt = completedAt

	return &job, nil
}

// UpdateJob advances a job's status and conditionally sets result, error message,
// HTTP status, timing fields, and retries used. Only non-nil fields are overwritten.
// When status is "completed" or "failed" the completed_at timestamp is set to now.
func (s *Store) UpdateJob(ctx context.Context, id uuid.UUID, status JobStatus, result *scraper.Result, errorMsg *string, retriesUsed int) error {
	var httpStatus *int
	var durationMs *int64
	var completedAt *time.Time
	var retries *int

	if result != nil {
		httpStatus = &result.HTTPStatus
		durationMs = &result.DurationMs
	}

	if status == JobStatusFailed || status == JobStatusCompleted {
		retries = &retriesUsed
	}

	now := time.Now()

	if status == JobStatusCompleted || status == JobStatusFailed {
		completedAt = &now
	}

	tag, err := s.pool.Exec(ctx,
		`UPDATE jobs
		 SET status = $1,
		     result = CASE WHEN $2::JSONB IS NOT NULL THEN $2::JSONB ELSE result END,
		     error_msg = CASE WHEN $3::TEXT IS NOT NULL THEN $3 ELSE error_msg END,
		     http_status = CASE WHEN $4::INT IS NOT NULL THEN $4 ELSE http_status END,
		     duration_ms = CASE WHEN $5::INT IS NOT NULL THEN $5 ELSE duration_ms END,
		     retries_used = CASE WHEN $6::INT IS NOT NULL THEN $6 ELSE retries_used END,
		     completed_at = CASE WHEN $7::TIMESTAMPTZ IS NOT NULL THEN $7 ELSE completed_at END,
		     updated_at = $8
		 WHERE id = $9`,
		status, resultToJSON(result), errorMsg, httpStatus, durationMs, retries, completedAt, now, id,
	)
	if err != nil {
		return fmt.Errorf("update job %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return nil
}

// ListJobs returns a paginated, optionally filtered list of jobs ordered by created_at DESC.
// Valid statusFilter values are "queued", "processing", "completed", and "failed".
// page is 1-based; limit is clamped to [1, 100] and defaults to 20.
func (s *Store) ListJobs(ctx context.Context, page, limit int, statusFilter string) ([]*Job, int, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	offset := (page - 1) * limit

	var total int
	var whereClause string
	var args []any

	if statusFilter != "" {
		whereClause = "WHERE status = $1"
		args = append(args, statusFilter)
		countArgs := []any{statusFilter}
		if err := s.pool.QueryRow(ctx, "SELECT COUNT(*) FROM jobs "+whereClause, countArgs...).Scan(&total); err != nil {
			return nil, 0, fmt.Errorf("count jobs: %w", err)
		}
	} else {
		if err := s.pool.QueryRow(ctx, "SELECT COUNT(*) FROM jobs").Scan(&total); err != nil {
			return nil, 0, fmt.Errorf("count jobs: %w", err)
		}
	}

	query := fmt.Sprintf(
		`SELECT id, url, extract, options::TEXT, status, result::TEXT, error_msg,
		        http_status, retries_used, duration_ms, created_at, updated_at, completed_at
		 FROM jobs %s ORDER BY created_at DESC LIMIT $%d OFFSET $%d`,
		whereClause, len(args)+1, len(args)+2,
	)
	queryArgs := append(args, limit, offset)

	rows, err := s.pool.Query(ctx, query, queryArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()

	var jobs []*Job
	for rows.Next() {
		var job Job
		var resultJSON, errorMsg *string
		var httpStatus *int
		var durationMs *int64
		var completedAt *time.Time
		var optsJSON *string

		if err := rows.Scan(
			&job.ID, &job.URL, &job.Extract, &optsJSON, &job.Status,
			&resultJSON, &errorMsg, &httpStatus, &job.RetriesUsed, &durationMs,
			&job.CreatedAt, &job.UpdatedAt, &completedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("scan job row: %w", err)
		}
		job.Result = parseResultJSON(resultJSON)
		job.Options = parseOptionsJSON(optsJSON)
		job.ErrorMsg = errorMsg
		job.HTTPStatus = httpStatus
		job.DurationMs = durationMs
		job.CompletedAt = completedAt
		jobs = append(jobs, &job)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("rows iteration: %w", err)
	}

	return jobs, total, nil
}

// CancelJob sets a queued job's status to "failed" with error_msg "cancelled".
// Returns ErrNotFound if the job does not exist, or ErrCannotCancel if the job
// is not in "queued" status.
func (s *Store) CancelJob(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE jobs SET status = 'failed', error_msg = 'cancelled', updated_at = now()
		 WHERE id = $1 AND status = 'queued'`,
		id,
	)
	if err != nil {
		return fmt.Errorf("cancel job %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		var current Job
		err := s.pool.QueryRow(ctx, "SELECT status FROM jobs WHERE id = $1", id).Scan(&current.Status)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: %s", ErrNotFound, id)
			}
			return fmt.Errorf("check job %s: %w", id, err)
		}
		return fmt.Errorf("%w: %s", ErrCannotCancel, current.Status)
	}
	return nil
}
