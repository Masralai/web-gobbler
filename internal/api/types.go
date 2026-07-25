// Package api provides the HTTP REST layer for the GoScrape service.
// It defines request/response DTOs, Gin handler functions, middleware
// (rate limiting, security headers, body size limits), request validation,
// and interfaces that decouple the HTTP layer from the store and queue implementations.
package api

import (
	"encoding/json"
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
	MaxPages        *int  `json:"max_pages,omitempty"`
	MaxDepth        *int  `json:"max_depth,omitempty"`
	MaxURLs         *int  `json:"max_urls,omitempty"`
	RenderJS        *bool `json:"render_js,omitempty"`
}

// CrawlRequest is the JSON body for POST /api/v1/crawl.
type CrawlRequest struct {
	URL     string          `json:"url" binding:"required"`
	Extract []string        `json:"extract"`
	Options *RequestOptions `json:"options,omitempty"`
}

// MapRequest is the JSON body for POST /api/v1/map.
type MapRequest struct {
	URL     string          `json:"url" binding:"required"`
	Options *RequestOptions `json:"options,omitempty"`
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
	LinksCount      int  `json:"links_count"`
	HeadersCount    int  `json:"headers_count"`
	ParagraphsCount int  `json:"paragraphs_count"`
	MarkdownLength  int  `json:"markdown_length,omitempty"`
	PagesCrawled    int  `json:"pages_crawled,omitempty"`
	URLCount        int  `json:"url_count,omitempty"`
	HTTPStatus      int  `json:"http_status"`
	RetriesUsed     int  `json:"retries_used"`
	Truncated       bool `json:"truncated,omitempty"`
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

// ExtractRequest is the body for POST /api/v1/jobs/:id/extract.
type ExtractRequest struct {
	Schema json.RawMessage `json:"schema,omitempty"`
	Prompt string          `json:"prompt,omitempty"`
}

// ExtractResponse is returned by successful LLM extract.
type ExtractResponse struct {
	JobID     uuid.UUID       `json:"job_id"`
	Extracted json.RawMessage `json:"extracted,omitempty"`
	Pages     []PageExtract   `json:"pages,omitempty"`
}

// PageExtract is per-page extract output for crawl jobs.
type PageExtract struct {
	URL       string          `json:"url"`
	Extracted json.RawMessage `json:"extracted,omitempty"`
	Error     string          `json:"error,omitempty"`
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
