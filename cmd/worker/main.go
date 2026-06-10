package main

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/Masralai/web-gobbler/internal/metrics"
	"github.com/Masralai/web-gobbler/internal/queue"
	"github.com/Masralai/web-gobbler/internal/scraper"
	"github.com/Masralai/web-gobbler/internal/store"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/google/uuid"
	"golang.org/x/time/rate"
)

type perDomainRateLimiter struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
	r        rate.Limit
	burst    int
}

func newPerDomainRateLimiter(r rate.Limit, burst int) *perDomainRateLimiter {
	return &perDomainRateLimiter{
		limiters: make(map[string]*rate.Limiter),
		r:        r,
		burst:    burst,
	}
}

func (p *perDomainRateLimiter) Wait(ctx context.Context, hostname string) error {
	p.mu.Lock()
	limiter, ok := p.limiters[hostname]
	if !ok {
		limiter = rate.NewLimiter(p.r, p.burst)
		p.limiters[hostname] = limiter
	}
	p.mu.Unlock()
	return limiter.Wait(ctx)
}

type workerPool struct {
	store       *store.Store
	queue       *queue.Queue
	rateLimiter *perDomainRateLimiter
	defaultOpts scraper.ScrapeOptions
	maxRetries  int
	logger      *slog.Logger
	wg          sync.WaitGroup
	cancel      context.CancelFunc
}

func (wp *workerPool) Run(ctx context.Context, concurrency int) {
	ctx, wp.cancel = context.WithCancel(ctx)
	for i := 0; i < concurrency; i++ {
		wp.wg.Add(1)
		go wp.work(ctx, i)
	}
}

func (wp *workerPool) Stop() {
	wp.cancel()
	wp.wg.Wait()
}

func (wp *workerPool) work(ctx context.Context, id int) {
	defer wp.wg.Done()
	logger := wp.logger.With("worker_id", id)
	logger.Info("worker started")

	for {
		select {
		case <-ctx.Done():
			logger.Info("worker stopping")
			return
		default:
		}

		payload, err := wp.queue.Dequeue(ctx)
		if err != nil {
			logger.Error("dequeue error", "error", err)
			select {
			case <-time.After(time.Second):
			case <-ctx.Done():
				return
			}
			continue
		}

		if payload == nil {
			continue
		}

		wp.processJob(ctx, logger, payload)
	}
}

func (wp *workerPool) processJob(ctx context.Context, logger *slog.Logger, payload *queue.JobPayload) {
	jobID, err := uuid.Parse(payload.JobID)
	if err != nil {
		logger.Error("invalid job id in payload", "job_id", payload.JobID)
		return
	}

	log := logger.With("job_id", jobID, "url", payload.URL)
	log.Info("job dequeued")

	if err := wp.store.UpdateJob(ctx, jobID, store.JobStatusProcessing, nil, nil, 0); err != nil {
		log.Error("failed to mark job as processing", "error", err)
		return
	}

	hostname := extractHostname(payload.URL)
	if err := wp.rateLimiter.Wait(ctx, hostname); err != nil {
		log.Warn("rate limiter cancelled", "error", err)
		return
	}

	maxRetries := wp.maxRetries
	opts := wp.defaultOpts
	if payload.Options != nil {
		if payload.Options.TimeoutSeconds != nil {
			opts.Timeout = time.Duration(*payload.Options.TimeoutSeconds) * time.Second
		}
		if payload.Options.MaxRetries != nil {
			maxRetries = *payload.Options.MaxRetries
		}
		if payload.Options.FollowRedirects != nil {
			opts.FollowRedirects = *payload.Options.FollowRedirects
		}
	}

	var lastResult *scraper.Result
	var lastErr error
	retriesUsed := 0

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(100*(1<<(attempt-1))) * time.Millisecond
			log.Warn("retrying", "attempt", attempt+1, "backoff_ms", backoff.Milliseconds(), "error", lastErr)
			metrics.RetriesTotal.Inc()
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				log.Warn("context cancelled during backoff")
				return
			}
		}

		lastResult, lastErr = scraper.ScrapePage(ctx, payload.URL, payload.Extract, &opts)
		if lastErr == nil {
			break
		}

		retriesUsed = attempt + 1
	}

	if lastErr == nil {
		log.Info("job completed",
			"duration_ms", lastResult.DurationMs,
			"http_status", lastResult.HTTPStatus,
			"links", len(lastResult.Links),
			"headers", len(lastResult.Headers),
			"paragraphs", len(lastResult.Paragraphs),
			"retries_used", retriesUsed,
		)

		if err := wp.store.UpdateJob(ctx, jobID, store.JobStatusCompleted, lastResult, nil, retriesUsed); err != nil {
			log.Error("failed to update job as completed", "error", err)
		}

		metrics.JobsTotal.WithLabelValues("completed").Inc()
		metrics.JobDuration.WithLabelValues("all").Observe(float64(lastResult.DurationMs))
	} else {
		var partial *scraper.Result
		if lastResult != nil {
			partial = &scraper.Result{
				HTTPStatus: lastResult.HTTPStatus,
				DurationMs: lastResult.DurationMs,
			}
		}
		errMsg := lastErr.Error()

		log.Error("job failed",
			"error", errMsg,
			"retries_used", retriesUsed,
		)

		if err := wp.store.UpdateJob(ctx, jobID, store.JobStatusFailed, partial, &errMsg, retriesUsed); err != nil {
			log.Error("failed to update job as failed", "error", err)
		}

		metrics.JobsTotal.WithLabelValues("failed").Inc()
		if errors.Is(lastErr, scraper.ErrHTTPFailed) {
			metrics.HTTPErrorsTotal.WithLabelValues("http_failed").Inc()
		}
	}
}

