package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/Masralai/web-gobbler/internal/queue"
	"github.com/Masralai/web-gobbler/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Store interface {
	CreateJob(ctx context.Context, job *store.Job) (uuid.UUID, error)
	GetJob(ctx context.Context, id uuid.UUID) (*store.Job, error)
	ListJobs(ctx context.Context, page, limit int, statusFilter string) ([]*store.Job, int, error)
	CancelJob(ctx context.Context, id uuid.UUID) error
	Ping(ctx context.Context) error
}

type Queue interface {
	Enqueue(ctx context.Context, payload queue.JobPayload) error
	QueueDepth(ctx context.Context) (int64, error)
	Ping(ctx context.Context) error
}

type Handler struct {
	store         Store
	queue         Queue
	defaultTimeout  int
	defaultRetries  int
}

func NewHandler(s Store, q Queue, defaultTimeout, defaultRetries int) *Handler {
	return &Handler{
		store:          s,
		queue:          q,
		defaultTimeout: defaultTimeout,
		defaultRetries: defaultRetries,
	}
}

func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/scrape", h.HandleScrape)
	r.GET("/jobs/:id", h.HandleGetJob)
	r.GET("/jobs", h.HandleListJobs)
	r.DELETE("/jobs/:id", h.HandleDeleteJob)
	r.GET("/health", h.HandleHealth)
}

func (h *Handler) HandleScrape(c *gin.Context) {
	var req ScrapeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "invalid request body: " + err.Error()})
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

	jobOpts := &store.JobOptions{
		TimeoutSeconds:  nil,
		MaxRetries:      nil,
		FollowRedirects: nil,
	}
	if req.Options != nil {
		jobOpts.TimeoutSeconds = req.Options.TimeoutSeconds
		jobOpts.MaxRetries = req.Options.MaxRetries
		jobOpts.FollowRedirects = req.Options.FollowRedirects
	}

	job := &store.Job{
		URL:     req.URL,
		Extract: extract,
		Options: jobOpts,
	}

	id, err := h.store.CreateJob(c.Request.Context(), job)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "failed to create job"})
		return
	}

	if d, err := h.queue.QueueDepth(c.Request.Context()); err == nil {
		slog.Debug("queue depth after enqueue", "depth", d)
	}

	payload := queue.JobPayload{
		JobID:   id.String(),
		URL:     req.URL,
		Extract: extract,
		Options: toPayloadOptions(jobOpts),
	}

	if err := h.queue.Enqueue(c.Request.Context(), payload); err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "failed to queue job"})
		return
	}

	pollURL := "/api/v1/jobs/" + id.String()
	c.JSON(http.StatusAccepted, JobResponse{
		JobID:     id,
		Status:    string(store.JobStatusQueued),
		CreatedAt: job.CreatedAt,
		PollURL:   &pollURL,
	})
}

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
		if strings.Contains(err.Error(), "cannot cancel") {
			c.JSON(http.StatusConflict, ErrorResponse{Error: err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "failed to cancel job"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "cancelled"})
}

func (h *Handler) HandleHealth(c *gin.Context) {
	resp := HealthResponse{
		Status:  "ok",
		DB:      "ok",
		Redis:   "ok",
		Version: "1.0.0",
	}

	if err := h.store.Ping(c.Request.Context()); err != nil {
		resp.DB = "error: " + err.Error()
		resp.Status = "degraded"
	}
	if err := h.queue.Ping(c.Request.Context()); err != nil {
		resp.Redis = "error: " + err.Error()
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
				LinksCount:    len(job.Result.Links),
				HeadersCount:  len(job.Result.Headers),
				ParagraphsCount: len(job.Result.Paragraphs),
				HTTPStatus:    job.Result.HTTPStatus,
				RetriesUsed:   job.RetriesUsed,
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
	}
}
