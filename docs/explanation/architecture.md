# Architecture and design decisions

This document explains the reasoning behind the major architectural choices in GoScrape.

## Why Go?

Go was chosen for three reasons:

- **Concurrency model.** Goroutines are lightweight (2 KB stack) and managed by the runtime scheduler. A single worker process can run hundreds of concurrent scrapers without the overhead of OS threads or the complexity of async/await.
- **Fast compilation.** The entire project compiles in seconds. Combined with static linking and distroless Docker images, the deployment artifact is a single ~50 MB binary with zero runtime dependencies.
- **Standard library HTTP client.** Go's `net/http` has built-in connection pooling, timeout control, context cancellation, and redirect policy — all essential for a scraper that must handle thousands of target domains without resource leaks.

## Why Gin for the HTTP layer?

Go's `net/http` is sufficient for simple APIs, but Gin adds:

- **Router with path parameters.** `r.GET("/jobs/:id", handler)` eliminates manual URL parsing.
- **Middleware chain.** Recovery, rate limiting, security headers, body size limits, and structured request logging compose cleanly as middleware.
- **Request binding and validation.** `c.ShouldBindJSON(&req)` with struct tags reduces boilerplate.

Gin was chosen over alternatives (Chi, Echo, Fiber) because it has the largest ecosystem of middleware and is the most widely deployed Go HTTP framework in production.

## Why Redis for the job queue?

The job queue has three requirements:

1. **Blocking dequeue.** Workers must wait efficiently for new jobs without polling.
2. **At-least-once delivery.** Every job must be processed at least once.
3. **Simple ordering.** Jobs should be processed in FIFO order.

Redis satisfies all three naturally:

- `BRPOP` provides blocking dequeue with a configurable timeout. The worker goroutines block on the Redis connection rather than busy-looping.
- `LPUSH` + `BRPOP` is a classic FIFO list pattern. Redis lists are atomic and durable (with AOF persistence).
- Redis cluster can be used for horizontal scaling of the queue.

Why not a dedicated message broker? RabbitMQ or Kafka would add operational complexity (cluster management, consumer groups, message retention policies) without benefit for this workload. The queue is a simple FIFO pipeline between the API and workers. Redis is already in the stack for caching and rate limiting state.

## Why PostgreSQL for job storage?

The job store needs:

1. **Structured querying.** List jobs by status, pagination, count queries.
2. **JSONB columns.** Scrape results have a flexible schema — different extract types produce different fields.
3. **Transactional updates.** Job status transitions (queued → processing → completed/failed) should be atomic.

PostgreSQL's JSONB type stores the scraper `Result` struct as queryable JSON without requiring a schema migration for each new extract type. The `pgx/v5` driver provides a connection pool, prepared statements, and native UUID support.

## Worker pool design

The worker pool uses a fixed number of goroutines (default 5, configurable via `WORKER_CONCURRENCY`). Each goroutine runs an infinite loop:

1. **Dequeue** — `BRPOP` blocks until a job is available or the timeout (5 seconds) fires.
2. **Claim** — `UPDATE jobs SET status = 'processing'` marks the job so no other worker picks it up.
3. **Rate limit** — the `perDomainRateLimiter` waits if the domain has exceeded its rate limit (default 2 req/s).
4. **Scrape** — `ScrapePage` fetches and parses the page.
5. **Retry loop** — if the scrape fails, wait `100ms × 2ⁿ` and retry, up to `maxRetries` attempts.
6. **Persist** — `UPDATE jobs SET status = 'completed'/'failed'` with the result or error.

This design was chosen over a dynamic worker pool (elastic scaling within a single process) because:

- Fixed pools are predictable — you know exactly how many concurrent HTTP connections the process will make.
- Horizontal scaling happens at the ECS service level (auto-scaling based on queue depth), not within the process.
- Each goroutine is cooperative — it never holds a resource while blocked on I/O.

## Rate limiting (two layers)

GoScrape enforces rate limits at two levels because they solve different problems:

**Per-IP rate limiting** (API layer, `internal/api/middleware.go`):
- Protects the API server from a single client submitting too many jobs.
- Token bucket: 60 requests per minute per IP, burst of 10.
- Visitor entries are purged every 10 minutes to prevent memory leaks.

**Per-domain rate limiting** (Worker layer, `cmd/worker/main.go`):
- Protects target websites from being hammered by the scraper.
- Token bucket: 2 requests per second per hostname, configurable via `SCRAPER_RATE_LIMIT`.
- Each worker process maintains its own limiter map — no cross-process coordination needed because auto-scaling handles load at the service level.

## SSRF protection

Server-Side Request Forgery (SSRF) is a critical vulnerability for any service that fetches arbitrary URLs. GoScrape mitigates this at the scraper level:

1. Before making the HTTP request, the hostname is resolved to an IP address using `net.LookupIP`.
2. The IP is checked against private and loopback ranges (`127.0.0.0/8`, `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `169.254.0.0/16`, `::1/128`).
3. If the IP falls in any private range, the request is rejected with `ErrPrivateIP`.
4. Redirect targets are also resolved and checked — an attacker cannot chain a redirect to an internal service.
5. Redirect chains are limited to 5 hops to prevent loop attacks.

This runs before any HTTP connection is made, so the scraper never even opens a TCP socket to a private address.

## Observability stack

The observability stack (Prometheus + Grafana) was chosen for its ubiquity and zero-cost operation in development:

- **Prometheus** scrapes the `/metrics` endpoint every 15 seconds. The metrics are registered via `promauto`, which eliminates the need for manual registration in a registry.
- **Grafana** auto-provisions the datasource and dashboard via the `docker/grafana/` directory. The dashboard JSON contains five panels covering the key signals for a scraping service: throughput, latency, queue depth, error rate, and retries.
- **Structured logging** uses Go's `log/slog` package with JSON output. All handler requests, worker lifecycle events, and error details are logged with structured key-value pairs for analysis in CloudWatch Logs or Loki.

In production, worker processes also publish `scraper_queue_depth` to CloudWatch every 60 seconds. This metric drives the ECS Service Auto Scaling policy that adjusts the worker count based on backlog pressure.
