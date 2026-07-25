package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/Masralai/web-gobbler/internal/extract"
	"github.com/Masralai/web-gobbler/internal/metrics"
	"github.com/Masralai/web-gobbler/internal/queue"
	"github.com/Masralai/web-gobbler/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Store defines the persistence contract required by the HTTP handlers.
// It mirrors the methods of store.Store to enable mock-based testing.
type Store interface {
	CreateJob(ctx context.Context, job *store.Job) (uuid.UUID, error)
	GetJob(ctx context.Context, id uuid.UUID) (*store.Job, error)
	ListJobs(ctx context.Context, page, limit int, statusFilter string) ([]*store.Job, int, error)
	CancelJob(ctx context.Context, id uuid.UUID) error
	Ping(ctx context.Context) error
}

// Queue defines the job queue contract required by the HTTP handlers.
// It mirrors the methods of queue.Queue to enable mock-based testing.
type Queue interface {
	Enqueue(ctx context.Context, payload queue.JobPayload) error
	QueueDepth(ctx context.Context) (int64, error)
	Ping(ctx context.Context) error
}

// Handler wires a Store and Queue together and exposes them as Gin handler functions.
type Handler struct {
	store          Store
	queue          Queue
	defaultTimeout int
	defaultRetries int
	extractor      extract.Extractor
	llmEnabled     bool
}

// NewHandler creates a Handler with the given store, queue, and default scraper parameters.
func NewHandler(s Store, q Queue, defaultTimeout, defaultRetries int) *Handler {
	return &Handler{
		store:          s,
		queue:          q,
		defaultTimeout: defaultTimeout,
		defaultRetries: defaultRetries,
	}
}

// WithExtractor attaches an optional LLM extractor (P5).
func (h *Handler) WithExtractor(ex extract.Extractor, enabled bool) *Handler {
	h.extractor = ex
	h.llmEnabled = enabled
	return h
}

// RegisterRoutes attaches all handler methods to the given router group.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/scrape", h.HandleScrape)
	r.POST("/crawl", h.HandleCrawl)
	r.POST("/map", h.HandleMap)
	r.GET("/jobs/:id", h.HandleGetJob)
	r.GET("/jobs", h.HandleListJobs)
	r.DELETE("/jobs/:id", h.HandleDeleteJob)
	r.POST("/jobs/:id/extract", h.HandleExtract)
	r.GET("/health", h.HandleHealth)
}

// HandleScrape processes POST /api/v1/scrape.
func (h *Handler) HandleScrape(c *gin.Context) {
	var req ScrapeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		slog.Debug("invalid request body", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "invalid request body"})
		return
	}

	if err := validateScrapeRequest(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}

	extract := req.Extract
	if len(extract) == 0 {
		extract = []string{"links"}
	}

	h.acceptJob(c, queue.KindScrape, req.URL, extract, req.Options)
}

// HandleCrawl processes POST /api/v1/crawl.
func (h *Handler) HandleCrawl(c *gin.Context) {
	var req CrawlRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		slog.Debug("invalid request body", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "invalid request body"})
		return
	}
	if err := validateCrawlRequest(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}
	extract := req.Extract
	if len(extract) == 0 {
		extract = []string{"markdown"}
	}
	opts := req.Options
	if opts == nil {
		opts = &RequestOptions{}
	}
	if opts.MaxPages == nil {
		v := 10
		opts.MaxPages = &v
	}
	if opts.MaxDepth == nil {
		v := 2
		opts.MaxDepth = &v
	}
	h.acceptJob(c, queue.KindCrawl, req.URL, extract, opts)
}

// HandleMap processes POST /api/v1/map.
func (h *Handler) HandleMap(c *gin.Context) {
	var req MapRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		slog.Debug("invalid request body", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "invalid request body"})
		return
	}
	if err := validateMapRequest(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}
	opts := req.Options
	if opts == nil {
		opts = &RequestOptions{}
	}
	if opts.MaxURLs == nil {
		v := 50
		opts.MaxURLs = &v
	}
	if opts.MaxDepth == nil {
		v := 2
		opts.MaxDepth = &v
	}
	h.acceptJob(c, queue.KindMap, req.URL, []string{"links"}, opts)
}

