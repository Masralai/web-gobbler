// Package api provides the HTTP REST layer for the GoScrape service.
// It defines request/response DTOs, Gin handler functions, middleware
// (rate limiting, security headers, body size limits), request validation,
// and interfaces that decouple the HTTP layer from the store and queue implementations.
package api

import (
	"time"

	"github.com/Masralai/web-gobbler/internal/scraper"
	"github.com/google/uuid"
)

// ScrapeRequest is the JSON body accepted by POST /api/v1/scrape.
type ScrapeRequest struct {
	URL     string          `json:"url" binding:"required"`
	Extract []string        `json:"extract"`
	Options *RequestOptions `json:"options,omitempty"`
}

// RequestOptions carries optional per-job scraper configuration in the API request.
type RequestOptions struct {
	TimeoutSeconds  *int  `json:"timeout_seconds,omitempty"`
	MaxRetries      *int  `json:"max_retries,omitempty"`
	FollowRedirects *bool `json:"follow_redirects,omitempty"`
}

// JobResponse is the full response body returned by GET /api/v1/jobs/:id.
type JobResponse struct {
	JobID      uuid.UUID       `json:"job_id"`
	Status     string          `json:"status"`
	URL        string          `json:"url,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  *time.Time      `json:"updated_at,omitempty"`
	Completed  *time.Time      `json:"completed_at,omitempty"`
	DurationMs *int64          `json:"duration_ms,omitempty"`
	Results    *scraper.Result `json:"results,omitempty"`
	Meta       *JobMeta        `json:"meta,omitempty"`
	Error      *string         `json:"error,omitempty"`
	PollURL    *string         `json:"poll_url,omitempty"`
}

// JobMeta holds summary counts and metadata included alongside a completed job response.
type JobMeta struct {
	LinksCount      int `json:"links_count"`
	HeadersCount    int `json:"headers_count"`
	ParagraphsCount int `json:"paragraphs_count"`
	HTTPStatus      int `json:"http_status"`
	RetriesUsed     int `json:"retries_used"`
}

// PaginatedResponse wraps a list of job summaries with total count and pagination info.
type PaginatedResponse struct {
	Jobs  []*JobSummary `json:"jobs"`
	Total int           `json:"total"`
	Page  int           `json:"page"`
	Limit int           `json:"limit"`
}

// JobSummary is a lightweight job representation used in list responses.
type JobSummary struct {
	JobID     uuid.UUID `json:"job_id"`
	URL       string    `json:"url"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ErrorResponse is the standard error body returned on non-2xx responses.
type ErrorResponse struct {
	Error string `json:"error"`
}

// HealthResponse is returned by GET /api/v1/health.
// DB and Redis fields are "ok" or "degraded".
type HealthResponse struct {
	Status  string `json:"status"`
	DB      string `json:"db"`
	Redis   string `json:"redis"`
	Version string `json:"version"`
}
