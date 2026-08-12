package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Masralai/web-gobbler/internal/metrics"
	"github.com/Masralai/web-gobbler/internal/queue"
	"github.com/Masralai/web-gobbler/internal/scraper"
	"github.com/Masralai/web-gobbler/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type mockStore struct {
	createJobFunc func(ctx context.Context, job *store.Job) (uuid.UUID, error)
	getJobFunc    func(ctx context.Context, id uuid.UUID) (*store.Job, error)
	listJobsFunc  func(ctx context.Context, page, limit int, statusFilter string) ([]*store.Job, int, error)
	cancelJobFunc func(ctx context.Context, id uuid.UUID) error
	pingFunc      func(ctx context.Context) error
}

func (m *mockStore) CreateJob(ctx context.Context, job *store.Job) (uuid.UUID, error) {
	if m.createJobFunc == nil {
		return uuid.Nil, nil
	}
	return m.createJobFunc(ctx, job)
}

func (m *mockStore) GetJob(ctx context.Context, id uuid.UUID) (*store.Job, error) {
	if m.getJobFunc == nil {
		return nil, nil
	}
	return m.getJobFunc(ctx, id)
}

func (m *mockStore) ListJobs(ctx context.Context, page, limit int, statusFilter string) ([]*store.Job, int, error) {
	if m.listJobsFunc == nil {
		return nil, 0, nil
	}
	return m.listJobsFunc(ctx, page, limit, statusFilter)
}

func (m *mockStore) CancelJob(ctx context.Context, id uuid.UUID) error {
	if m.cancelJobFunc == nil {
		return nil
	}
	return m.cancelJobFunc(ctx, id)
}

func (m *mockStore) Ping(ctx context.Context) error {
	if m.pingFunc == nil {
		return nil
	}
	return m.pingFunc(ctx)
}

type mockQueue struct {
	enqueueFunc    func(ctx context.Context, payload queue.JobPayload) error
	queueDepthFunc func(ctx context.Context) (int64, error)
	pingFunc       func(ctx context.Context) error
}

func (m *mockQueue) Enqueue(ctx context.Context, payload queue.JobPayload) error {
	if m.enqueueFunc == nil {
		return nil
	}
	return m.enqueueFunc(ctx, payload)
}

func (m *mockQueue) QueueDepth(ctx context.Context) (int64, error) {
	if m.queueDepthFunc == nil {
		return 0, nil
	}
	return m.queueDepthFunc(ctx)
}

func (m *mockQueue) Ping(ctx context.Context) error {
	if m.pingFunc == nil {
		return nil
	}
	return m.pingFunc(ctx)
}

func setupTestRouter(h *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	v1 := r.Group("/api/v1")
	h.RegisterRoutes(v1)
	return r
}

func TestHandleScrape_202(t *testing.T) {
	ms := &mockStore{
		createJobFunc: func(ctx context.Context, job *store.Job) (uuid.UUID, error) {
			return uuid.MustParse("f47ac10b-58cc-4372-a567-0e02b2c3d479"), nil
		},
	}
	mq := &mockQueue{
		enqueueFunc: func(ctx context.Context, payload queue.JobPayload) error {
			return nil
		},
	}

	h := NewHandler(ms, mq, 10, 3)
	r := setupTestRouter(h)

	body := `{"url":"https://example.com","extract":["links","headers"]}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/scrape", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d", w.Code)
	}

	var resp JobResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.JobID.String() != "f47ac10b-58cc-4372-a567-0e02b2c3d479" {
		t.Errorf("unexpected job_id: %s", resp.JobID)
	}
	if resp.Status != "queued" {
		t.Errorf("expected queued, got %s", resp.Status)
	}
	if resp.PollURL == nil || *resp.PollURL != "/api/v1/jobs/f47ac10b-58cc-4372-a567-0e02b2c3d479" {
		t.Errorf("unexpected poll_url: %v", resp.PollURL)
	}
}

func TestHandleCrawl_202(t *testing.T) {
	var got queue.JobPayload
	ms := &mockStore{
		createJobFunc: func(ctx context.Context, job *store.Job) (uuid.UUID, error) {
			return uuid.MustParse("f47ac10b-58cc-4372-a567-0e02b2c3d479"), nil
		},
	}
	mq := &mockQueue{
		enqueueFunc: func(ctx context.Context, payload queue.JobPayload) error {
			got = payload
			return nil
		},
	}
	h := NewHandler(ms, mq, 10, 3)
	r := setupTestRouter(h)
	body := `{"url":"https://example.com","extract":["markdown"],"options":{"max_pages":5,"max_depth":1}}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/crawl", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	if got.Kind != queue.KindCrawl {
		t.Errorf("kind=%s", got.Kind)
	}
	if got.Options == nil || got.Options.MaxPages == nil || *got.Options.MaxPages != 5 {
		t.Errorf("options=%+v", got.Options)
	}
}