func extractHostname(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "unknown"
	}
	return parsed.Hostname()
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

func setupLogger(level string) {
	var l slog.Level
	switch level {
	case "DEBUG":
		l = slog.LevelDebug
	case "INFO":
		l = slog.LevelInfo
	case "WARN":
		l = slog.LevelWarn
	case "ERROR":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: l})))
}

func startMetricPublisher(ctx context.Context, q *queue.Queue, cloudwatchClient *cloudwatch.Client, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				depth, err := q.QueueDepth(ctx)
				if err != nil {
					slog.Debug("failed to get queue depth for cloudwatch", "error", err)
					continue
				}

				_, err = cloudwatchClient.PutMetricData(ctx, &cloudwatch.PutMetricDataInput{
					Namespace: aws.String("GoScrape"),
					MetricData: []types.MetricDatum{
						{
							MetricName: aws.String("scraper_queue_depth"),
							Value:      aws.Float64(float64(depth)),
							Unit:       types.StandardUnitCount,
							Dimensions: []types.Dimension{
								{
									Name:  aws.String("Environment"),
									Value: aws.String(getEnv("GOSCRAPE_ENVIRONMENT", "prod")),
								},
							},
						},
					},
				})
				if err != nil {
					slog.Debug("failed to publish queue depth to cloudwatch", "error", err)
				} else {
					metrics.QueueDepth.Set(float64(depth))
				}
			}
		}
	}()
}

func main() {
	ctx := context.Background()

	databaseURL := getEnv("DATABASE_URL", "postgresql://user:pass@localhost:5432/goscrape")
	redisURL := getEnv("REDIS_URL", "redis://localhost:6379")
	concurrency := getEnvInt("WORKER_CONCURRENCY", 5)
	defaultTimeout := getEnvInt("DEFAULT_TIMEOUT_SEC", 10)
	defaultRetries := getEnvInt("DEFAULT_MAX_RETRIES", 3)
	rateLimit := getEnvInt("SCRAPER_RATE_LIMIT", 2)
	logLevel := getEnv("LOG_LEVEL", "INFO")

	setupLogger(logLevel)

	slog.Info("starting worker pool",
		"concurrency", concurrency,
		"default_timeout", defaultTimeout,
		"default_retries", defaultRetries,
		"rate_limit", rateLimit,
		"log_level", logLevel,
	)

	db, err := store.New(ctx, databaseURL)
	if err != nil {
		slog.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	slog.Info("connected to PostgreSQL")

	q, err := queue.New(ctx, redisURL)
	if err != nil {
		slog.Error("failed to connect to redis", "error", err)
		os.Exit(1)
	}
	defer q.Close()
	slog.Info("connected to Redis")

	opts := scraper.DefaultOptions()
	opts.Timeout = time.Duration(defaultTimeout) * time.Second

	rateLimiter := newPerDomainRateLimiter(rate.Limit(rateLimit), rateLimit)

	pool := &workerPool{
		store:       db,
		queue:       q,
		rateLimiter: rateLimiter,
		defaultOpts: *opts,
		maxRetries:  defaultRetries,
		logger:      slog.Default(),
	}

	pool.Run(ctx, concurrency)
	slog.Info("all workers running", "count", concurrency)

	awsCfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		slog.Warn("failed to load AWS config, CloudWatch metrics disabled", "error", err)
	} else {
		cwClient := cloudwatch.NewFromConfig(awsCfg)
		startMetricPublisher(ctx, q, cwClient, 60*time.Second)
		slog.Info("CloudWatch metric publisher started", "interval_sec", 60)
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	slog.Info("shutting down", "signal", sig)

	pool.Stop()
	slog.Info("all workers stopped")
}