func (h *Handler) acceptJob(c *gin.Context, kind, rawURL string, extract []string, reqOpts *RequestOptions) {
	jobOpts := requestToJobOptions(reqOpts)

	job := &store.Job{
		URL:     rawURL,
		Extract: extract,
		Options: jobOpts,
	}

	id, err := h.store.CreateJob(c.Request.Context(), job)
	if err != nil {
		slog.Error("failed to create job", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "failed to create job"})
		return
	}

	payload := queue.JobPayload{
		JobID:   id.String(),
		Kind:    kind,
		URL:     rawURL,
		Extract: extract,
		Options: toPayloadOptions(jobOpts),
	}

	if err := h.queue.Enqueue(c.Request.Context(), payload); err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "failed to queue job"})
		return
	}

	if d, err := h.queue.QueueDepth(c.Request.Context()); err == nil {
		metrics.QueueDepth.Set(float64(d))
	}

	metrics.JobsTotal.WithLabelValues("queued").Inc()

	pollURL := "/api/v1/jobs/" + id.String()
	c.JSON(http.StatusAccepted, JobResponse{
		JobID:     id,
		Status:    string(store.JobStatusQueued),
		CreatedAt: job.CreatedAt,
		PollURL:   &pollURL,
	})
}

func requestToJobOptions(reqOpts *RequestOptions) *store.JobOptions {
	jobOpts := &store.JobOptions{}
	if reqOpts == nil {
		return jobOpts
	}
	jobOpts.TimeoutSeconds = reqOpts.TimeoutSeconds
	jobOpts.MaxRetries = reqOpts.MaxRetries
	jobOpts.FollowRedirects = reqOpts.FollowRedirects
	jobOpts.MaxPages = reqOpts.MaxPages
	jobOpts.MaxDepth = reqOpts.MaxDepth
	jobOpts.MaxURLs = reqOpts.MaxURLs
	jobOpts.RenderJS = reqOpts.RenderJS
	return jobOpts
}

// HandleGetJob processes GET /api/v1/jobs/:id.
func (h *Handler) HandleGetJob(c *gin.Context) {
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "invalid job id"})
		return
	}

	job, err := h.store.GetJob(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, ErrorResponse{Error: "job not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "failed to get job"})
		return
	}

	resp := jobToResponse(job)
	c.JSON(http.StatusOK, resp)
}

// HandleListJobs processes GET /api/v1/jobs.
// Supports pagination (?page=1&limit=20) and optional status filtering (?status=queued).
func (h *Handler) HandleListJobs(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	statusFilter := c.Query("status")

	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	if statusFilter != "" {
		valid := false
		for _, s := range []string{"queued", "processing", "completed", "failed"} {
			if statusFilter == s {
				valid = true
				break
			}
		}
		if !valid {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error: "invalid status filter: must be queued, processing, completed, or failed",
			})
			return
		}
	}

	jobs, total, err := h.store.ListJobs(c.Request.Context(), page, limit, statusFilter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "failed to list jobs"})
		return
	}

	summaries := make([]*JobSummary, 0, len(jobs))
	for _, j := range jobs {
		summaries = append(summaries, &JobSummary{
			JobID:     j.ID,
			URL:       j.URL,
			Status:    string(j.Status),
			CreatedAt: j.CreatedAt,
			UpdatedAt: j.UpdatedAt,
		})
	}

	c.JSON(http.StatusOK, PaginatedResponse{
		Jobs:  summaries,
		Total: total,
		Page:  page,
		Limit: limit,
	})
}

// HandleDeleteJob processes DELETE /api/v1/jobs/:id.
// Cancels a queued job. Returns 409 if the job is already processing, completed, or failed.
func (h *Handler) HandleDeleteJob(c *gin.Context) {
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "invalid job id"})
		return
	}

	err = h.store.CancelJob(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, ErrorResponse{Error: "job not found"})
			return
		}
		if errors.Is(err, store.ErrCannotCancel) {
			c.JSON(http.StatusConflict, ErrorResponse{Error: err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "failed to cancel job"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "cancelled"})
}