func TestHandleMap_202(t *testing.T) {
	var got queue.JobPayload
	ms := &mockStore{
		createJobFunc: func(ctx context.Context, job *store.Job) (uuid.UUID, error) {
			return uuid.MustParse("f47ac10b-58cc-4372-a567-0e02b2c3d479"), nil
		},
	}
	mq := &mockQueue{
		enqueueFunc: func(ctx context.Context, payload queue.JobPayload) error {
			got = payload
			return nil
		},
	}
	h := NewHandler(ms, mq, 10, 3)
	r := setupTestRouter(h)
	body := `{"url":"https://example.com","options":{"max_urls":20}}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/map", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", w.Code)
	}
	if got.Kind != queue.KindMap {
		t.Errorf("kind=%s", got.Kind)
	}
}

func TestHandleExtract_501(t *testing.T) {
	h := NewHandler(&mockStore{}, &mockQueue{}, 10, 3)
	r := setupTestRouter(h)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/jobs/f47ac10b-58cc-4372-a567-0e02b2c3d479/extract", strings.NewReader(`{"prompt":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d", w.Code)
	}
}

type mockExtractor struct {
	out json.RawMessage
	err error
}

func (m mockExtractor) Extract(ctx context.Context, markdown string, schema json.RawMessage, prompt string) (json.RawMessage, error) {
	return m.out, m.err
}

func TestHandleExtract_200(t *testing.T) {
	id := uuid.MustParse("f47ac10b-58cc-4372-a567-0e02b2c3d479")
	ms := &mockStore{
		getJobFunc: func(ctx context.Context, jobID uuid.UUID) (*store.Job, error) {
			return &store.Job{
				ID:     id,
				Status: store.JobStatusCompleted,
				Result: &scraper.Result{Markdown: "# Title\n\nBody", HTTPStatus: 200},
			}, nil
		},
	}
	h := NewHandler(ms, &mockQueue{}, 10, 3).WithExtractor(mockExtractor{out: json.RawMessage(`{"title":"Title"}`)}, true)
	r := setupTestRouter(h)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/jobs/"+id.String()+"/extract", strings.NewReader(`{"prompt":"title"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d %s", w.Code, w.Body.String())
	}
}

func TestHandleScrape_400(t *testing.T) {
	ms := &mockStore{}
	mq := &mockQueue{}
	h := NewHandler(ms, mq, 10, 3)
	r := setupTestRouter(h)

	tests := []struct {
		name string
		body string
	}{
		{"empty body", `{}`},
		{"missing url", `{"extract":["links"]}`},
		{"bad url format", `{"url":"not-a-url"}`},
		{"invalid extract type", `{"url":"https://example.com","extract":["invalid"]}`},
		{"timeout too high", `{"url":"https://example.com","options":{"timeout_seconds":100}}`},
		{"timeout too low", `{"url":"https://example.com","options":{"timeout_seconds":0}}`},
		{"retries too high", `{"url":"https://example.com","options":{"max_retries":10}}`},
		{"malformed json", `{"url": broken`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req, _ := http.NewRequest("POST", "/api/v1/scrape", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("expected 400, got %d", w.Code)
			}
			var errResp ErrorResponse
			if err := json.Unmarshal(w.Body.Bytes(), &errResp); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if errResp.Error == "" {
				t.Error("expected non-empty error message")
			}
		})
	}
}

