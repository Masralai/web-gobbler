package api

import (
	"time"

	"github.com/Masralai/web-gobbler/internal/scraper"
	"github.com/google/uuid"
)

type ScrapeRequest struct {
	URL     string          `json:"url" binding:"required"`
	Extract []string        `json:"extract"`
	Options *RequestOptions `json:"options,omitempty"`
}

type RequestOptions struct {
	TimeoutSeconds  *int  `json:"timeout_seconds,omitempty"`
	MaxRetries      *int  `json:"max_retries,omitempty"`
	FollowRedirects *bool `json:"follow_redirects,omitempty"`
}

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

type JobMeta struct {
	LinksCount    int `json:"links_count"`
	HeadersCount  int `json:"headers_count"`
	ParagraphsCount int `json:"paragraphs_count"`
	HTTPStatus    int `json:"http_status"`
	RetriesUsed   int `json:"retries_used"`
}

type PaginatedResponse struct {
	Jobs  []*JobSummary `json:"jobs"`
	Total int           `json:"total"`
	Page  int           `json:"page"`
	Limit int           `json:"limit"`
}

type JobSummary struct {
	JobID     uuid.UUID  `json:"job_id"`
	URL       string     `json:"url"`
	Status    string     `json:"status"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

type HealthResponse struct {
	Status  string `json:"status"`
	DB      string `json:"db"`
	Redis   string `json:"redis"`
	Version string `json:"version"`
}
