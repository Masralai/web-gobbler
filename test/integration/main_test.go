//go:build integration

package integration

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Masralai/web-gobbler/internal/queue"
	"github.com/Masralai/web-gobbler/internal/scraper"
	"github.com/Masralai/web-gobbler/internal/store"
	"github.com/google/uuid"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/modules/redis"
)

func connectStore(ctx context.Context, connStr string) (*store.Store, error) {
	var lastErr error
	for i := 0; i < 10; i++ {
		s, err := store.New(ctx, connStr)
		if err == nil {
			return s, nil
		}
		lastErr = err
		time.Sleep(time.Duration(100*(1<<i)) * time.Millisecond)
	}
	return nil, fmt.Errorf("connect store after 10 retries: %w", lastErr)
}

var (
	testStore *store.Store
	testQueue *queue.Queue
)

func TestMain(m *testing.M) {
	ctx := context.Background()

	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("goscrape"),
		postgres.WithUsername("user"),
		postgres.WithPassword("pass"),
		postgres.WithInitScripts("../../migrations/000001_create_jobs_table.up.sql"),
	)
	if err != nil {
		panic("failed to start postgres container: " + err.Error())
	}

	pgConnStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		panic("failed to get postgres connection string: " + err.Error())
	}

	redisContainer, err := redis.Run(ctx,
		"redis:7-alpine",
	)
	if err != nil {
		panic("failed to start redis container: " + err.Error())
	}

	redisConnStr, err := redisContainer.ConnectionString(ctx)
	if err != nil {
		panic("failed to get redis connection string: " + err.Error())
	}

	s, err := connectStore(ctx, pgConnStr)
	if err != nil {
		panic("failed to create store: " + err.Error())
	}
	testStore = s

	q, err := queue.New(ctx, redisConnStr)
	if err != nil {
		panic("failed to create queue: " + err.Error())
	}
	testQueue = q

	code := m.Run()

	testQueue.Close()
	testStore.Close()
	containerTerminate(ctx, pgContainer)
	containerTerminate(ctx, redisContainer)
	os.Exit(code)
}

func containerTerminate(ctx context.Context, c testcontainers.Container) {
	_ = c.Terminate(ctx)
}

func TestStoreCreateAndGetJob(t *testing.T) {
	ctx := context.Background()

	timeout := 15
	job := &store.Job{
		URL:     "https://example.com",
		Extract: []string{"links", "headers"},
		Options: &store.JobOptions{TimeoutSeconds: &timeout},
	}

	id, err := testStore.CreateJob(ctx, job)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if id == uuid.Nil {
		t.Fatal("expected non-nil job id")
	}

	got, err := testStore.GetJob(ctx, id)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}

	if got.URL != "https://example.com" {
		t.Errorf("expected url example.com, got %s", got.URL)
	}
	if len(got.Extract) != 2 || got.Extract[0] != "links" {
		t.Errorf("unexpected extract: %v", got.Extract)
	}
	if got.Status != store.JobStatusQueued {
		t.Errorf("expected status queued, got %s", got.Status)
	}
	if got.CreatedAt.IsZero() {
		t.Error("expected non-zero created_at")
	}
	if got.Options == nil || *got.Options.TimeoutSeconds != 15 {
		t.Error("expected options with timeout_seconds=15")
	}
}

func TestStoreUpdateAndGetCompletedJob(t *testing.T) {
	ctx := context.Background()

	job := &store.Job{
		URL:     "https://example.org",
		Extract: []string{"paragraphs"},
	}

	id, err := testStore.CreateJob(ctx, job)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	durationMs := int64(500)
	result := &scraper.Result{
		Links:      []string{"https://example.org/about"},
		Headers:    []string{"Welcome"},
		Paragraphs: []string{"Hello world"},
		HTTPStatus: 200,
		DurationMs: durationMs,
	}

	err = testStore.UpdateJob(ctx, id, store.JobStatusCompleted, result, nil, 1)
	if err != nil {
		t.Fatalf("UpdateJob: %v", err)
	}

	got, err := testStore.GetJob(ctx, id)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}

	if got.Status != store.JobStatusCompleted {
		t.Errorf("expected completed, got %s", got.Status)
	}
	if got.Result == nil {
		t.Fatal("expected result")
	}
	if len(got.Result.Links) != 1 {
		t.Errorf("expected 1 link, got %d", len(got.Result.Links))
	}
	if got.Result.HTTPStatus != 200 {
		t.Errorf("expected http_status 200, got %d", got.Result.HTTPStatus)
	}
	if *got.DurationMs != durationMs {
		t.Errorf("expected duration %d, got %d", durationMs, *got.DurationMs)
	}
	if got.CompletedAt == nil {
		t.Fatal("expected completed_at")
	}
	if got.RetriesUsed != 1 {
		t.Errorf("expected retries_used 1, got %d", got.RetriesUsed)
	}
}