func TestHandleScrape_500(t *testing.T) {
	ms := &mockStore{
		createJobFunc: func(ctx context.Context, job *store.Job) (uuid.UUID, error) {
			return uuid.Nil, errors.New("db error")
		},
	}
	mq := &mockQueue{}
	h := NewHandler(ms, mq, 10, 3)
	r := setupTestRouter(h)

	body := `{"url":"https://example.com"}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/scrape", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestHandleGetJob_200_completed(t *testing.T) {
	id := uuid.MustParse("f47ac10b-58cc-4372-a567-0e02b2c3d479")
	now := time.Now().UTC().Truncate(time.Second)
	completed := now
	durationMs := int64(1240)

	ms := &mockStore{
		getJobFunc: func(ctx context.Context, got uuid.UUID) (*store.Job, error) {
			if got != id {
				t.Errorf("expected id %v, got %v", id, got)
			}
			return &store.Job{
				ID:          id,
				URL:         "https://example.com",
				Extract:     []string{"links"},
				Status:      store.JobStatusCompleted,
				CreatedAt:   now,
				UpdatedAt:   now,
				CompletedAt: &completed,
				DurationMs:  &durationMs,
				Result: &scraper.Result{
					Links:      []string{"https://example.com/about"},
					Headers:    []string{"Welcome"},
					Paragraphs: []string{"Hello world"},
					HTTPStatus: 200,
					DurationMs: 1240,
				},
				RetriesUsed: 0,
			}, nil
		},
	}
	mq := &mockQueue{}
	h := NewHandler(ms, mq, 10, 3)
	r := setupTestRouter(h)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/jobs/f47ac10b-58cc-4372-a567-0e02b2c3d479", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp JobResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != "completed" {
		t.Errorf("expected completed, got %s", resp.Status)
	}
	if resp.Results == nil {
		t.Fatal("expected results")
	}
	if len(resp.Results.Links) != 1 {
		t.Errorf("expected 1 link, got %d", len(resp.Results.Links))
	}
	if resp.Meta == nil {
		t.Fatal("expected meta")
	}
	if resp.Meta.LinksCount != 1 {
		t.Errorf("expected links_count=1, got %d", resp.Meta.LinksCount)
	}
}

func TestHandleGetJob_404(t *testing.T) {
	ms := &mockStore{
		getJobFunc: func(ctx context.Context, id uuid.UUID) (*store.Job, error) {
			return nil, store.ErrNotFound
		},
	}
	mq := &mockQueue{}
	h := NewHandler(ms, mq, 10, 3)
	r := setupTestRouter(h)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/jobs/f47ac10b-58cc-4372-a567-0e02b2c3d479", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHandleGetJob_400_invalid_uuid(t *testing.T) {
	ms := &mockStore{}
	mq := &mockQueue{}
	h := NewHandler(ms, mq, 10, 3)
	r := setupTestRouter(h)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/jobs/not-a-uuid", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleListJobs(t *testing.T) {
	id := uuid.New()
	now := time.Now().UTC().Truncate(time.Second)

	ms := &mockStore{
		listJobsFunc: func(ctx context.Context, page, limit int, statusFilter string) ([]*store.Job, int, error) {
			if page != 1 {
				t.Errorf("expected page 1, got %d", page)
			}
			if limit != 20 {
				t.Errorf("expected limit 20, got %d", limit)
			}
			return []*store.Job{
				{
					ID:        id,
					URL:       "https://example.com",
					Status:    store.JobStatusCompleted,
					CreatedAt: now,
					UpdatedAt: now,
				},
			}, 1, nil
		},
	}
	mq := &mockQueue{}
	h := NewHandler(ms, mq, 10, 3)
	r := setupTestRouter(h)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/jobs", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp PaginatedResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Total != 1 {
		t.Errorf("expected total 1, got %d", resp.Total)
	}
	if len(resp.Jobs) != 1 {
		t.Errorf("expected 1 job, got %d", len(resp.Jobs))
	}
	if resp.Jobs[0].JobID != id {
		t.Errorf("unexpected job id")
	}
}

func TestHandleListJobs_with_status_filter(t *testing.T) {
	ms := &mockStore{
		listJobsFunc: func(ctx context.Context, page, limit int, statusFilter string) ([]*store.Job, int, error) {
			if statusFilter != "queued" {
				t.Errorf("expected status=queued, got %s", statusFilter)
			}
			return []*store.Job{}, 0, nil
		},
	}
	mq := &mockQueue{}
	h := NewHandler(ms, mq, 10, 3)
	r := setupTestRouter(h)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/jobs?status=queued", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandleListJobs_invalid_status(t *testing.T) {
	ms := &mockStore{}
	mq := &mockQueue{}
	h := NewHandler(ms, mq, 10, 3)
	r := setupTestRouter(h)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/jobs?status=invalid", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleDeleteJob_200(t *testing.T) {
	ms := &mockStore{
		cancelJobFunc: func(ctx context.Context, id uuid.UUID) error {
			return nil
		},
	}
	mq := &mockQueue{}
	h := NewHandler(ms, mq, 10, 3)
	r := setupTestRouter(h)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/api/v1/jobs/f47ac10b-58cc-4372-a567-0e02b2c3d479", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandleDeleteJob_404(t *testing.T) {
	ms := &mockStore{
		cancelJobFunc: func(ctx context.Context, id uuid.UUID) error {
			return store.ErrNotFound
		},
	}
	mq := &mockQueue{}
	h := NewHandler(ms, mq, 10, 3)
	r := setupTestRouter(h)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/api/v1/jobs/f47ac10b-58cc-4372-a567-0e02b2c3d479", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHandleDeleteJob_409(t *testing.T) {
	ms := &mockStore{
		cancelJobFunc: func(ctx context.Context, id uuid.UUID) error {
			return fmt.Errorf("%w: processing", store.ErrCannotCancel)
		},
	}
	mq := &mockQueue{}
	h := NewHandler(ms, mq, 10, 3)
	r := setupTestRouter(h)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/api/v1/jobs/f47ac10b-58cc-4372-a567-0e02b2c3d479", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d", w.Code)
	}
}

func TestHandleDeleteJob_400(t *testing.T) {
	ms := &mockStore{}
	mq := &mockQueue{}
	h := NewHandler(ms, mq, 10, 3)
	r := setupTestRouter(h)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/api/v1/jobs/not-a-uuid", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleHealth_ok(t *testing.T) {
	ms := &mockStore{
		pingFunc: func(ctx context.Context) error {
			return nil
		},
	}
	mq := &mockQueue{
		pingFunc: func(ctx context.Context) error {
			return nil
		},
	}
	h := NewHandler(ms, mq, 10, 3)
	r := setupTestRouter(h)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/health", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp HealthResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("expected status ok, got %s", resp.Status)
	}
	if resp.DB != "ok" {
		t.Errorf("expected db ok, got %s", resp.DB)
	}
	if resp.Redis != "ok" {
		t.Errorf("expected redis ok, got %s", resp.Redis)
	}
	if resp.Version != "1.0.0" {
		t.Errorf("expected version 1.0.0, got %s", resp.Version)
	}
}

func TestHandleHealth_degraded(t *testing.T) {
	ms := &mockStore{
		pingFunc: func(ctx context.Context) error {
			return errors.New("connection refused")
		},
	}
	mq := &mockQueue{
		pingFunc: func(ctx context.Context) error {
			return errors.New("timeout")
		},
	}
	h := NewHandler(ms, mq, 10, 3)
	r := setupTestRouter(h)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/health", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}

	var resp HealthResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != "degraded" {
		t.Errorf("expected degraded, got %s", resp.Status)
	}
	if resp.DB == "ok" {
		t.Errorf("expected db error")
	}
}

func TestHandleScrape_storeError_returns_500(t *testing.T) {
	ms := &mockStore{
		createJobFunc: func(ctx context.Context, job *store.Job) (uuid.UUID, error) {
			return uuid.Nil, errors.New("connection error")
		},
	}
	mq := &mockQueue{}
	h := NewHandler(ms, mq, 10, 3)
	r := setupTestRouter(h)

	body := `{"url":"https://example.com"}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/scrape", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestHandleListJobs_pagination_params(t *testing.T) {
	ms := &mockStore{
		listJobsFunc: func(ctx context.Context, page, limit int, statusFilter string) ([]*store.Job, int, error) {
			if page != 2 {
				t.Errorf("expected page 2, got %d", page)
			}
			if limit != 10 {
				t.Errorf("expected limit 10, got %d", limit)
			}
			return []*store.Job{}, 0, nil
		},
	}
	mq := &mockQueue{}
	h := NewHandler(ms, mq, 10, 3)
	r := setupTestRouter(h)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/jobs?page=2&limit=10", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandleScrape_queueError_returns_500(t *testing.T) {
	ms := &mockStore{
		createJobFunc: func(ctx context.Context, job *store.Job) (uuid.UUID, error) {
			return uuid.New(), nil
		},
	}
	mq := &mockQueue{
		enqueueFunc: func(ctx context.Context, payload queue.JobPayload) error {
			return errors.New("redis down")
		},
	}
	h := NewHandler(ms, mq, 10, 3)
	r := setupTestRouter(h)

	body := `{"url":"https://example.com"}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/scrape", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestHandleScrape_metrics(t *testing.T) {
	ms := &mockStore{
		createJobFunc: func(ctx context.Context, job *store.Job) (uuid.UUID, error) {
			return uuid.New(), nil
		},
	}
	mq := &mockQueue{
		enqueueFunc: func(ctx context.Context, payload queue.JobPayload) error {
			return nil
		},
		queueDepthFunc: func(ctx context.Context) (int64, error) {
			return 5, nil
		},
	}

	beforeQueued := testutil.ToFloat64(metrics.JobsTotal.WithLabelValues("queued"))

	h := NewHandler(ms, mq, 10, 3)
	r := setupTestRouter(h)

	body := `{"url":"https://example.com","extract":["links"]}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/scrape", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", w.Code)
	}

	afterQueued := testutil.ToFloat64(metrics.JobsTotal.WithLabelValues("queued"))
	afterDepth := testutil.ToFloat64(metrics.QueueDepth)

	if afterQueued-beforeQueued != 1 {
		t.Errorf("expected JobsTotal{queued} to increment by 1, got delta %f", afterQueued-beforeQueued)
	}
	if afterDepth != 5 {
		t.Errorf("expected QueueDepth to be 5, got %f", afterDepth)
	}
}