// HandleExtract processes POST /api/v1/jobs/:id/extract (P5).
func (h *Handler) HandleExtract(c *gin.Context) {
	if !h.llmEnabled || h.extractor == nil {
		c.JSON(http.StatusNotImplemented, ErrorResponse{Error: "LLM extract not configured (set LLM_API_KEY)"})
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "invalid job id"})
		return
	}

	var req ExtractRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "invalid request body"})
		return
	}
	if len(req.Schema) == 0 && strings.TrimSpace(req.Prompt) == "" {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "schema or prompt is required"})
		return
	}
	if len(req.Prompt) > 4000 {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "prompt too long"})
		return
	}

	job, err := h.store.GetJob(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, ErrorResponse{Error: "job not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "failed to get job"})
		return
	}
	if job.Status != store.JobStatusCompleted || job.Result == nil {
		c.JSON(http.StatusConflict, ErrorResponse{Error: "job must be completed with results"})
		return
	}

	ctx := c.Request.Context()
	// Crawl: per-page extract
	if len(job.Result.Pages) > 0 {
		pages := make([]PageExtract, 0, len(job.Result.Pages))
		for _, p := range job.Result.Pages {
			pe := PageExtract{URL: p.URL}
			if p.Markdown == "" {
				pe.Error = "no markdown"
				pages = append(pages, pe)
				continue
			}
			out, err := h.extractor.Extract(ctx, p.Markdown, req.Schema, req.Prompt)
			if err != nil {
				pe.Error = "extract failed"
				slog.Error("llm extract page failed", "job_id", id, "url", p.URL)
			} else {
				pe.Extracted = out
			}
			pages = append(pages, pe)
		}
		c.JSON(http.StatusOK, ExtractResponse{JobID: id, Pages: pages})
		return
	}

	if job.Result.Markdown == "" {
		c.JSON(http.StatusConflict, ErrorResponse{Error: "job has no markdown; scrape with extract markdown first"})
		return
	}
	out, err := h.extractor.Extract(ctx, job.Result.Markdown, req.Schema, req.Prompt)
	if err != nil {
		slog.Error("llm extract failed", "job_id", id)
		c.JSON(http.StatusBadGateway, ErrorResponse{Error: "extract failed"})
		return
	}
	c.JSON(http.StatusOK, ExtractResponse{JobID: id, Extracted: out})
}

// HandleHealth processes GET /api/v1/health.
// Pings the store and queue; returns 200 with "ok" or 503 with "degraded".
// Error details are logged serverside and not exposed in the response body.
func (h *Handler) HandleHealth(c *gin.Context) {
	resp := HealthResponse{
		Status:  "ok",
		DB:      "ok",
		Redis:   "ok",
		Version: "1.0.0",
	}

	if err := h.store.Ping(c.Request.Context()); err != nil {
		slog.Error("health check: db ping failed", "error", err)
		resp.DB = "degraded"
		resp.Status = "degraded"
	}
	if err := h.queue.Ping(c.Request.Context()); err != nil {
		slog.Error("health check: redis ping failed", "error", err)
		resp.Redis = "degraded"
		resp.Status = "degraded"
	}

	statusCode := http.StatusOK
	if resp.Status == "degraded" {
		statusCode = http.StatusServiceUnavailable
	}

	c.JSON(statusCode, resp)
}

func jobToResponse(job *store.Job) JobResponse {
	resp := JobResponse{
		JobID:     job.ID,
		Status:    string(job.Status),
		URL:       job.URL,
		CreatedAt: job.CreatedAt,
	}

	if job.UpdatedAt != (job.UpdatedAt) {
		resp.UpdatedAt = &job.UpdatedAt
	}
	resp.Completed = job.CompletedAt
	resp.DurationMs = job.DurationMs

	switch job.Status {
	case store.JobStatusCompleted:
		resp.Results = job.Result
		if job.Result != nil {
			resp.Meta = &JobMeta{
				LinksCount:      len(job.Result.Links),
				HeadersCount:    len(job.Result.Headers),
				ParagraphsCount: len(job.Result.Paragraphs),
				MarkdownLength:  len(job.Result.Markdown),
				PagesCrawled:    job.Result.PagesCrawled,
				URLCount:        len(job.Result.URLs),
				HTTPStatus:      job.Result.HTTPStatus,
				RetriesUsed:     job.RetriesUsed,
				Truncated:       job.Result.Truncated,
			}
		}
	case store.JobStatusFailed:
		resp.Error = job.ErrorMsg
	}

	return resp
}

func toPayloadOptions(opts *store.JobOptions) *queue.PayloadOptions {
	if opts == nil {
		return nil
	}
	return &queue.PayloadOptions{
		TimeoutSeconds:  opts.TimeoutSeconds,
		MaxRetries:      opts.MaxRetries,
		FollowRedirects: opts.FollowRedirects,
		MaxPages:        opts.MaxPages,
		MaxDepth:        opts.MaxDepth,
		MaxURLs:         opts.MaxURLs,
		RenderJS:        opts.RenderJS,
	}
}