func TestStoreGetJobNotFound(t *testing.T) {
	ctx := context.Background()

	_, err := testStore.GetJob(ctx, uuid.New())
	if err == nil {
		t.Fatal("expected error for non-existent job")
	}
}

func TestStoreListJobs(t *testing.T) {
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		_, err := testStore.CreateJob(ctx, &store.Job{
			URL:     "https://example.com",
			Extract: []string{"links"},
		})
		if err != nil {
			t.Fatalf("CreateJob: %v", err)
		}
	}

	jobs, total, err := testStore.ListJobs(ctx, 1, 20, "")
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}

	if len(jobs) == 0 {
		t.Error("expected at least 1 job")
	}
	if total < 3 {
		t.Errorf("expected total >= 3, got %d", total)
	}

	queuedJobs, queuedTotal, err := testStore.ListJobs(ctx, 1, 20, "queued")
	if err != nil {
		t.Fatalf("ListJobs with filter: %v", err)
	}
	if len(queuedJobs) == 0 {
		t.Error("expected at least 1 queued job")
	}
	if queuedTotal < 3 {
		t.Errorf("expected queued total >= 3, got %d", queuedTotal)
	}
}

func TestStoreCancelQueuedJob(t *testing.T) {
	ctx := context.Background()

	job := &store.Job{
		URL:     "https://example.com",
		Extract: []string{"links"},
	}
	id, err := testStore.CreateJob(ctx, job)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	err = testStore.CancelJob(ctx, id)
	if err != nil {
		t.Fatalf("CancelJob: %v", err)
	}

	got, err := testStore.GetJob(ctx, id)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Status != store.JobStatusFailed {
		t.Errorf("expected failed after cancel, got %s", got.Status)
	}
}

func TestStoreCancelAlreadyProcessingJob(t *testing.T) {
	ctx := context.Background()

	job := &store.Job{
		URL:     "https://example.com",
		Extract: []string{"links"},
	}
	id, err := testStore.CreateJob(ctx, job)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	err = testStore.UpdateJob(ctx, id, store.JobStatusProcessing, nil, nil, 0)
	if err != nil {
		t.Fatalf("UpdateJob to processing: %v", err)
	}

	err = testStore.CancelJob(ctx, id)
	if err == nil {
		t.Error("expected error cancelling processing job")
	}
}

func TestQueueEnqueueDequeue(t *testing.T) {
	ctx := context.Background()

	timeout := 10
	follow := true
	payload := queue.JobPayload{
		JobID:   uuid.New().String(),
		URL:     "https://example.com",
		Extract: []string{"links", "headers"},
		Options: &queue.PayloadOptions{
			TimeoutSeconds:  &timeout,
			FollowRedirects: &follow,
		},
	}

	err := testQueue.Enqueue(ctx, payload)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	depth, err := testQueue.QueueDepth(ctx)
	if err != nil {
		t.Fatalf("QueueDepth: %v", err)
	}
	if depth < 1 {
		t.Errorf("expected depth >= 1, got %d", depth)
	}

	got, err := testQueue.Dequeue(ctx)
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil payload")
	}

	if got.JobID != payload.JobID {
		t.Errorf("expected job_id %s, got %s", payload.JobID, got.JobID)
	}
	if got.URL != "https://example.com" {
		t.Errorf("expected url example.com, got %s", got.URL)
	}
	if len(got.Extract) != 2 {
		t.Errorf("expected 2 extract types, got %d", len(got.Extract))
	}
	if got.Options == nil {
		t.Fatal("expected options")
	}
	if *got.Options.TimeoutSeconds != 10 {
		t.Errorf("expected timeout 10, got %d", *got.Options.TimeoutSeconds)
	}
}

func TestQueueDequeueEmpty(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	got, err := testQueue.Dequeue(ctx)
	if err != nil {
		t.Fatalf("Dequeue on empty queue: %v", err)
	}
	if got != nil {
		t.Error("expected nil payload on empty queue")
	}
}

func TestQueueDepth(t *testing.T) {
	ctx := context.Background()

	depth, err := testQueue.QueueDepth(ctx)
	if err != nil {
		t.Fatalf("QueueDepth: %v", err)
	}
	if depth < 0 {
		t.Errorf("expected depth >= 0, got %d", depth)
	}
}

func TestStorePing(t *testing.T) {
	ctx := context.Background()
	if err := testStore.Ping(ctx); err != nil {
		t.Fatalf("Ping store: %v", err)
	}
}

func TestQueuePing(t *testing.T) {
	ctx := context.Background()
	if err := testQueue.Ping(ctx); err != nil {
		t.Fatalf("Ping queue: %v", err)
	}
}
